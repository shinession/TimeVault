package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testHash string

func hashOnce(t *testing.T) string {
	t.Helper()
	if testHash == "" {
		h, err := HashPassword("correct horse battery staple")
		if err != nil {
			t.Fatal(err)
		}
		testHash = h
	}
	return testHash
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newTestManager(t *testing.T, clock *fakeClock) *Manager {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	m, err := NewManager(Options{AdminUser: "admin", AdminPasswordHash: hashOnce(t), SessionKey: key, TTL: time.Hour, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPasswordHashRoundTrip(t *testing.T) {
	h := hashOnce(t)
	if !strings.HasPrefix(h, "pbkdf2-sha256$600000$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery staple"); err != nil || !ok {
		t.Fatalf("correct password rejected: %v %v", ok, err)
	}
	if ok, err := VerifyPassword(h, "wrong"); err != nil || ok {
		t.Fatalf("wrong password accepted: %v %v", ok, err)
	}
	other, _ := HashPassword("correct horse battery staple")
	if other == h {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{
		"", "plain", "pbkdf2-sha256$600000$AAAA", "md5$600000$AAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"pbkdf2-sha256$10$AAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"pbkdf2-sha256$999999999$AAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"pbkdf2-sha256$600000$!!$AAAAAAAAAAAAAAAAAAAAAA",
		"pbkdf2-sha256$600000$AAAAAAAAAAAA$!!",
		"pbkdf2-sha256$600000$AA$AAAAAAAAAAAAAAAAAAAAAA",
	} {
		if ok, err := VerifyPassword(bad, "x"); ok || err == nil {
			t.Errorf("VerifyPassword(%q) = %v, %v; want an error", bad, ok, err)
		}
	}
}

func TestNewManagerValidation(t *testing.T) {
	if _, err := NewManager(Options{AdminPasswordHash: "nope"}); err == nil {
		t.Fatal("a malformed hash must be rejected")
	}
	if _, err := NewManager(Options{AdminPasswordHash: hashOnce(t), SessionKey: []byte("short")}); err == nil {
		t.Fatal("a short session key must be rejected")
	}
	if _, err := NewManager(Options{Disabled: true}); err != nil {
		t.Fatalf("disabled auth needs no hash: %v", err)
	}
}

func TestAuthenticate(t *testing.T) {
	m := newTestManager(t, &fakeClock{now: time.Now()})
	if !m.Authenticate("admin", "correct horse battery staple") {
		t.Fatal("valid credentials rejected")
	}
	for _, c := range [][2]string{{"admin", "wrong"}, {"root", "correct horse battery staple"}, {"", ""}} {
		if m.Authenticate(c[0], c[1]) {
			t.Errorf("credentials %v accepted", c)
		}
	}
}

func cookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tv_session" {
			return c
		}
	}
	t.Fatal("no session cookie was set")
	return nil
}

func TestSessionCookieAttributesAndRoundTrip(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	m := newTestManager(t, clock)
	rec := httptest.NewRecorder()
	s, err := m.StartSession(rec, "admin")
	if err != nil {
		t.Fatal(err)
	}
	c := cookieOf(t, rec)
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure {
		t.Fatalf("cookie attributes = %+v", c)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	got, ok := m.Session(req)
	if !ok || got.User != "admin" || got.CSRF != s.CSRF || !got.Can(PermRestore) {
		t.Fatalf("Session = %+v, %v", got, ok)
	}

	clock.now = clock.now.Add(2 * time.Hour)
	if _, ok := m.Session(req); ok {
		t.Fatal("an expired session must be rejected")
	}
}

func TestSecureCookieFlag(t *testing.T) {
	key := make([]byte, 32)
	m, err := NewManager(Options{AdminPasswordHash: hashOnce(t), SessionKey: key, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if _, err := m.StartSession(rec, "admin"); err != nil {
		t.Fatal(err)
	}
	if !cookieOf(t, rec).Secure {
		t.Fatal("the cookie must be Secure when configured")
	}
}

func TestTamperedForeignOrMalformedCookiesAreRejected(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	m := newTestManager(t, clock)
	rec := httptest.NewRecorder()
	if _, err := m.StartSession(rec, "admin"); err != nil {
		t.Fatal(err)
	}
	good := cookieOf(t, rec)
	payload, sig, _ := strings.Cut(good.Value, ".")

	other, _ := NewManager(Options{AdminPasswordHash: hashOnce(t), SessionKey: []byte(strings.Repeat("k", 32)), Now: clock.Now})
	values := map[string]string{
		"payload changed":  payload + "x." + sig,
		"signature broken": payload + "." + sig[:len(sig)-2] + "AA",
		"no signature":     payload,
		"empty":            "",
		"garbage":          "not.a.cookie",
	}
	for name, v := range values {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "tv_session", Value: v})
		if _, ok := m.Session(req); ok {
			t.Errorf("%s: cookie accepted", name)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(good)
	if _, ok := other.Session(req); ok {
		t.Error("a cookie signed with another key must be rejected")
	}
	if _, ok := m.Session(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("a request without a cookie has no session")
	}
}

func TestClearSessionExpiresCookie(t *testing.T) {
	m := newTestManager(t, &fakeClock{now: time.Now()})
	rec := httptest.NewRecorder()
	m.ClearSession(rec)
	if c := cookieOf(t, rec); c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("cleared cookie = %+v", c)
	}
}

func TestCSRFChecks(t *testing.T) {
	m := newTestManager(t, &fakeClock{now: time.Now()})
	s, _ := m.StartSession(httptest.NewRecorder(), "admin")

	post := func(mut func(r *http.Request)) error {
		req := httptest.NewRequest(http.MethodPost, "http://tv.test/api", strings.NewReader(""))
		req.Host = "tv.test"
		mut(req)
		return m.CheckCSRF(req, s)
	}
	if err := post(func(r *http.Request) { r.Header.Set("X-CSRF-Token", s.CSRF) }); err != nil {
		t.Fatalf("valid header token rejected: %v", err)
	}
	if err := post(func(r *http.Request) {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"csrf_token": {s.CSRF}}.Encode())).Body
	}); err != nil {
		t.Fatalf("valid form token rejected: %v", err)
	}
	rejected := map[string]func(r *http.Request){
		"missing token": func(r *http.Request) {},
		"wrong token":   func(r *http.Request) { r.Header.Set("X-CSRF-Token", "nope") },
		"foreign origin": func(r *http.Request) {
			r.Header.Set("X-CSRF-Token", s.CSRF)
			r.Header.Set("Origin", "https://evil.example")
		},
		"null origin": func(r *http.Request) { r.Header.Set("X-CSRF-Token", s.CSRF); r.Header.Set("Origin", "null") },
		"cross-site browser": func(r *http.Request) {
			r.Header.Set("X-CSRF-Token", s.CSRF)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		},
	}
	for name, mut := range rejected {
		if err := post(mut); err == nil {
			t.Errorf("%s: request accepted", name)
		}
	}
	if err := post(func(r *http.Request) { r.Header.Set("X-CSRF-Token", s.CSRF); r.Header.Set("Origin", "http://tv.test") }); err != nil {
		t.Fatalf("same-origin request rejected: %v", err)
	}
	get := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := m.CheckCSRF(get, s); err != nil {
		t.Fatalf("GET needs no token: %v", err)
	}
}

func TestLoginThrottle(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	m := newTestManager(t, clock)
	for i := 0; i < maxLoginAttempts; i++ {
		if _, ok := m.Allow("10.0.0.1"); !ok {
			t.Fatalf("attempt %d blocked too early", i)
		}
		m.Failed("10.0.0.1")
	}
	wait, ok := m.Allow("10.0.0.1")
	if ok || wait <= 0 || wait > loginWindow {
		t.Fatalf("Allow after %d failures = %v, %v", maxLoginAttempts, wait, ok)
	}
	if _, ok := m.Allow("10.0.0.2"); !ok {
		t.Fatal("another client must not be affected")
	}
	clock.now = clock.now.Add(loginWindow + time.Second)
	if _, ok := m.Allow("10.0.0.1"); !ok {
		t.Fatal("the block must end after the window")
	}
	m.Failed("10.0.0.3")
	m.Succeeded("10.0.0.3")
	for i := 0; i < maxLoginAttempts-1; i++ {
		m.Failed("10.0.0.3")
	}
	if _, ok := m.Allow("10.0.0.3"); !ok {
		t.Fatal("a successful login must reset the counter")
	}
}

func TestDisabledAuthStillRequiresCSRF(t *testing.T) {
	m, err := NewManager(Options{Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://tv.test/x", nil)
	req.Host = "tv.test"
	s, ok := m.Session(req)
	if !ok || !s.AuthDisabled || s.CSRF == "" {
		t.Fatalf("synthetic session = %+v, %v", s, ok)
	}
	if err := m.CheckCSRF(req, s); err == nil {
		t.Fatal("a POST without a token must be rejected even when sign-in is off")
	}
	req.Header.Set("X-CSRF-Token", s.CSRF)
	if err := m.CheckCSRF(req, s); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if m.Authenticate("admin", "anything") {
		t.Fatal("Authenticate must never succeed when sign-in is disabled")
	}
}
