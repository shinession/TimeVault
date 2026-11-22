// Package auth implements TimeVault's sign-in: signed stateless session
// cookies, CSRF tokens, login throttles, PBKDF2 hashes for an optional local
// administrator, and the Directory interface through which company accounts
// are checked.
//
// Browsing needs no sign-in. What a signed-in session may do is expressed
// through Permission so the handlers never look at roles directly.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hashScheme         = "pbkdf2-sha256"
	hashIterations     = 600_000
	minIterations      = 100_000
	maxIterations      = 10_000_000
	saltBytes          = 16
	keyBytes           = 32
	csrfBytes          = 16
	maxLoginAttempts   = 5
	maxAccountAttempts = 10
	loginWindow        = 10 * time.Minute
	// sessionVersion invalidates cookies issued by an earlier layout.
	sessionVersion = 2
)

// ErrInvalidHash means a stored password hash is malformed.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// HashPassword returns "pbkdf2-sha256$iterations$salt$hash" for password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, keyBytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, hashIterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword checks password against an encoded hash in constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false, ErrInvalidHash
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < minIterations || iter > maxIterations {
		return false, ErrInvalidHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil || len(salt) < 8 {
		return false, ErrInvalidHash
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil || len(want) < 16 {
		return false, ErrInvalidHash
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Role is what an account may do beyond the things every signed-in account may do.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// Permission names something a request may be allowed to do.
type Permission string

const (
	// PermBrowse is open to everyone, signed in or not.
	PermBrowse Permission = "browse"
	// PermAccount covers what any signed-in account may do, such as signing out. What an account may do with
	// one monitored folder (back it up, restore from it, change its settings) is decided per folder by the
	// access package, not here.
	PermAccount Permission = "account"
	// PermAudit covers the audit log, the service status and the settings page.
	PermAudit Permission = "audit"
	// PermManage is the administrator: it adds monitored folders, sees the folders on the server's disk, and has
	// full control of every monitored folder.
	PermManage Permission = "manage"
	// PermSite is the local administrator alone: the account of TIMEVAULT_ADMIN_USER, or the synthetic session of a
	// service whose sign-in is switched off. It opens the Site settings, which decide who may sign in and what runs on the
	// server, so an account of the user directory does not have it, whatever its role.
	PermSite Permission = "site"
)

// Session is the identity behind a request. The zero value is an anonymous visitor.
type Session struct {
	// User is the sign-in identity: the ee_code of a company account, or the name of the local administrator.
	User string `json:"u"`
	// Name is shown in the header.
	Name string `json:"n"`
	Role Role   `json:"r"`
	// Ref is how the Directory finds the account again; empty for the local administrator.
	Ref       string    `json:"f"`
	CSRF      string    `json:"c"`
	IssuedAt  time.Time `json:"-"`
	ExpiresAt time.Time `json:"-"`
	// AuthDisabled marks the synthetic session used when sign-in is switched off.
	AuthDisabled bool `json:"-"`
}

// SignedIn reports whether the session belongs to an account.
func (s Session) SignedIn() bool { return s.User != "" }

// Can reports whether the session is allowed to perform p. An unknown permission is never allowed.
func (s Session) Can(p Permission) bool {
	switch p {
	case PermBrowse:
		return true
	case PermAccount, PermAudit:
		return s.SignedIn()
	case PermManage:
		return s.SignedIn() && s.Role == RoleAdmin
	case PermSite:
		// Only the local administrator has no Ref: the accounts of the user directory are found again by it.
		return s.SignedIn() && s.Role == RoleAdmin && s.Ref == ""
	}
	return false
}

type ctxKey struct{}

// WithSession stores s in ctx.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext returns the session stored by WithSession.
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(ctxKey{}).(Session)
	return s, ok
}

// Options configures a Manager.
type Options struct {
	// AdminUser and AdminPasswordHash describe an optional local administrator
	// who can sign in even when the Directory is down. Leave the hash empty for none.
	AdminUser         string
	AdminPasswordHash string
	// Directory checks company accounts. Optional when there is a local administrator.
	Directory Directory
	// RecheckEvery is how long an answer of the Directory about a signed-in
	// account is trusted before it is asked again. Default one minute.
	RecheckEvery time.Duration
	// Disabled turns sign-in off. Only safe on a loopback listener.
	Disabled     bool
	SessionKey   []byte
	TTL          time.Duration
	CookieName   string
	CookieSecure bool
	// CookiePath is the path the session cookie is limited to. Default "/"; behind a reverse proxy that serves
	// the site under a path prefix, give the prefix so the cookie is not sent to the other sites of the same host.
	CookiePath string
	Now        func() time.Time
}

// Manager issues and validates sessions.
type Manager struct {
	o        Options
	key      []byte
	limiter  *limiter
	accounts *limiter

	mu      sync.Mutex
	checked map[string]verdict
}

// NewManager validates the options and creates a Manager. A random session key
// is generated when none is given, which invalidates sessions on restart.
func NewManager(o Options) (*Manager, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.TTL <= 0 {
		o.TTL = 8 * time.Hour
	}
	if o.RecheckEvery <= 0 {
		o.RecheckEvery = time.Minute
	}
	if o.CookieName == "" {
		o.CookieName = "tv_session"
	}
	if o.CookiePath == "" {
		o.CookiePath = "/"
	}
	if o.AdminUser == "" {
		o.AdminUser = "admin"
	}
	if !o.Disabled {
		if o.AdminPasswordHash != "" {
			if _, err := VerifyPassword(o.AdminPasswordHash, ""); errors.Is(err, ErrInvalidHash) {
				return nil, errors.New("auth: the admin password hash is malformed")
			}
		} else if o.Directory == nil {
			return nil, errors.New("auth: there is no way to sign in; configure the user directory or a local administrator")
		}
	}
	key := o.SessionKey
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	} else if len(key) < 32 {
		return nil, errors.New("auth: the session key must have at least 32 bytes")
	}
	return &Manager{
		o: o, key: key,
		limiter:  newLimiter(maxLoginAttempts, loginWindow, o.Now),
		accounts: newLimiter(maxAccountAttempts, loginWindow, o.Now),
		checked:  map[string]verdict{},
	}, nil
}

// Disabled reports whether sign-in is switched off.
func (m *Manager) Disabled() bool { return m.o.Disabled }

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type wire struct {
	Session
	Issued  int64 `json:"i"`
	Expires int64 `json:"e"`
	Version int   `json:"v"`
}

// StartSession sets a fresh session cookie for the account and returns the session.
func (m *Manager) StartSession(w http.ResponseWriter, id Identity) (Session, error) {
	token := make([]byte, csrfBytes)
	if _, err := rand.Read(token); err != nil {
		return Session{}, err
	}
	now := m.o.Now()
	s := Session{
		User: id.ID, Name: id.Name, Role: id.Role, Ref: id.Ref,
		CSRF: base64.RawURLEncoding.EncodeToString(token), IssuedAt: now, ExpiresAt: now.Add(m.o.TTL),
	}
	raw, err := json.Marshal(wire{Session: s, Issued: now.Unix(), Expires: s.ExpiresAt.Unix(), Version: sessionVersion})
	if err != nil {
		return Session{}, err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: m.o.CookieName, Value: payload + "." + m.sign(payload), Path: m.o.CookiePath,
		HttpOnly: true, Secure: m.o.CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: s.ExpiresAt, MaxAge: int(m.o.TTL.Seconds()),
	})
	return s, nil
}

// ClearSession expires the session cookie.
func (m *Manager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: m.o.CookieName, Value: "", Path: m.o.CookiePath, HttpOnly: true, Secure: m.o.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(0, 0),
	})
}

// Session returns the session of a request, if it carries a valid one. It only
// checks the cookie; use Verify before acting on behalf of the account.
func (m *Manager) Session(r *http.Request) (Session, bool) {
	if m.o.Disabled {
		return Session{User: "local-admin", Name: "local-admin", Role: RoleAdmin, CSRF: m.disabledCSRF(), AuthDisabled: true}, true
	}
	c, err := r.Cookie(m.o.CookieName)
	if err != nil {
		return Session{}, false
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(m.sign(payload))) {
		return Session{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Session{}, false
	}
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Session{}, false
	}
	s := w.Session
	s.IssuedAt, s.ExpiresAt = time.Unix(w.Issued, 0), time.Unix(w.Expires, 0)
	if w.Version != sessionVersion || !m.o.Now().Before(s.ExpiresAt) || s.User == "" || s.CSRF == "" || (s.Role != RoleAdmin && s.Role != RoleUser) {
		return Session{}, false
	}
	if s.Name == "" {
		s.Name = s.User
	}
	return s, true
}

func (m *Manager) disabledCSRF() string {
	return base64.RawURLEncoding.EncodeToString([]byte(m.sign("csrf-without-sign-in")))
}

// ErrCSRF is returned by CheckCSRF.
var ErrCSRF = errors.New("auth: missing or invalid CSRF token")

// CheckCSRF validates a state-changing request: the token must match the
// session's, and a browser-supplied Origin or Sec-Fetch-Site must not point to
// another site.
func (m *Manager) CheckCSRF(r *http.Request, s Session) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return ErrCSRF
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return ErrCSRF
		}
	} else if origin == "null" {
		return ErrCSRF
	}
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		got = r.PostFormValue("csrf_token")
	}
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.CSRF)) != 1 {
		return ErrCSRF
	}
	return nil
}

// Throttle says that a sign-in attempt was refused because a limit is reached.
type Throttle struct {
	// Wait is how long until the limit lets the next attempt through.
	Wait time.Duration
	// ByAccount is true when the account name hit its limit, false for the client address.
	ByAccount bool
}

// Attempt is a sign-in attempt that has been counted against the limits of its
// client address and its account name. Exactly one of Succeeded, Unavailable or
// nothing (the attempt failed) settles it.
type Attempt struct {
	m    *Manager
	ip   string
	acct string
}

// Begin counts a sign-in attempt before it is checked, so guesses that run in
// parallel cannot all pass the limits before the first failure is known. When a
// limit is reached it returns the Throttle and counts nothing.
//
// The limits are per client address and per account name, because company
// passwords can be short and a guesser may use many addresses. Account names are
// compared the way the directory finds an account: without case and, for an
// e-mail address, by the part before the @.
func (m *Manager) Begin(client, login string) (*Attempt, *Throttle) {
	if wait, ok := m.limiter.reserve(client); !ok {
		return nil, &Throttle{Wait: wait}
	}
	key := accountKey(login)
	if wait, ok := m.accounts.reserve(key); !ok {
		m.limiter.refund(client)
		return nil, &Throttle{Wait: wait, ByAccount: true}
	}
	return &Attempt{m: m, ip: client, acct: key}, nil
}

// Succeeded settles an attempt that signed in: the address gets its slot back
// and the failures counted against the account are forgotten. The address's
// other failures stay, so signing in to one's own account does not buy more
// guesses at another.
func (a *Attempt) Succeeded() {
	a.m.limiter.refund(a.ip)
	a.m.accounts.reset(a.acct)
}

// Unavailable settles an attempt that could not be checked because the user
// directory did not answer: the account is not blamed. The address stays
// counted, so an anonymous visitor cannot make the directory work without limit.
func (a *Attempt) Unavailable() { a.m.accounts.refund(a.acct) }

type attempt struct {
	count int
	first time.Time
}

// maxTracked bounds the memory of a limiter. A real deployment has far fewer
// clients; beyond it new keys are refused rather than forgotten.
const maxTracked = 100_000

type limiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	now       func() time.Time
	seen      map[string]*attempt
	lastPurge time.Time
}

func newLimiter(max int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{max: max, window: window, now: now, seen: map[string]*attempt{}}
}

// reserve counts an attempt of key. When the limit is reached it returns how
// long to wait and counts nothing.
func (l *limiter) reserve(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.purge(now)
	a, ok := l.seen[key]
	if ok && now.Sub(a.first) >= l.window {
		delete(l.seen, key)
		ok = false
	}
	if !ok {
		if len(l.seen) >= maxTracked {
			return l.window, false
		}
		l.seen[key] = &attempt{count: 1, first: now}
		return 0, true
	}
	if a.count >= l.max {
		return l.window - now.Sub(a.first), false
	}
	a.count++
	return 0, true
}

// purge drops the entries whose window has ended, at most once per tenth of a
// window, so a flood of distinct keys cannot make every call walk the map.
func (l *limiter) purge(now time.Time) {
	if now.Sub(l.lastPurge) < l.window/10 {
		return
	}
	l.lastPurge = now
	for k, a := range l.seen {
		if now.Sub(a.first) >= l.window {
			delete(l.seen, k)
		}
	}
}

// refund gives back one counted attempt.
func (l *limiter) refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a, ok := l.seen[key]; ok {
		if a.count <= 1 {
			delete(l.seen, key)
		} else {
			a.count--
		}
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, key)
}
