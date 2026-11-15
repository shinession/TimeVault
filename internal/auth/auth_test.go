package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

var adminID = Identity{ID: "admin", Name: "admin", Role: RoleAdmin}

// fakeDirectory knows one company account and counts how often it is asked.
type fakeDirectory struct {
	account   Identity
	password  string
	active    bool
	err       error // returned by every call when set
	refreshes int
}

func (d *fakeDirectory) Authenticate(_ context.Context, login, password string) (Identity, error) {
	if d.err != nil {
		return Identity{}, d.err
	}
	if !strings.EqualFold(login, d.account.ID) || password != d.password || !d.active {
		return Identity{}, ErrInvalidCredentials
	}
	return d.account, nil
}

func (d *fakeDirectory) Refresh(_ context.Context, id Identity) (Identity, error) {
	d.refreshes++
	if d.err != nil {
		return Identity{}, d.err
	}
	if id.ID != d.account.ID || !d.active {
		return Identity{}, ErrInvalidCredentials
	}
	return d.account, nil
}

func newDirectory() *fakeDirectory {
	return &fakeDirectory{
		account:  Identity{ID: "MES10001", Name: "Li Yi", Role: RoleUser, Ref: "app_user_mes10001_li yi_li.yi@example.com"},
		password: "654321", active: true,
	}
}

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func newTestManager(t *testing.T, clock *fakeClock) *Manager {
	t.Helper()
	m, err := NewManager(Options{AdminUser: "admin", AdminPasswordHash: hashOnce(t), SessionKey: testKey(), TTL: time.Hour, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newDirectoryManager(t *testing.T, clock *fakeClock, dir Directory, withLocalAdmin bool) *Manager {
	t.Helper()
	o := Options{Directory: dir, SessionKey: testKey(), TTL: 8 * time.Hour, RecheckEvery: time.Minute, Now: clock.Now}
	if withLocalAdmin {
		o.AdminUser, o.AdminPasswordHash = "admin", hashOnce(t)
	}
	m, err := NewManager(o)
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
	if _, err := NewManager(Options{}); err == nil {
		t.Fatal("a manager nobody can sign in to must be rejected")
	}
	if _, err := NewManager(Options{Directory: newDirectory()}); err != nil {
		t.Fatalf("a directory alone is enough: %v", err)
	}
}

func TestLogin(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, &fakeClock{now: time.Now()})
	id, err := m.Login(ctx, " admin ", "correct horse battery staple")
	if err != nil || id.ID != "admin" || id.Role != RoleAdmin || id.Ref != "" {
		t.Fatalf("valid local administrator = %+v, %v", id, err)
	}
	for _, c := range [][2]string{{"admin", "wrong"}, {"root", "correct horse battery staple"}, {"", ""}} {
		if _, err := m.Login(ctx, c[0], c[1]); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("credentials %v: err = %v, want ErrInvalidCredentials", c, err)
		}
	}

	dir := newDirectory()
	both := newDirectoryManager(t, &fakeClock{now: time.Now()}, dir, true)
	if id, err := both.Login(ctx, "mes10001", "654321"); err != nil || id != dir.account {
		t.Fatalf("company account = %+v, %v", id, err)
	}
	if _, err := both.Login(ctx, "MES10001", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong company password: %v", err)
	}
	if _, err := both.Login(ctx, "admin", "654321"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("the local administrator's name never falls through to the directory")
	}
	dir.err = ErrUnavailable
	if _, err := both.Login(ctx, "MES10001", "654321"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("directory down: %v", err)
	}
	if _, err := both.Login(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("the local administrator must still sign in while the directory is down: %v", err)
	}
	dir.err = nil
	dir.active = false
	if _, err := both.Login(ctx, "MES10001", "654321"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deactivated account: %v", err)
	}

	only := newDirectoryManager(t, &fakeClock{now: time.Now()}, newDirectory(), false)
	if _, err := only.Login(ctx, "admin", "correct horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("without a local administrator that name is just another account")
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
	s, err := m.StartSession(rec, adminID)
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
	if !ok || got.User != "admin" || got.Name != "admin" || got.Role != RoleAdmin || got.CSRF != s.CSRF || !got.Can(PermRestore) || !got.Can(PermManage) {
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
	if _, err := m.StartSession(rec, adminID); err != nil {
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
	if _, err := m.StartSession(rec, adminID); err != nil {
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
	s, _ := m.StartSession(httptest.NewRecorder(), adminID)

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
	if _, err := m.Login(context.Background(), "admin", "anything"); err == nil {
		t.Fatal("Login must never succeed when sign-in is disabled")
	}
}

func TestPermissions(t *testing.T) {
	anon := Session{}
	user := Session{User: "MES10001", Role: RoleUser}
	admin := Session{User: "admin", Role: RoleAdmin}
	cases := []struct {
		perm              Permission
		anon, user, admin bool
	}{
		{PermBrowse, true, true, true},
		{PermAccount, false, true, true},
		{PermBackup, false, true, true},
		{PermRestore, false, true, true},
		{PermAudit, false, true, true},
		{PermManage, false, false, true},
		{"unknown", false, false, false},
	}
	for _, c := range cases {
		if anon.Can(c.perm) != c.anon || user.Can(c.perm) != c.user || admin.Can(c.perm) != c.admin {
			t.Errorf("%s: anonymous=%v user=%v admin=%v, want %v %v %v", c.perm, anon.Can(c.perm), user.Can(c.perm), admin.Can(c.perm), c.anon, c.user, c.admin)
		}
	}
	if anon.SignedIn() || !user.SignedIn() {
		t.Error("SignedIn must follow the user")
	}
	// A role the program does not know is never an administrator.
	if (Session{User: "x", Role: "superuser"}).Can(PermManage) {
		t.Error("an unknown role must not manage")
	}
}

// A cookie of the layout of v0.1.0 (no version, no role names) must not be honoured.
func TestOldCookieLayoutIsRejected(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	m := newTestManager(t, clock)
	raw, _ := json.Marshal(map[string]any{"u": "admin", "r": "admin", "c": "token", "i": clock.now.Unix(), "e": clock.now.Add(time.Hour).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "tv_session", Value: payload + "." + m.sign(payload)})
	if _, ok := m.Session(req); ok {
		t.Fatal("a correctly signed cookie of the old layout must be rejected")
	}
}

func TestSessionCarriesTheCompanyAccount(t *testing.T) {
	dir := newDirectory()
	m := newDirectoryManager(t, &fakeClock{now: time.Now()}, dir, false)
	rec := httptest.NewRecorder()
	if _, err := m.StartSession(rec, dir.account); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookieOf(t, rec))
	got, ok := m.Session(req)
	if !ok || got.User != "MES10001" || got.Name != "Li Yi" || got.Role != RoleUser || got.Ref != dir.account.Ref {
		t.Fatalf("Session = %+v, %v", got, ok)
	}
	if !got.Can(PermBackup) || got.Can(PermManage) {
		t.Fatal("a company user may operate but not manage")
	}
}

func TestVerifyAppliesDeactivationAndRoleChangesWithinTheRecheckInterval(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	dir := newDirectory()
	m := newDirectoryManager(t, clock, dir, false)
	session := Session{User: dir.account.ID, Name: dir.account.Name, Role: RoleUser, Ref: dir.account.Ref, CSRF: "c"}

	if got, err := m.Verify(ctx, session); err != nil || got.Role != RoleUser {
		t.Fatalf("first check = %+v, %v", got, err)
	}
	for i := 0; i < 5; i++ {
		if _, err := m.Verify(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	if dir.refreshes != 1 {
		t.Fatalf("the directory was asked %d times within the interval, want 1", dir.refreshes)
	}

	dir.account.Role, dir.account.Name = RoleAdmin, "Li Yi (admin)"
	if got, _ := m.Verify(ctx, session); got.Role != RoleUser {
		t.Fatal("a change must not apply before the interval is over")
	}
	clock.now = clock.now.Add(61 * time.Second)
	if got, err := m.Verify(ctx, session); err != nil || got.Role != RoleAdmin || got.Name != "Li Yi (admin)" || !got.Can(PermManage) {
		t.Fatalf("after the interval = %+v, %v", got, err)
	}

	dir.active = false
	clock.now = clock.now.Add(61 * time.Second)
	if _, err := m.Verify(ctx, session); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a deactivated account must lose its session: %v", err)
	}
	dir.active = true
	if _, err := m.Verify(ctx, session); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("the verdict is remembered for the interval")
	}
	clock.now = clock.now.Add(61 * time.Second)
	if _, err := m.Verify(ctx, session); err != nil {
		t.Fatalf("a reactivated account signs in again: %v", err)
	}
}

func TestVerifyKeepsSessionsWorkingWhileTheDirectoryIsDown(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Now()}
	dir := newDirectory()
	m := newDirectoryManager(t, clock, dir, false)
	session := Session{User: dir.account.ID, Name: dir.account.Name, Role: RoleUser, Ref: dir.account.Ref, CSRF: "c"}

	dir.err = ErrUnavailable
	for i := 0; i < 3; i++ {
		if got, err := m.Verify(ctx, session); err != nil || got != session {
			t.Fatalf("a session must survive a directory outage: %+v, %v", got, err)
		}
	}
	if dir.refreshes != 1 {
		t.Fatalf("a dead directory was asked %d times within the back-off, want 1", dir.refreshes)
	}
	clock.now = clock.now.Add(unavailableBackoff + time.Second)
	dir.err, dir.active = nil, false
	if _, err := m.Verify(ctx, session); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("once the directory is back it decides: %v", err)
	}
}

func TestVerifyLocalAdministratorAndOrphanedSessions(t *testing.T) {
	ctx := context.Background()
	m := newDirectoryManager(t, &fakeClock{now: time.Now()}, newDirectory(), true)
	if _, err := m.Verify(ctx, Session{User: "admin", Name: "admin", Role: RoleAdmin, CSRF: "c"}); err != nil {
		t.Fatalf("local administrator: %v", err)
	}
	if _, err := m.Verify(ctx, Session{User: "someone", Role: RoleAdmin, CSRF: "c"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("a session without a directory reference must be the configured local administrator")
	}
	if got, err := m.Verify(ctx, Session{}); err != nil || got.SignedIn() {
		t.Fatal("an anonymous visitor passes through untouched")
	}

	// The directory was switched off in the configuration: its sessions end.
	noDir := newTestManager(t, &fakeClock{now: time.Now()})
	if _, err := noDir.Verify(ctx, Session{User: "MES10001", Role: RoleUser, Ref: "app_user_x", CSRF: "c"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("a company session must end when the directory is no longer configured")
	}
	// And the local administrator was removed.
	onlyDir := newDirectoryManager(t, &fakeClock{now: time.Now()}, newDirectory(), false)
	if _, err := onlyDir.Verify(ctx, Session{User: "admin", Role: RoleAdmin, CSRF: "c"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("a local administrator session must end when the administrator is no longer configured")
	}
}

// guesser makes the attempts of one person who is wrong every time.
func guesser(m *Manager, ip string, names ...string) (blocked *Throttle) {
	for _, n := range names {
		if _, th := m.Begin(ip, n); th != nil {
			return th
		}
	}
	return nil
}

func manyNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "user" + strconv.Itoa(i)
	}
	return out
}

func TestAccountThrottleHoldsBackGuessersFromManyAddresses(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	m := newTestManager(t, clock)
	for i := 0; i < maxAccountAttempts; i++ {
		if _, th := m.Begin("10.1.0."+strconv.Itoa(i), " MES10001 "); th != nil {
			t.Fatalf("attempt %d blocked too early: %+v", i, th)
		}
	}
	// A guesser with a new address for every attempt is stopped by the account, whatever way the account is spelled.
	for _, login := range []string{"Mes10001", "MES10001@example.com", "mes10001@another.example", "mes10001@x.y.z"} {
		th := func() *Throttle { _, th := m.Begin("10.2.0.1", login); return th }()
		if th == nil || !th.ByAccount || th.Wait <= 0 || th.Wait > loginWindow {
			t.Fatalf("%q after %d failures: %+v; the account must be matched without case and without the domain of an e-mail address", login, maxAccountAttempts, th)
		}
	}
	// Being refused by the account costs the address nothing.
	if th := guesser(m, "10.2.0.1", manyNames(maxLoginAttempts)...); th != nil {
		t.Fatalf("refused attempts were charged to the address: %+v", th)
	}
	if _, th := m.Begin("10.3.0.1", "MES10002"); th != nil {
		t.Fatal("another account must not be affected")
	}
	clock.now = clock.now.Add(loginWindow + time.Second)
	if _, th := m.Begin("10.3.0.2", "MES10001"); th != nil {
		t.Fatal("the block must end after the window")
	}
	long := strings.Repeat("x", 1000)
	if _, th := m.Begin("10.3.0.3", long); th != nil {
		t.Fatal("one failure must not block")
	}
}

func TestAccountFailuresAreForgottenWhenTheOwnerSignsIn(t *testing.T) {
	m := newTestManager(t, &fakeClock{now: time.Now()})
	for i := 0; i < maxAccountAttempts-1; i++ {
		m.Begin("10.4.0."+strconv.Itoa(i), "mes10001")
	}
	attempt, th := m.Begin("10.4.1.1", "mes10001")
	if th != nil {
		t.Fatalf("the last attempt that is allowed was blocked: %+v", th)
	}
	attempt.Succeeded()
	for i := 0; i < maxAccountAttempts; i++ {
		if _, th := m.Begin("10.4.2."+strconv.Itoa(i), "mes10001"); th != nil {
			t.Fatalf("a successful sign-in must reset the account's counter; attempt %d: %+v", i, th)
		}
	}
}

func TestLoginThrottle(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	m := newTestManager(t, clock)
	if th := guesser(m, "10.0.0.1", manyNames(maxLoginAttempts)...); th != nil {
		t.Fatalf("attempts blocked too early: %+v", th)
	}
	_, th := m.Begin("10.0.0.1", "someone")
	if th == nil || th.ByAccount || th.Wait <= 0 || th.Wait > loginWindow {
		t.Fatalf("attempt %d from one address = %+v", maxLoginAttempts+1, th)
	}
	if _, th := m.Begin("10.0.0.2", "someone"); th != nil {
		t.Fatal("another client must not be affected")
	}
	clock.now = clock.now.Add(loginWindow + time.Second)
	if _, th := m.Begin("10.0.0.1", "someone"); th != nil {
		t.Fatal("the block must end after the window")
	}
}

func TestSigningInToOneAccountDoesNotBuyMoreGuessesAtAnother(t *testing.T) {
	m := newTestManager(t, &fakeClock{now: time.Now()})
	if th := guesser(m, "10.0.0.3", manyNames(maxLoginAttempts-1)...); th != nil {
		t.Fatalf("blocked too early: %+v", th)
	}
	mine, th := m.Begin("10.0.0.3", "mine")
	if th != nil {
		t.Fatalf("blocked too early: %+v", th)
	}
	mine.Succeeded() // gives back its own slot only
	if _, th := m.Begin("10.0.0.3", "next"); th != nil {
		t.Fatalf("the slot of the successful sign-in must be given back: %+v", th)
	}
	if _, th := m.Begin("10.0.0.3", "again"); th == nil {
		t.Fatal("the failures of the address must survive a successful sign-in, or it could be interleaved with endless guesses")
	}
}

func TestGuessesThatRunInParallelAreCountedWhileTheyRun(t *testing.T) {
	m := newTestManager(t, &fakeClock{now: time.Now()})
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, th := m.Begin("10.0.0.4", "guess"+strconv.Itoa(i)); th == nil {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != maxLoginAttempts {
		t.Fatalf("%d parallel attempts got through, want exactly %d", got, maxLoginAttempts)
	}
}

func TestADirectoryThatDoesNotAnswerBlamesNoAccountButCostsTheAddress(t *testing.T) {
	m := newTestManager(t, &fakeClock{now: time.Now()})
	for i := 0; i < maxAccountAttempts+3; i++ {
		attempt, th := m.Begin("10.5.0."+strconv.Itoa(i), "mes10001")
		if th != nil {
			t.Fatalf("attempt %d: an account must not be blocked by attempts nobody could check: %+v", i, th)
		}
		attempt.Unavailable()
	}
	for i := 0; i < maxLoginAttempts; i++ {
		attempt, th := m.Begin("10.5.1.1", "name"+strconv.Itoa(i))
		if th != nil {
			t.Fatalf("attempt %d blocked too early: %+v", i, th)
		}
		attempt.Unavailable()
	}
	if _, th := m.Begin("10.5.1.1", "another"); th == nil || th.ByAccount {
		t.Fatalf("an address must not be able to make the directory work without limit: %+v", th)
	}
}

func TestTheThrottleOfAnAddressIsBounded(t *testing.T) {
	l := newLimiter(1, time.Hour, time.Now)
	for i := 0; i < maxTracked; i++ {
		if _, ok := l.reserve(strconv.Itoa(i)); !ok {
			t.Fatalf("key %d refused below the bound", i)
		}
	}
	if _, ok := l.reserve("one more"); ok {
		t.Fatal("beyond the bound a new key must be refused, not remembered")
	}
	if _, ok := l.reserve("7"); ok {
		t.Fatal("a known key keeps its count")
	}
}

func TestLocalAdministratorNameIsReservedInAnyCase(t *testing.T) {
	ctx := context.Background()
	dir := newDirectory()
	dir.account.ID = "ADMIN" // a company account whose employee ID reads like the local administrator's name
	m := newDirectoryManager(t, &fakeClock{now: time.Now()}, dir, true)
	if _, err := m.Login(ctx, "ADMIN", "654321"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("the name of the local administrator must never be looked up in the directory: %v", err)
	}
	if id, err := m.Login(ctx, "Admin", "correct horse battery staple"); err != nil || id.ID != "admin" || id.Ref != "" {
		t.Fatalf("the local administrator in another case: %+v, %v", id, err)
	}
}

func TestVerifyIgnoresAbandonedRequests(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	dir := newDirectory()
	m := newDirectoryManager(t, clock, dir, false)
	session := Session{User: dir.account.ID, Name: dir.account.Name, Role: RoleUser, Ref: dir.account.Ref, CSRF: "c"}

	// A visitor who gives up in the middle of the check says nothing about their account.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	dir.err = context.Canceled
	if _, err := m.Verify(gone, session); !errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("an abandoned request = %v", err)
	}
	// and must not leave a verdict that trusts the session as if the directory were down.
	dir.err, dir.active = nil, false
	if _, err := m.Verify(context.Background(), session); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a deactivated account after an abandoned request: %v", err)
	}
}

func TestVerdictsAreKeptPerAccountRecord(t *testing.T) {
	dir := newDirectory()
	m := newDirectoryManager(t, &fakeClock{now: time.Now()}, dir, false)
	one := Session{User: dir.account.ID, Name: "x", Role: RoleUser, Ref: "app_user_old", CSRF: "c"}
	two := Session{User: dir.account.ID, Name: "x", Role: RoleUser, Ref: "app_user_new", CSRF: "c"}
	for _, s := range []Session{one, two, one, two} {
		if _, err := m.Verify(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if dir.refreshes != 2 {
		t.Fatalf("the directory was asked %d times, want once per record", dir.refreshes)
	}
}
