// Package redisuser reads the user directory of the dash application from
// Redis, read-only.
//
// dash keeps one hash per user under the key "app_user_<ee_code>_<name>_<email>"
// (lower case). Every field of the hash holds zlib.compress(pickle.dumps(value)),
// and the password is stored in plain text. This package finds a user the way
// dash's load_user_data() does, decodes the fields with a restricted pickle
// reader that never executes anything (see pickle.go), and returns a Record.
//
// Open does not touch the network, so a service can start while Redis is down.
// Every failure to talk to Redis is reported as ErrUnavailable. The package
// never writes to Redis, never uses KEYS, bounds every scan, treats user input
// as data (it can never act as a glob pattern) and never lets the password of
// the connection URL or of a user appear in an error, a log value or a report.
package redisuser

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

var (
	ErrNotFound    = errors.New("user not found")             // nothing matched
	ErrAmbiguous   = errors.New("more than one user matches") // more than one distinct ee_code among the matches
	ErrUnavailable = errors.New("user directory unavailable") // network, timeout, authentication or protocol failure
	ErrUnreadable  = errors.New("user record unreadable")     // a candidate record could not be decoded and nothing readable matched
)

const (
	defaultPrefix  = "app_user_"
	defaultTimeout = 3 * time.Second
	defaultMaxScan = 200
	maxScanLimit   = 100_000
	poolSize       = 4
	scanCount      = 1000 // COUNT hint of every SCAN call
	pipelineChunk  = 50   // HGETALL commands per round trip
	maxLogin       = 254  // runes, the longest e-mail address
	maxRef         = 1024 // bytes of a key reference that Get accepts
)

// Options configures a Store.
type Options struct {
	URL       string        // redis://[user:password@]host:port[/db] or rediss://...; required
	KeyPrefix string        // default "app_user_"
	Timeout   time.Duration // dial, read and write timeout; default 3s
	MaxScan   int           // most candidate keys a Lookup accepts; more are ErrAmbiguous; default 200
}

// String shows where URL points but not the credentials in it, so a logged
// Options never leaks them.
func (o Options) String() string {
	shown := "<invalid>"
	if u, err := url.Parse(o.URL); err == nil {
		u.User = nil
		shown = u.String()
	}
	return fmt.Sprintf("redisuser.Options{URL: %s, KeyPrefix: %q, Timeout: %s, MaxScan: %d}", shown, o.KeyPrefix, o.Timeout, o.MaxScan)
}

func (o Options) GoString() string { return o.String() }

// Store is the user directory. It is safe for concurrent use.
type Store struct {
	rdb     *redis.Client
	prefix  string
	glob    string // prefix as a literal glob pattern
	maxScan int
	budget  time.Duration // what Lookup and Get may spend in total
	addr    string
	secret  string // password of the URL, scrubbed from errors
}

// quietLogger replaces the logger go-redis writes to stderr. The errors are
// returned to the caller, which decides what to log.
type quietLogger struct{}

func (quietLogger) Printf(context.Context, string, ...any) {}

var silence sync.Once

// Open validates o and prepares the client. It does not connect.
func Open(o Options) (*Store, error) {
	ro, secret, err := parseURL(o.URL)
	if err != nil {
		return nil, err
	}
	prefix := cmp.Or(o.KeyPrefix, defaultPrefix)
	timeout := cmp.Or(o.Timeout, defaultTimeout)
	maxScan := cmp.Or(o.MaxScan, defaultMaxScan)
	switch {
	case hasControl(prefix):
		return nil, errors.New("redisuser: key prefix must not contain control characters")
	case timeout < 0:
		return nil, errors.New("redisuser: timeout must not be negative")
	case maxScan < 0 || maxScan > maxScanLimit:
		return nil, fmt.Errorf("redisuser: max scan must be between 1 and %d", maxScanLimit)
	}

	// These override whatever the URL query says. RESP2 and no CLIENT SETINFO
	// suit the Redis 3.x and 5.x servers dash may run on: go-redis still sends
	// HELLO 2 first, and falls back to AUTH when the server does not know it.
	// Idle connections are not dialed ahead, because Open must not touch the
	// network.
	ro.Protocol = 2
	ro.DisableIdentity = true
	ro.MaxRetries = 1
	ro.DialerRetries = 1
	ro.PoolSize, ro.MaxActiveConns = poolSize, poolSize
	ro.MinIdleConns, ro.MaxIdleConns = 0, 0
	ro.DialTimeout, ro.ReadTimeout, ro.WriteTimeout = timeout, timeout, timeout
	ro.ContextTimeoutEnabled = true
	silence.Do(func() { redis.SetLogger(quietLogger{}) })

	s := &Store{
		rdb:     redis.NewClient(ro),
		prefix:  prefix,
		glob:    escapeGlob(prefix),
		maxScan: maxScan,
		budget:  5 * timeout,
		addr:    fmt.Sprintf("%s/%d", ro.Addr, ro.DB),
		secret:  secret,
	}
	if ro.TLSConfig != nil {
		s.addr += " (tls)"
	}
	return s, nil
}

// parseURL checks the URL and returns the client options and the password in
// it. No error it returns contains the URL or the password.
func parseURL(raw string) (*redis.Options, string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, "", errors.New("redisuser: url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", errors.New("redisuser: url is not valid") // the cause would quote the URL
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return nil, "", errors.New("redisuser: url scheme must be redis or rediss")
	}
	if u.Hostname() == "" {
		return nil, "", errors.New("redisuser: url has no host")
	}
	secret, _ := u.User.Password()
	ro, err := redis.ParseURL(raw)
	if err != nil {
		return nil, "", fmt.Errorf("redisuser: %s", mask(err.Error(), secret))
	}
	return ro, secret, nil
}

// Close releases the connections.
func (s *Store) Close() error { return s.rdb.Close() }

// Addr describes the server for display: "host:port/db", plus " (tls)". It never contains credentials.
func (s *Store) Addr() string { return s.addr }

// Ping checks that Redis answers. Errors wrap ErrUnavailable.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		return s.unavailable(err)
	}
	return nil
}

// Lookup finds the user a person typed at the login form: an ee_code, a name
// or an e-mail address. It applies the rules of load_user_data() in dash, with
// one difference: dash's fillna(0) lets a missing password read as "0", while
// here a record without a password has none and CheckPassword is false for it.
//
// Candidate keys are those that contain the typed text (up to the e-mail's "@")
// in lower case. Among them an e-mail address matches the Email, or its part
// before "@" the ee_code; anything else matches the Name exactly or the ee_code.
// The newest record wins per (Email, Name). If the winners belong to more than
// one ee_code the result is ErrAmbiguous. A user that is not activated is
// returned as such; it is up to the caller to refuse the sign-in.
func (s *Store) Lookup(ctx context.Context, login string) (Record, error) {
	login, ok := cleanToken(login)
	if !ok {
		return Record{}, ErrNotFound
	}
	local, _, _ := strings.Cut(strings.ToLower(login), "@")
	if local == "" {
		return Record{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()
	recs, unreadable, err := s.load(ctx, s.glob+"*"+escapeGlob(local)+"*")
	if err != nil {
		return Record{}, err
	}
	matches := recs[:0]
	matcher := loginMatcher(login)
	for _, r := range recs {
		if matcher(r) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return Record{}, noMatch(unreadable)
	}
	sortNewest(matches)
	seen := map[[2]string]bool{}
	for _, r := range matches {
		k := [2]string{r.Email, r.Name}
		if seen[k] {
			continue
		}
		seen[k] = true
		if !strings.EqualFold(r.ECode, matches[0].ECode) {
			return Record{}, ErrAmbiguous
		}
	}
	return matches[0], nil
}

// Get reads a user again during a session. It reads ref, the key of an earlier
// Record, and uses it if that record still belongs to ecode. dash moves a
// record to a new key when the e-mail changes, so when the key is gone it
// falls back to the newest record of ecode (case-insensitive). ErrNotFound
// means neither exists; ErrUnreadable that a record exists but none could be
// decoded. Keys outside the prefix of the directory are never read.
func (s *Store) Get(ctx context.Context, ref, ecode string) (Record, error) {
	ecode = strings.TrimSpace(ecode)
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()
	directlyUnreadable := 0
	if ref != "" && len(ref) <= maxRef && strings.HasPrefix(ref, s.prefix) && !hasControl(ref) {
		got, err := s.hgetall(ctx, []string{ref}, 0)
		if err != nil {
			return Record{}, err
		}
		if got[0].exists() {
			h := decodeHash(ref, got[0].hash, false)
			switch {
			case !h.usable:
				directlyUnreadable = 1
			case ecode == "" || strings.EqualFold(h.rec.ECode, ecode):
				return h.rec, nil
			}
		}
	}
	ecode, ok := cleanToken(ecode)
	if !ok {
		return Record{}, noMatch(directlyUnreadable)
	}
	recs, unreadable, err := s.load(ctx, s.glob+escapeGlob(strings.ToLower(ecode))+"_*")
	if err != nil {
		return Record{}, err
	}
	mine := recs[:0]
	for _, r := range recs {
		if strings.EqualFold(r.ECode, ecode) {
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		return Record{}, noMatch(max(unreadable, directlyUnreadable)) // the scan finds an unreadable key again
	}
	sortNewest(mine)
	return mine[0], nil
}

// noMatch is the error for "no readable record matched".
func noMatch(unreadable int) error {
	if unreadable > 0 {
		return fmt.Errorf("%w: %d candidate record(s) could not be decoded", ErrUnreadable, unreadable)
	}
	return ErrNotFound
}

// sortNewest orders records newest first, unknown times last. Equal times are
// ordered by key, so the outcome never depends on the order SCAN returned them in.
func sortNewest(recs []Record) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Updated.Equal(recs[j].Updated) {
			return recs[i].Ref < recs[j].Ref
		}
		return recs[i].Updated.After(recs[j].Updated)
	})
}

// emailLike is the test of load_user_data(): re.match(r".+@.+\..+", login.lower()).
var emailLike = regexp.MustCompile(`^.+@.+\..+`)

// loginMatcher is the filter of load_user_data(), with Email, ee_code and
// Name compared as described on Lookup.
func loginMatcher(login string) func(Record) bool {
	lower := strings.ToLower(login)
	if emailLike.MatchString(lower) {
		local, _, _ := strings.Cut(lower, "@")
		return func(r Record) bool { return r.Email == lower || strings.EqualFold(r.ECode, local) }
	}
	return func(r Record) bool { return r.Name == login || strings.EqualFold(r.ECode, login) }
}

// cleanToken trims s and rejects what cannot be a login or an ee_code: empty
// text, more than maxLogin runes, invalid UTF-8 and control characters.
func cleanToken(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 4*maxLogin || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxLogin || hasControl(s) {
		return "", false
	}
	return s, true
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// escapeGlob makes s match itself literally in a Redis glob pattern.
func escapeGlob(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '*', '?', '[', ']':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// load scans for pattern, reads the matching hashes and decodes them. unreadable
// counts the hashes that could not be decoded; keys that are not hashes or
// vanished meanwhile are ignored. More than MaxScan matching keys is
// ErrAmbiguous: the answer would depend on which of them were looked at, and
// a login that broad cannot be told apart from another employee's.
func (s *Store) load(ctx context.Context, pattern string) (recs []Record, unreadable int, err error) {
	keys, more, err := s.scan(ctx, pattern, s.maxScan)
	if err != nil {
		return nil, 0, err
	}
	if more {
		return nil, 0, fmt.Errorf("%w: more than %d keys match", ErrAmbiguous, s.maxScan)
	}
	sort.Strings(keys)
	for base := 0; base < len(keys); base += pipelineChunk {
		got, err := s.hgetall(ctx, keys[base:min(base+pipelineChunk, len(keys))], base)
		if err != nil {
			return nil, 0, err
		}
		for _, f := range got {
			if !f.exists() {
				continue
			}
			if h := decodeHash(f.key, f.hash, false); h.usable {
				recs = append(recs, h.rec)
			} else {
				unreadable++
			}
		}
	}
	return recs, unreadable, nil
}

// scan collects the keys that match pattern, at most limit of them (0 means all);
// more is true when there are further matches. It uses SCAN, never KEYS, and may
// see a key more than once.
func (s *Store) scan(ctx context.Context, pattern string, limit int) (keys []string, more bool, err error) {
	seen := map[string]bool{}
	var cursor uint64
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, s.unavailable(err)
		}
		page, next, err := s.rdb.Scan(ctx, cursor, pattern, scanCount).Result()
		if err != nil {
			return nil, false, s.unavailable(err)
		}
		for _, k := range page {
			if seen[k] {
				continue
			}
			seen[k] = true
			if limit > 0 && len(keys) >= limit {
				return keys, true, nil
			}
			keys = append(keys, k)
		}
		if next == 0 {
			return keys, false, nil
		}
		cursor = next
	}
}

// fetched is the answer to HGETALL for one key.
type fetched struct {
	n       int // 1-based position of the key in the list it was taken from
	key     string
	hash    map[string]string
	notHash bool
}

// exists is false for a key that vanished or holds something else than a hash.
func (f fetched) exists() bool { return !f.notHash && len(f.hash) > 0 }

// hgetall reads keys in one round trip. base is the position of keys[0] in the
// list the caller works through. A WRONGTYPE answer marks the key as not a
// hash; any other error is a failure of the directory.
func (s *Store) hgetall(ctx context.Context, keys []string, base int) ([]fetched, error) {
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.HGetAll(ctx, k)
	}
	_, _ = pipe.Exec(ctx) // returns the first error of the commands, which are examined one by one below
	out := make([]fetched, len(keys))
	for i, c := range cmds {
		out[i] = fetched{n: base + i + 1, key: keys[i]}
		hash, err := c.Result()
		switch {
		case err == nil:
			out[i].hash = hash
		case strings.HasPrefix(err.Error(), "WRONGTYPE"):
			out[i].notHash = true
		default:
			return nil, s.unavailable(err)
		}
	}
	return out, nil
}

// unavailable wraps err in ErrUnavailable. The password of the URL is masked
// in the text in case a server echoes the command that failed.
func (s *Store) unavailable(err error) error {
	return fmt.Errorf("%w: %w", ErrUnavailable, redacted{err, s.secret})
}

// redacted is err with secret masked in its text; the chain stays intact.
type redacted struct {
	err    error
	secret string
}

func (r redacted) Error() string { return mask(r.err.Error(), r.secret) }
func (r redacted) Unwrap() error { return r.err }

func mask(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "***")
}
