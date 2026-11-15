// Package auth implements TimeVault's sign-in: PBKDF2 password hashes, signed
// stateless session cookies, CSRF tokens and a login throttle.
//
// The MVP knows one administrator whose password hash comes from the
// environment. Authorization is expressed through Permission so roles can be
// added later without touching the handlers.
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
	hashScheme       = "pbkdf2-sha256"
	hashIterations   = 600_000
	minIterations    = 100_000
	maxIterations    = 10_000_000
	saltBytes        = 16
	keyBytes         = 32
	csrfBytes        = 16
	maxLoginAttempts = 5
	loginWindow      = 10 * time.Minute
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

// Permission names something a session may be allowed to do.
type Permission string

const (
	PermBrowse  Permission = "browse"
	PermBackup  Permission = "backup"
	PermManage  Permission = "manage"
	PermRestore Permission = "restore"
	PermAudit   Permission = "audit"
)

// RoleAdmin may do everything.
const RoleAdmin = "admin"

// Session is the identity behind a request.
type Session struct {
	User      string    `json:"u"`
	Role      string    `json:"r"`
	CSRF      string    `json:"c"`
	IssuedAt  time.Time `json:"-"`
	ExpiresAt time.Time `json:"-"`
	// AuthDisabled marks the synthetic session used when sign-in is switched off.
	AuthDisabled bool `json:"-"`
}

// Can reports whether the session is allowed to perform p.
func (s Session) Can(p Permission) bool {
	return s.Role == RoleAdmin
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
	AdminUser         string
	AdminPasswordHash string
	// Disabled turns sign-in off. Only safe on a loopback listener.
	Disabled     bool
	SessionKey   []byte
	TTL          time.Duration
	CookieName   string
	CookieSecure bool
	Now          func() time.Time
}

// Manager issues and validates sessions.
type Manager struct {
	o       Options
	key     []byte
	limiter *limiter
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
	if o.CookieName == "" {
		o.CookieName = "tv_session"
	}
	if o.AdminUser == "" {
		o.AdminUser = "admin"
	}
	if !o.Disabled {
		if _, err := VerifyPassword(o.AdminPasswordHash, ""); errors.Is(err, ErrInvalidHash) {
			return nil, errors.New("auth: the admin password hash is missing or malformed")
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
	m := &Manager{o: o, key: key, limiter: newLimiter(maxLoginAttempts, loginWindow, o.Now)}
	return m, nil
}

// Disabled reports whether sign-in is switched off.
func (m *Manager) Disabled() bool { return m.o.Disabled }

// Authenticate checks the credentials. It always performs a password hash
// computation so the response time does not reveal whether the user exists.
func (m *Manager) Authenticate(user, password string) bool {
	if m.o.Disabled {
		return false
	}
	ok, _ := VerifyPassword(m.o.AdminPasswordHash, password)
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(m.o.AdminUser)) == 1
	return ok && userOK
}

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type wire struct {
	Session
	Issued  int64 `json:"i"`
	Expires int64 `json:"e"`
}

// StartSession sets a fresh session cookie for user and returns the session.
func (m *Manager) StartSession(w http.ResponseWriter, user string) (Session, error) {
	token := make([]byte, csrfBytes)
	if _, err := rand.Read(token); err != nil {
		return Session{}, err
	}
	now := m.o.Now()
	s := Session{User: user, Role: RoleAdmin, CSRF: base64.RawURLEncoding.EncodeToString(token), IssuedAt: now, ExpiresAt: now.Add(m.o.TTL)}
	raw, err := json.Marshal(wire{Session: s, Issued: now.Unix(), Expires: s.ExpiresAt.Unix()})
	if err != nil {
		return Session{}, err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: m.o.CookieName, Value: payload + "." + m.sign(payload), Path: "/",
		HttpOnly: true, Secure: m.o.CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: s.ExpiresAt, MaxAge: int(m.o.TTL.Seconds()),
	})
	return s, nil
}

// ClearSession expires the session cookie.
func (m *Manager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: m.o.CookieName, Value: "", Path: "/", HttpOnly: true, Secure: m.o.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(0, 0),
	})
}

// Session returns the session of a request, if it carries a valid one.
func (m *Manager) Session(r *http.Request) (Session, bool) {
	if m.o.Disabled {
		return Session{User: "local-admin", Role: RoleAdmin, CSRF: m.disabledCSRF(), AuthDisabled: true}, true
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
	if !m.o.Now().Before(s.ExpiresAt) || s.User == "" || s.CSRF == "" || s.Role != RoleAdmin {
		return Session{}, false
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

// Allow reports whether the client may attempt a login now; when not, it
// returns how long to wait.
func (m *Manager) Allow(client string) (time.Duration, bool) { return m.limiter.allow(client) }

// Failed records a failed login of a client.
func (m *Manager) Failed(client string) { m.limiter.fail(client) }

// Succeeded forgets the failures of a client.
func (m *Manager) Succeeded(client string) { m.limiter.reset(client) }

type attempt struct {
	count int
	first time.Time
}

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	seen   map[string]*attempt
}

func newLimiter(max int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{max: max, window: window, now: now, seen: map[string]*attempt{}}
}

func (l *limiter) allow(client string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.seen[client]
	if !ok {
		return 0, true
	}
	if elapsed := l.now().Sub(a.first); elapsed >= l.window {
		delete(l.seen, client)
		return 0, true
	} else if a.count >= l.max {
		return l.window - elapsed, false
	}
	return 0, true
}

func (l *limiter) fail(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.seen) > 10_000 {
		for k, a := range l.seen {
			if now.Sub(a.first) >= l.window {
				delete(l.seen, k)
			}
		}
	}
	a, ok := l.seen[client]
	if !ok || now.Sub(a.first) >= l.window {
		l.seen[client] = &attempt{count: 1, first: now}
		return
	}
	a.count++
}

func (l *limiter) reset(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, client)
}
