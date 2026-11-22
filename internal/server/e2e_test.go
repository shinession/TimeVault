package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/config"
	"timevault/internal/jobs"
	"timevault/internal/search"
	"timevault/internal/server"
	"timevault/internal/service"
	"timevault/internal/testenv"
	"timevault/web"
)

const password = "correct horse battery staple"

var (
	hashOnce sync.Once
	hashed   string
)

func passwordHash(t *testing.T) string {
	t.Helper()
	hashOnce.Do(func() {
		h, err := auth.HashPassword(password)
		if err != nil {
			t.Fatal(err)
		}
		hashed = h
	})
	return hashed
}

type harness struct {
	t      *testing.T
	env    *testenv.Env
	srv    *httptest.Server
	client *http.Client
	search *swappable
	csrf   string
	// addr is the address this browser appears to come from; empty means the real loopback address.
	addr string
	// prefix is put in front of the path of every request: the base path the browser asks for.
	prefix string
	// strip makes the requests go through a reverse proxy that takes the prefix off again (see forwarded).
	strip bool
	// dir stands in for the company user accounts, clock for the time the sign-in manager sees.
	dir   *fakeDirectory
	clock *testClock
	// logs is what the HTTP server logged.
	logs *logBuffer
}

var visitorSeq atomic.Int32

// swappable lets a test replace the search provider after the server is built.
type swappable struct {
	mu sync.RWMutex
	p  search.Provider
}

func (s *swappable) set(p search.Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.p = p
}

func (s *swappable) current() search.Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.p
}

func (s *swappable) Name() string { return s.current().Name() }
func (s *swappable) Search(ctx context.Context, q search.Query) (search.Page, error) {
	return s.current().Search(ctx, q)
}
func (s *swappable) Health(ctx context.Context) search.Health { return s.current().Health(ctx) }

// Subfolders passes the question on to a provider that can answer it.
func (s *swappable) Subfolders(ctx context.Context, dir string, limit int) ([]search.FolderInfo, bool, error) {
	if lister, ok := s.current().(search.FolderLister); ok {
		return lister.Subfolders(ctx, dir, limit)
	}
	return nil, false, search.ErrDisabled
}

func newHarness(t *testing.T) *harness { return newHarnessWith(t, nil) }

// newHarnessWith builds the server of the tests; tune, when given, may change the service configuration and the
// deployment before they are used.
func newHarnessWith(t *testing.T, tune func(*service.Config, *config.Deployment)) *harness {
	return newHarnessOpts(t, tune, nil)
}

// newHarnessOpts is newHarnessWith that can also change the options of the HTTP server.
func newHarnessOpts(t *testing.T, tune func(*service.Config, *config.Deployment), serverTune func(*server.Options)) *harness {
	t.Helper()
	env := testenv.New(t)
	log := slog.New(slog.DiscardHandler)
	logs := &logBuffer{}
	runner := jobs.NewRunner(env.Pool, log, 2)
	provider := &swappable{p: search.NewDisabled()}
	dir, clock := newFakeDirectory(), &testClock{now: time.Now()}
	cfg := service.Config{
		Pool: env.Pool, Catalog: env.Cat, Repo: env.Repo, Engine: env.Eng, Restore: env.Restore,
		Jobs: runner, Audit: audit.New(env.Pool, log), Search: provider, Logger: log, Directory: dir,
	}
	deployment := config.Deployment{
		ListenAddr: "127.0.0.1:8660", Database: "db.test:5432/TimeVault", UserDirectory: dir.Addr() + "/0",
		RepositoryPath: env.Repo.Root(), RestoreRoot: env.RestoreRoot, EnvFile: "test.env", LocalAdmin: "admin", SessionTTL: time.Hour,
	}
	if tune != nil {
		tune(&cfg, &deployment)
	}
	svc := service.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	runner.Start(ctx)
	t.Cleanup(func() {
		cancel()
		stopCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		runner.Shutdown(stopCtx)
	})
	opts := server.Options{Version: "test", Location: time.UTC, Deployment: deployment, Now: clock.Now}
	if serverTune != nil {
		serverTune(&opts)
	}
	am, err := auth.NewManager(auth.Options{
		AdminUser: "admin", AdminPasswordHash: passwordHash(t), Directory: dir,
		SessionKey: []byte(strings.Repeat("k", 32)), TTL: time.Hour, Now: clock.Now, CookiePath: opts.BasePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.New(svc, am, web.FS, opts, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(forwarded(s.Handler()))
	t.Cleanup(ts.Close)
	h := &harness{t: t, env: env, srv: ts, search: provider, dir: dir, clock: clock, logs: logs}
	h.client = h.newBrowser()
	return h
}

func (h *harness) newBrowser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// visitor is another browser talking to the same server: its own cookies, its own CSRF token and its own address.
func (h *harness) visitor() *harness {
	v := *h
	v.client, v.csrf = h.newBrowser(), ""
	n := int(visitorSeq.Add(1))
	v.addr = "10.8." + strconv.Itoa(n/250) + "." + strconv.Itoa(n%250+1)
	return &v
}

func (h *harness) do(method, path string, body io.Reader, hdr map[string]string) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+h.prefix+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if h.addr != "" {
		req.Header.Set("X-Test-Client", h.addr)
	}
	if h.strip {
		req.Header.Set("X-Test-Strip", "1")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func (h *harness) get(path string, hdr ...map[string]string) (*http.Response, string) {
	h.t.Helper()
	var m map[string]string
	if len(hdr) > 0 {
		m = hdr[0]
	}
	return h.do(http.MethodGet, path, nil, m)
}

func (h *harness) postForm(path string, v url.Values, hdr ...map[string]string) (*http.Response, string) {
	h.t.Helper()
	if v == nil {
		v = url.Values{}
	}
	if h.csrf != "" && v.Get("csrf_token") == "" {
		v.Set("csrf_token", h.csrf)
	}
	m := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if len(hdr) > 0 {
		for k, val := range hdr[0] {
			m[k] = val
		}
	}
	return h.do(http.MethodPost, path, strings.NewReader(v.Encode()), m)
}

// signIn posts the sign-in form and, when it succeeds, remembers the CSRF token of the new session.
func (h *harness) signIn(login, pw string) int {
	h.t.Helper()
	h.csrf = ""
	res, _ := h.postForm("/login", url.Values{"username": {login}, "password": {pw}, "next": {"/"}})
	if res.StatusCode != http.StatusSeeOther {
		return res.StatusCode
	}
	_, body := h.get("/api/v1/session")
	var s struct {
		Csrf string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(body), &s); err != nil || s.Csrf == "" {
		h.t.Fatalf("session: %q %v", body, err)
	}
	h.csrf = s.Csrf
	return res.StatusCode
}

// login signs in as the local administrator, who may do everything.
func (h *harness) login() {
	h.t.Helper()
	if status := h.signIn("admin", password); status != http.StatusSeeOther {
		h.t.Fatalf("login status = %d", status)
	}
}

// loginAs signs in with a company account of the fake directory.
func (h *harness) loginAs(login string) {
	h.t.Helper()
	if status := h.signIn(login, h.dir.password(login)); status != http.StatusSeeOther {
		h.t.Fatalf("sign-in of %s = %d", login, status)
	}
}

func (h *harness) api(method, path string, payload any) (int, map[string]any) {
	h.t.Helper()
	var body io.Reader
	hdr := map[string]string{"X-CSRF-Token": h.csrf}
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = strings.NewReader(string(b))
		hdr["Content-Type"] = "application/json"
	}
	res, text := h.do(method, path, body, hdr)
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return res.StatusCode, out
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func (h *harness) waitJob(id int64) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, out := h.api("GET", "/api/v1/jobs/"+itoa(id), nil)
		if status != 200 {
			h.t.Fatalf("job status request = %d %v", status, out)
		}
		job := out["job"].(map[string]any)
		if active, _ := job["active"].(bool); !active {
			return job
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("job %d did not finish", id)
	return nil
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func hidden(html, name string) string {
	m := regexp.MustCompile(`name="` + name + `" value="([^"]*)"`).FindStringSubmatch(html)
	if m == nil {
		return ""
	}
	return m[1]
}

func seed(h *harness) {
	h.env.Write("a.txt", "alpha")
	h.env.Write("docs/b.txt", "bravo")
	h.env.Write("docs/deep/c.txt", "charlie")
}

var (
	inlineScript = regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>`)
	scriptSrc    = regexp.MustCompile(`(?i)<script[^>]*\ssrc=`)
	styleAttr    = regexp.MustCompile(`(?i)\sstyle\s*=`)
	handlerAttr  = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
)

// A strict CSP forbids inline script and style; no page may depend on either.
func assertCSPClean(t *testing.T, what, html string) {
	t.Helper()
	for _, m := range inlineScript.FindAllString(html, -1) {
		if !scriptSrc.MatchString(m) {
			t.Errorf("%s contains an inline script: %s", what, m)
		}
	}
	if styleAttr.MatchString(html) {
		t.Errorf("%s contains a style attribute", what)
	}
	if handlerAttr.MatchString(html) {
		t.Errorf("%s contains an inline event handler", what)
	}
	if strings.Contains(html, "<style") {
		t.Errorf("%s contains a <style> element", what)
	}
}

func TestSignInAndGuards(t *testing.T) {
	h := newHarness(t)

	// Browsing needs no account; what is not browsing leads to the sign-in form.
	if res, _ := h.get("/backups"); res.StatusCode != http.StatusOK {
		t.Fatalf("anonymous /backups = %d, want 200", res.StatusCode)
	}
	res, _ := h.get("/audit")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login?next=") {
		t.Fatalf("anonymous /audit = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, body := h.get("/api/v1/audit"); res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "unauthorized") {
		t.Fatalf("anonymous protected API = %d %s", res.StatusCode, body)
	}
	if res, _ := h.get("/healthz"); res.StatusCode != 200 {
		t.Fatalf("healthz = %d", res.StatusCode)
	}
	if res, _ := h.get("/static/css/app.css"); res.StatusCode != 200 {
		t.Fatalf("static asset = %d", res.StatusCode)
	}
	if res, body := h.get("/login"); res.StatusCode != 200 || !strings.Contains(body, "TimeVault") {
		t.Fatalf("login page = %d", res.StatusCode)
	}

	for i := 0; i < 5; i++ {
		res, body := h.postForm("/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
		if res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Invalid account or password") {
			t.Fatalf("failed login %d = %d", i, res.StatusCode)
		}
	}
	res, _ = h.postForm("/login", url.Values{"username": {"admin"}, "password": {password}})
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("login while throttled = %d, want 429 even with the right password", res.StatusCode)
	}
}

func TestLoginRedirectsOnlyToLocalPathsAndRejectsCrossSitePosts(t *testing.T) {
	h := newHarness(t)
	for next, want := range map[string]string{"//evil.example": "/", "https://evil.example/x": "/", `/./\evil.example`: "/", "/backups": "/backups"} {
		res, _ := h.postForm("/login", url.Values{"username": {"admin"}, "password": {password}, "next": {next}})
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != want {
			t.Fatalf("next=%q -> %d %q, want %q", next, res.StatusCode, res.Header.Get("Location"), want)
		}
	}
	h2 := newHarness(t)
	res, _ := h2.postForm("/login", url.Values{"username": {"admin"}, "password": {password}}, map[string]string{"Origin": "https://evil.example"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site login = %d, want 403", res.StatusCode)
	}
}

func TestCSRFAndSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	h.login()

	res, body := h.get("/explorer")
	if res.StatusCode != 200 {
		t.Fatalf("explorer = %d", res.StatusCode)
	}
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP = %q", csp)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", res.Header)
	}
	assertCSPClean(t, "/explorer", body)

	saved := h.csrf
	h.csrf = ""
	if res, _ := h.postForm("/logout", nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without a CSRF token = %d, want 403", res.StatusCode)
	}
	if res, _ := h.postForm("/backups/run", url.Values{"source_id": {"1"}}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("run backup without a CSRF token = %d, want 403", res.StatusCode)
	}
	status, _ := h.api("POST", "/api/v1/backups", map[string]any{"source_id": 1})
	if status != http.StatusForbidden {
		t.Fatalf("API POST without the CSRF header = %d, want 403", status)
	}
	h.csrf = saved
	if res, _ := h.postForm("/logout", nil, map[string]string{"Origin": "https://evil.example"}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("logout from a foreign origin = %d, want 403", res.StatusCode)
	}
	if res, _ := h.postForm("/logout", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout = %d", res.StatusCode)
	}
	if res, _ := h.get("/audit"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("after logout /audit = %d, want redirect to login", res.StatusCode)
	}

	// Denied attempts are in the audit trail.
	h.login()
	_, out := h.api("GET", "/api/v1/audit?action=access.denied", nil)
	if events, _ := out["events"].([]any); len(events) < 3 {
		t.Fatalf("expected the denied requests in the audit log, got %v", out)
	}
}

func TestBackupHistoryAndRestoreThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	srcID := h.env.Src.ID

	status, out := h.api("POST", "/api/v1/backups", map[string]any{"source_id": srcID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	if job := h.waitJob(num(out["job"].(map[string]any)["id"])); job["state"] != "succeeded" {
		t.Fatalf("backup job = %v", job)
	}

	h.env.Write("a.txt", "alpha v2")
	h.env.Remove("docs/b.txt")
	_, out = h.api("POST", "/api/v1/backups", map[string]any{"source_id": srcID})
	if job := h.waitJob(num(out["job"].(map[string]any)["id"])); job["state"] != "succeeded" {
		t.Fatalf("second backup job = %v", job)
	}

	_, out = h.api("GET", "/api/v1/recovery-points?source_id="+itoa(srcID), nil)
	points := out["recovery_points"].([]any)
	if len(points) != 2 {
		t.Fatalf("recovery points = %v", points)
	}
	newest, oldest := points[0].(map[string]any), points[1].(map[string]any)
	if newest["seq"].(float64) != 2 || oldest["restorable"] != true || oldest["consistency"] != "live_scan" {
		t.Fatalf("points = %v / %v", newest, oldest)
	}

	_, out = h.api("GET", "/api/v1/history/tree?source_id="+itoa(srcID)+"&point_id="+itoa(num(oldest["id"]))+"&path=docs", nil)
	entries := out["entries"].([]any)
	status2 := map[string]string{}
	for _, e := range entries {
		m := e.(map[string]any)
		status2[m["name"].(string)], _ = m["status"].(string)
	}
	if status2["b.txt"] != "deleted" || status2["deep"] != "same" {
		t.Fatalf("history of docs at point 1 = %v", status2)
	}

	_, out = h.api("GET", "/api/v1/history/versions?source_id="+itoa(srcID)+"&path=a.txt", nil)
	if versions := out["versions"].([]any); len(versions) != 2 || versions[0].(map[string]any)["current"] != true {
		t.Fatalf("versions = %v", out)
	}

	// A file that disappears after the newest backup is offered for recovery.
	h.env.Remove("docs/deep/c.txt")
	_, out = h.api("GET", "/api/v1/files?source_id="+itoa(srcID)+"&path=docs/deep", nil)
	if deleted := out["deleted"].([]any); len(deleted) != 1 || deleted[0].(map[string]any)["name"] != "c.txt" {
		t.Fatalf("deleted list = %v", out["deleted"])
	}

	// Restore the deleted file from the first point.
	req := map[string]any{"source_id": srcID, "point_id": num(oldest["id"]), "paths": []string{"docs/b.txt"}, "dest_name": "recovered", "policy": "skip"}
	status, preview := h.api("POST", "/api/v1/restores/preview", req)
	if status != 200 || preview["totals"].(map[string]any)["create"].(float64) != 1 {
		t.Fatalf("preview = %d %v", status, preview)
	}
	token := preview["confirmation_token"].(string)

	bad := map[string]any{"source_id": srcID, "point_id": num(oldest["id"]), "paths": []string{"docs/b.txt"}, "dest_name": "recovered", "policy": "skip",
		"confirmation_token": token + "x", "idempotency_key": "k1"}
	if status, _ := h.api("POST", "/api/v1/restores", bad); status != http.StatusUnprocessableEntity {
		t.Fatalf("restore with a tampered token = %d, want 422", status)
	}
	if h.env.Restored("recovered") {
		t.Fatal("a rejected restore must not write anything")
	}

	req["confirmation_token"], req["idempotency_key"] = token, "restore-1"
	status, started := h.api("POST", "/api/v1/restores", req)
	if status != http.StatusAccepted {
		t.Fatalf("start restore = %d %v", status, started)
	}
	jobID := num(started["job"].(map[string]any)["id"])
	if job := h.waitJob(jobID); job["state"] != "succeeded" {
		t.Fatalf("restore job = %v", job)
	}
	if got := h.env.ReadRestored("recovered/b.txt"); got != "bravo" {
		t.Fatalf("restored content = %q", got)
	}
	// A repeated submission with the same idempotency key does not start a second restore.
	_, again := h.api("POST", "/api/v1/restores", req)
	if num(again["job"].(map[string]any)["id"]) != jobID {
		t.Fatalf("idempotency key created another job: %v", again)
	}

	for _, action := range []string{"auth.login", "source.create", "backup.request", "backup.finish", "restore.preview", "restore.request", "restore.finish"} {
		_, out := h.api("GET", "/api/v1/audit?action="+action, nil)
		if events, _ := out["events"].([]any); len(events) == 0 && action != "source.create" {
			t.Errorf("no %q event in the audit log", action)
		}
	}
}

func TestRestoreRequestsAreValidatedAtTheEdge(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	p := h.env.Backup()
	base := func() map[string]any {
		return map[string]any{"source_id": h.env.Src.ID, "point_id": p.ID, "paths": []string{"a.txt"}, "dest_name": "ok", "policy": "skip"}
	}
	cases := map[string]func(m map[string]any){
		"traversal in dest": func(m map[string]any) { m["dest_name"] = "../escape" },
		"nested dest":       func(m map[string]any) { m["dest_name"] = "a/b" },
		"absolute dest":     func(m map[string]any) { m["dest_name"] = `C:\Windows` },
		"traversal in path": func(m map[string]any) { m["paths"] = []string{"../../secret"} },
		"unknown policy":    func(m map[string]any) { m["policy"] = "wipe" },
		"nothing selected":  func(m map[string]any) { m["paths"] = []string{} },
		"missing path":      func(m map[string]any) { m["paths"] = []string{"nope.txt"} },
	}
	for name, mutate := range cases {
		m := base()
		mutate(m)
		if status, out := h.api("POST", "/api/v1/restores/preview", m); status != http.StatusBadRequest {
			t.Errorf("%s: preview status = %d %v, want 400", name, status, out)
		}
	}
	if status, _ := h.api("POST", "/api/v1/restores/preview", map[string]any{"unknown_field": 1}); status != http.StatusBadRequest {
		t.Errorf("unknown JSON fields must be rejected, got %d", status)
	}
	if status, _ := h.api("GET", "/api/v1/files?source_id=1&path=../../etc", nil); status != http.StatusBadRequest {
		t.Errorf("path traversal in files = %d, want 400", status)
	}
	if status, _ := h.api("GET", "/api/v1/history/versions?source_id=1&path=..%5C..%5Cx", nil); status != http.StatusBadRequest {
		t.Errorf("backslash traversal = %d, want 400", status)
	}
	if status, out := h.api("GET", "/api/v1/jobs/999999", nil); status != http.StatusNotFound {
		t.Errorf("unknown job = %d %v", status, out)
	}
	if status, _ := h.api("GET", "/api/v1/nope", nil); status != http.StatusNotFound {
		t.Errorf("unknown endpoint = %d", status)
	}
}

func TestPagesShowRealDataInBothLanguages(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	h.env.Write(`{{7+7}}.txt`, "template-looking name")
	h.env.Write("a&b'c.txt", "special characters")
	first := h.env.Backup()
	h.env.Write("a.txt", "alpha v2")
	h.env.Remove("docs/b.txt")
	h.env.Write("brand-new.txt", "not yet backed up")
	h.env.Backup()
	h.env.Write("brand-new.txt", "not yet backed up, edited")
	h.env.Write("only-live.txt", "never backed up")
	h.env.Remove("docs/deep")

	_, body := h.get("/explorer?source=1")
	// html/template writes "+" as &#43;, which a browser shows as the original character.
	for _, want := range []string{"a.txt", "docs", "Protected", "Changed", "New", "{{7&#43;7}}.txt", "a&amp;b&#39;c.txt", "Open in Time Machine"} {
		if !strings.Contains(body, want) {
			t.Errorf("explorer is missing %q", want)
		}
	}
	if strings.Contains(body, ">14.txt<") {
		t.Error("data must never be evaluated as a template")
	}
	assertCSPClean(t, "explorer", body)

	_, body = h.get("/explorer?source=1&path=docs")
	if !strings.Contains(body, "Deleted since the last backup (1)") || !strings.Contains(body, "deep") {
		t.Errorf("the deleted folder must be offered for recovery:\n%s", body)
	}

	_, body = h.get("/history?source=1&point=" + itoa(first.ID))
	for _, want := range []string{"Viewing recovery point #1", "Changed later", "Live scan", "Restore selected", "tv-timeline"} {
		if !strings.Contains(body, want) {
			t.Errorf("history is missing %q", want)
		}
	}
	assertCSPClean(t, "history", body)
	_, body = h.get("/history?source=1&path=docs&point=" + itoa(first.ID))
	if !strings.Contains(body, "Deleted later") {
		t.Errorf("a file deleted after this point must be marked:\n%s", body)
	}

	_, body = h.get("/versions?source=1&path=a.txt")
	if !strings.Contains(body, "2 recorded version") || !strings.Contains(body, "Current") {
		t.Errorf("versions page: %s", body)
	}
	assertCSPClean(t, "versions", body)

	_, body = h.get("/backups")
	for _, want := range []string{"Team Files", "Back up now", "Incremental", "Full", "Manage monitored folders"} {
		if !strings.Contains(body, want) {
			t.Errorf("backups page is missing %q", want)
		}
	}
	assertCSPClean(t, "backups", body)

	for _, page := range []string{"/audit", "/status", "/settings", "/explorer?source=999"} {
		_, body = h.get(page)
		assertCSPClean(t, page, body)
	}

	zh := map[string]string{"Accept-Language": "zh-CN,zh;q=0.9"}
	_, body = h.get("/history?source=1", zh)
	if !strings.Contains(body, "时间机器") || !strings.Contains(body, `lang="zh"`) {
		t.Errorf("Chinese history page: %s", body)
	}
	_, body = h.get("/explorer?source=1", zh)
	if !strings.Contains(body, "已保护") || strings.Contains(body, "Protected") {
		t.Errorf("Chinese explorer page: %s", body)
	}
	_, body = h.get("/explorer?source=1", map[string]string{"Accept-Language": "zh-CN", "Cookie": "tv_lang=en"})
	if !strings.Contains(body, "Protected") {
		t.Error("the language cookie must win over Accept-Language")
	}
}

func TestLanguageSwitchEndpoint(t *testing.T) {
	h := newHarness(t)
	res, _ := h.get("/lang?set=zh&next=/backups")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/backups" {
		t.Fatalf("lang switch = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	found := false
	for _, c := range res.Cookies() {
		if c.Name == "tv_lang" && c.Value == "zh" {
			found = true
		}
	}
	if !found {
		t.Fatal("the language cookie was not set")
	}
	res, _ = h.get("/lang?set=fr&next=//evil.example")
	if res.Header.Get("Location") != "/" || len(res.Cookies()) != 0 {
		t.Fatalf("an unknown language or foreign next must be ignored: %q %v", res.Header.Get("Location"), res.Cookies())
	}
	// http.Redirect cleans "/./\evil.example" into "/\evil.example", which browsers read as //evil.example.
	for _, next := range []string{`/./\evil.example`, "/\t/evil.example", `/a/..\evil.example`} {
		res, _ = h.get("/lang?set=en&next=" + url.QueryEscape(next))
		if got := res.Header.Get("Location"); got != "/" {
			t.Fatalf("next=%q redirected to %q, want /", next, got)
		}
	}
}

// A page that answers a form post has no address a GET can load, so its language links must lead
// back to a page that exists instead of to the POST route, which is a 404.
func TestLanguageLinksOfPostedPagesLeadToExistingPages(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	point := h.env.Backup()

	langNext := func(page string) string {
		t.Helper()
		m := regexp.MustCompile(`href="(/lang\?[^"]*set=zh[^"]*)"`).FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("the page has no language link:\n%s", page)
		}
		u, err := url.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
		if err != nil {
			t.Fatal(err)
		}
		return u.Query().Get("next")
	}

	form := url.Values{"source_id": {"1"}, "point_id": {itoa(point.ID)}, "paths": {"a.txt"}, "dest_name": {"lang-check"}, "policy": {"skip"}}
	res, page := h.postForm("/restore/preview", form)
	back := "/history?point=" + itoa(point.ID) + "&source=1"
	if res.StatusCode != http.StatusOK || langNext(page) != back {
		t.Fatalf("preview = %d, its language links lead to %q, want %q", res.StatusCode, langNext(page), back)
	}
	if res, _ = h.get(back); res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", back, res.StatusCode)
	}

	// Any other page that answers a post falls back to the home page.
	form.Set("point_id", "999999")
	res, page = h.postForm("/restore/preview", form)
	if res.StatusCode == http.StatusOK || langNext(page) != "/" {
		t.Fatalf("preview of an unknown point = %d, its language links lead to %q, want /", res.StatusCode, langNext(page))
	}

	// A failed sign-in keeps the page the reader was heading to.
	res, page = h.postForm("/login", url.Values{"username": {"admin"}, "password": {"wrong"}, "next": {"/backups"}})
	if want := "/login?next=%2Fbackups"; res.StatusCode != http.StatusUnauthorized || langNext(page) != want {
		t.Fatalf("failed sign-in = %d, its language links lead to %q, want %q", res.StatusCode, langNext(page), want)
	}

	// A page that answers a GET keeps its own address, query included.
	_, page = h.get("/explorer?source=1")
	if got := langNext(page); got != "/explorer?source=1" {
		t.Fatalf("language links on a GET page lead to %q", got)
	}
}

// Every row of the Status page is translated, including the details the service reports.
func TestStatusPageIsFullyTranslated(t *testing.T) {
	h := newHarness(t)
	h.login()
	if res, _ := h.get("/lang?set=zh&next=/status"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("language switch = %d", res.StatusCode)
	}
	_, zh := h.get("/status")
	for _, want := range []string{"MVP 未实现", "搜索索引已关闭", "个运行中"} {
		if !strings.Contains(zh, want) {
			t.Errorf("the Chinese status page lacks %q", want)
		}
	}
	for _, english := range []string{"not implemented in the MVP", "the search index is off", "running,"} {
		if strings.Contains(zh, english) {
			t.Errorf("the Chinese status page still shows the English text %q", english)
		}
	}
	if res, _ := h.get("/lang?set=en&next=/status"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("language switch = %d", res.StatusCode)
	}
	if _, en := h.get("/status"); !strings.Contains(en, "Not implemented in the MVP") || !strings.Contains(en, "running, ") {
		t.Error("the English status page lacks its details")
	}
}

// Asset URLs carry a fingerprint of their content, so a rebuilt program never serves stale CSS or JS.
func TestAssetsAreFingerprintedAndCachedAccordingly(t *testing.T) {
	h := newHarness(t)
	_, page := h.get("/login")
	m := regexp.MustCompile(`/static/css/app\.css\?v=([0-9a-f]{12})"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page does not reference a fingerprinted stylesheet:\n%s", page)
	}
	for _, asset := range []string{"/static/js/app.js", "/static/js/theme-init.js"} {
		if !strings.Contains(page, asset+"?v="+m[1]) {
			t.Errorf("%s is not referenced with the same fingerprint", asset)
		}
	}
	res, _ := h.get("/static/css/app.css?v=" + m[1])
	if cc := res.Header.Get("Cache-Control"); res.StatusCode != http.StatusOK || !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("fingerprinted asset: %d, Cache-Control %q", res.StatusCode, cc)
	}
	// Without the current fingerprint the browser must ask again.
	for _, u := range []string{"/static/css/app.css", "/static/css/app.css?v=stale", "/static/img/icons.svg"} {
		res, _ := h.get(u)
		if cc := res.Header.Get("Cache-Control"); res.StatusCode != http.StatusOK || cc != "no-cache" {
			t.Errorf("%s: %d, Cache-Control %q, want no-cache", u, res.StatusCode, cc)
		}
	}
}

func TestPartialRenderingForInPageNavigation(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	h.env.Backup()
	res, body := h.get("/explorer?source=1", map[string]string{"X-TV-Partial": "1"})
	if res.StatusCode != 200 || strings.Contains(body, "<html") || !strings.Contains(body, "tv-crumbs") {
		t.Fatalf("partial response: %d %s", res.StatusCode, body)
	}
	if res.Header.Get("X-TV-Nav") != "explorer" || res.Header.Get("X-TV-Title") == "" {
		t.Fatalf("partial headers = %v", res.Header)
	}
}

func TestRestoreThroughTheForms(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	first := h.env.Backup()
	h.env.Write("a.txt", "alpha v2")
	second := h.env.Backup()

	form := func(point int64, extra url.Values) url.Values {
		v := url.Values{"source_id": {"1"}, "point_id": {itoa(point)}, "paths": {"a.txt", "docs"}, "dest_name": {"ui-restore"}, "policy": {"skip"}}
		for k, vals := range extra {
			v[k] = vals
		}
		return v
	}
	res, body := h.postForm("/restore/preview", form(first.ID, nil))
	if res.StatusCode != 200 || !strings.Contains(body, "Restore preview") || !strings.Contains(body, "Start restore") {
		t.Fatalf("preview page: %d", res.StatusCode)
	}
	assertCSPClean(t, "restore preview", body)
	token, idem := hidden(body, "token"), hidden(body, "idem")
	if token == "" || idem == "" {
		t.Fatalf("the preview must carry a token and an idempotency key")
	}

	start := form(first.ID, url.Values{"token": {token}, "idem": {idem}})
	res, _ = h.postForm("/restore/start", start)
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/jobs/") {
		t.Fatalf("start = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	jobURL := res.Header.Get("Location")
	var jobID int64
	for _, c := range jobURL[len("/jobs/"):] {
		jobID = jobID*10 + int64(c-'0')
	}
	if job := h.waitJob(jobID); job["state"] != "succeeded" {
		t.Fatalf("restore job: %v", job)
	}
	if got := h.env.ReadRestored("ui-restore/a.txt"); got != "alpha" {
		t.Fatalf("restored a.txt = %q (must be the content of the FIRST point)", got)
	}
	if got := h.env.ReadRestored("ui-restore/docs/deep/c.txt"); got != "charlie" {
		t.Fatalf("restored c.txt = %q", got)
	}
	_, page := h.get(jobURL)
	for _, want := range []string{"Restore details", "a.txt", "docs"} {
		if !strings.Contains(page, want) {
			t.Errorf("job page is missing %q", want)
		}
	}
	assertCSPClean(t, "job page", page)

	// Double submit: the same idempotency key returns the same job.
	res, _ = h.postForm("/restore/start", start)
	if res.Header.Get("Location") != jobURL {
		t.Fatalf("double submit created another job: %q vs %q", res.Header.Get("Location"), jobURL)
	}

	// Replacing existing files needs an explicit confirmation, and without it nothing happens.
	over := url.Values{"policy": {"overwrite"}}
	_, body = h.postForm("/restore/preview", form(second.ID, over))
	if !strings.Contains(body, "will be replaced") {
		t.Fatalf("the preview must warn about replacing files:\n%s", body)
	}
	token2, idem2 := hidden(body, "token"), hidden(body, "idem")
	noConfirm := form(second.ID, url.Values{"policy": {"overwrite"}, "token": {token2}, "idem": {idem2}})
	res, body = h.postForm("/restore/start", noConfirm)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Start restore") {
		t.Fatalf("overwrite without confirmation = %d", res.StatusCode)
	}
	if got := h.env.ReadRestored("ui-restore/a.txt"); got != "alpha" {
		t.Fatalf("a rejected overwrite changed the file: %q", got)
	}
	token3, idem3 := hidden(body, "token"), hidden(body, "idem")
	confirmed := form(second.ID, url.Values{"policy": {"overwrite"}, "token": {token3}, "idem": {idem3}, "confirm_overwrite": {"1"}})
	res, _ = h.postForm("/restore/start", confirmed)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("confirmed overwrite = %d", res.StatusCode)
	}
	var overID int64
	for _, c := range res.Header.Get("Location")[len("/jobs/"):] {
		overID = overID*10 + int64(c-'0')
	}
	if job := h.waitJob(overID); job["state"] != "succeeded" {
		t.Fatalf("overwrite job: %v", job)
	}
	if got := h.env.ReadRestored("ui-restore/a.txt"); got != "alpha v2" {
		t.Fatalf("a.txt after the confirmed overwrite = %q", got)
	}
}

func TestSearchAndItsDegradedState(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	h.env.Backup()
	mock := search.NewMock([]search.Result{
		{Name: "b.txt", Path: filepath.Join(h.env.SrcDir, "docs", "b.txt"), Size: 5, ModTime: time.Now()},
		{Name: "elsewhere.txt", Path: filepath.Join(filepath.Dir(h.env.SrcDir), "other", "b-elsewhere.txt"), Size: 1},
	})
	h.search.set(mock)

	_, body := h.get("/explorer?source=1&q=b.txt")
	if !strings.Contains(body, "b.txt") || !strings.Contains(body, "1 matches") || strings.Contains(body, "elsewhere") {
		t.Fatalf("search results page:\n%s", body)
	}
	if !strings.Contains(body, "/versions?path=docs%2Fb.txt&amp;source=1") && !strings.Contains(body, "path=docs%2Fb.txt") {
		t.Errorf("a hit must link to its version history:\n%s", body)
	}

	status, out := h.api("GET", "/api/v1/search?source_id=1&q=b.txt", nil)
	if status != 200 || len(out["results"].([]any)) != 1 {
		t.Fatalf("API search = %d %v", status, out)
	}

	mock.SetUnavailable(true)
	res, body := h.get("/explorer?source=1&q=b.txt")
	if res.StatusCode != 200 || !strings.Contains(body, "Search is unavailable") {
		t.Fatalf("degraded search page = %d", res.StatusCode)
	}
	_, body = h.get("/explorer?source=1")
	if !strings.Contains(body, "a.txt") {
		t.Fatal("browsing must keep working while search is down")
	}
	if status, out := h.api("GET", "/api/v1/search?source_id=1&q=b.txt", nil); status != http.StatusServiceUnavailable || out["error"].(map[string]any)["code"] != "search_unavailable" {
		t.Fatalf("API search while down = %d %v", status, out)
	}
	_, out = h.api("GET", "/api/v1/health", nil)
	var searchState string
	for _, c := range out["components"].([]any) {
		if m := c.(map[string]any); m["name"] == "search" {
			searchState, _ = m["state"].(string)
		}
	}
	if searchState != "degraded" {
		t.Fatalf("health must report search as degraded, got %q", searchState)
	}

	mock.SetUnavailable(false)
	_, body = h.get("/explorer?source=1&q=" + url.QueryEscape(strings.Repeat("word ", 40)))
	if !strings.Contains(body, "not valid") {
		t.Errorf("an over-long query must be rejected with a message:\n%s", body)
	}
}

func TestSourceRegistrationThroughTheForm(t *testing.T) {
	h := newHarness(t)
	h.login()
	other := t.TempDir()

	res, _ := h.postForm("/settings/sources", url.Values{"name": {"Second"}, "root_path": {other}, "excludes": {"*.tmp\n"}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings" {
		t.Fatalf("add source = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	_, body := h.get("/settings")
	if !strings.Contains(body, "Second") || !strings.Contains(body, other) || !strings.Contains(body, "Monitored folder added.") {
		t.Fatalf("settings page after adding:\n%s", body)
	}

	for name, v := range map[string]url.Values{
		"overlaps the repository":  {"name": {"Bad"}, "root_path": {h.env.Repo.Root()}},
		"relative path":            {"name": {"Bad"}, "root_path": {`data\x`}},
		"missing folder":           {"name": {"Bad"}, "root_path": {filepath.Join(other, "missing")}},
		"duplicate name":           {"name": {"Second"}, "root_path": {t.TempDir()}},
		"interval is not a number": {"name": {"Bad"}, "root_path": {t.TempDir()}, "schedule": {"1"}, "schedule_minutes": {"often"}},
		"interval is too short":    {"name": {"Bad"}, "root_path": {t.TempDir()}, "schedule": {"1"}, "schedule_minutes": {"0"}},
	} {
		res, body := h.postForm("/settings/sources", v)
		if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusConflict {
			t.Errorf("%s: status %d", name, res.StatusCode)
			continue
		}
		if !strings.Contains(body, "tv-alert-error") || !strings.Contains(body, v.Get("name")) {
			t.Errorf("%s: the form must show the problem and keep the input", name)
		}
	}
	if _, err := os.Stat(filepath.Join(h.env.Repo.Root(), "objects")); err != nil {
		t.Fatal("the repository was disturbed")
	}
}

func TestInternalErrorsDoNotLeakDetails(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.env.Pool.Close() // every query now fails
	res, body := h.get("/backups")
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	for _, leak := range []string{"pgx", "closed pool", "SELECT", "postgres", h.env.SrcDir, h.env.Repo.Root()} {
		if strings.Contains(body, leak) {
			t.Errorf("the error page leaks %q", leak)
		}
	}
	if !strings.Contains(body, "Reference") {
		t.Error("the error page must carry a reference to the server log")
	}
	status, out := h.api("GET", "/api/v1/sources", nil)
	text, _ := json.Marshal(out)
	if status != http.StatusInternalServerError || strings.Contains(string(text), "pgx") || strings.Contains(string(text), "pool") {
		t.Fatalf("API internal error = %d %s", status, text)
	}
}
