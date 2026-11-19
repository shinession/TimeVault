package server_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"timevault/internal/audit"
	"timevault/internal/config"
	"timevault/internal/server"
	"timevault/internal/service"
	"timevault/internal/weblog"
)

// logBuffer collects what the HTTP server logs.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// within polls ok until it is true or d has passed. The line about a request is written after its answer has been
// sent, so a test that looks at once may be a moment early.
func within(d time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		if ok() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// find returns the first line that holds all of parts, waiting a little for it.
func (l *logBuffer) find(parts ...string) (found string) {
	within(2*time.Second, func() bool {
		for _, line := range strings.Split(l.String(), "\n") {
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(line, p)
			}
			if ok {
				found = line
				return true
			}
		}
		return false
	})
	return found
}

// webRecorder stands in for the web log: it keeps the lines it is given.
type webRecorder struct {
	mu  sync.Mutex
	got []weblog.Entry
}

func (r *webRecorder) Write(e weblog.Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, e)
}

func (r *webRecorder) entries() []weblog.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]weblog.Entry(nil), r.got...)
}

// has returns the first line whose message starts with prefix, waiting a little for it.
func (r *webRecorder) has(prefix string) (found weblog.Entry, ok bool) {
	ok = within(2*time.Second, func() bool {
		for _, e := range r.entries() {
			if strings.HasPrefix(e.Message, prefix) {
				found = e
				return true
			}
		}
		return false
	})
	return found, ok
}

func (r *webRecorder) messages() []string {
	var out []string
	for _, e := range r.entries() {
		out = append(out, e.Message)
	}
	return out
}

func TestTheRequestLogNamesTheUserAndTheAddress(t *testing.T) {
	h := newHarness(t)
	guest := h.visitor()
	guest.get("/explorer")
	line := h.logs.find("msg=request", "path=/explorer")
	for _, want := range []string{"user=guest", "ip=" + guest.addr, "method=GET", "status=200"} {
		if !strings.Contains(line, want) {
			t.Errorf("the request of a visitor is logged as %q, want %q in it", line, want)
		}
	}

	// A sign-in starts as a visitor and ends as an account, and the line says who the request ended as.
	admin := h.visitor()
	admin.login()
	login := h.logs.find("path=/login", "method=POST")
	if !strings.Contains(login, "user=admin") || !strings.Contains(login, "ip="+admin.addr) {
		t.Errorf("the sign-in is logged as %q, want the account that signed in and its address", login)
	}
	admin.get("/audit")
	if line := h.logs.find("path=/audit"); !strings.Contains(line, "user=admin") {
		t.Errorf("a request of the signed-in account is logged as %q", line)
	}

	// The files of the page and the health check are not requests anybody wants to read.
	guest.get("/static/css/app.css")
	guest.get("/healthz")
	if got := h.logs.String(); strings.Contains(got, "path=/static/") || strings.Contains(got, "path=/healthz") {
		t.Errorf("static files and the health check are logged:\n%s", got)
	}
}

func TestTheRequestLogShowsTheBrowserBehindATrustedProxy(t *testing.T) {
	h := newHarnessOpts(t, nil, func(o *server.Options) {
		o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.8.0.0/16")}
	})
	proxy := h.visitor() // the connection comes from 10.8.x.y, the proxy
	proxy.get("/explorer", map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if line := h.logs.find("path=/explorer"); !strings.Contains(line, "ip=203.0.113.9") {
		t.Errorf("logged %q, want the address the proxy reports for the browser", line)
	}
	// A header that a browser sends itself is not believed.
	direct := h.visitor()
	direct.addr = "198.51.100.7"
	direct.get("/history", map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if line := h.logs.find("path=/history"); !strings.Contains(line, "ip=198.51.100.7") {
		t.Errorf("logged %q, want the address that connected", line)
	}
}

func TestPagesReachTheWebLogButNotTheNoise(t *testing.T) {
	rec := &webRecorder{}
	h := newHarnessOpts(t, nil, func(o *server.Options) { o.WebLog = rec })
	v := h.visitor()
	v.get("/login")
	v.get("/explorer")
	v.get("/nosuch")
	v.get("/healthz")
	v.get("/static/css/app.css")
	v.get("/")                                              // a redirect to a page, which is reported when it is opened
	v.postForm("/backups/run", nil)                         // what a visitor does with a form is in the audit trail
	v.get("/api/v1/session")                                // the page script asks for data
	v.get("/explorer", map[string]string{"X-TV-Auto": "1"}) // and reloads a page by itself
	within(2*time.Second, func() bool { return len(rec.entries()) >= 5 })
	type line struct {
		msg   string
		level slog.Level
		code  int
	}
	var got []line
	for _, e := range rec.entries() {
		if e.IP != v.addr {
			t.Errorf("%q has the address %q, want %q", e.Message, e.IP, v.addr)
		}
		if e.Class != weblog.ClassPage {
			t.Errorf("%q is not a page line", e.Message)
		}
		got = append(got, line{e.Message[:strings.LastIndex(e.Message, " ")], e.Level, e.Status})
	}
	want := []line{
		{"TimeVault: /login user=guest", slog.LevelInfo, 200},
		{"TimeVault: /explorer user=guest", slog.LevelInfo, 200},
		{"TimeVault: (not found) user=guest", slog.LevelWarn, 404},
		{"TimeVault: /api/v1/session user=guest", slog.LevelDebug, 200},
		{"TimeVault: /explorer user=guest", slog.LevelDebug, 200},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %v\nwant %d: %v", len(got), rec.messages(), len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if e, ok := rec.has("TimeVault: /login"); !ok || !strings.HasSuffix(e.Message, "(s)") {
		t.Errorf("a page line ends with the time it took, like 0.112(s): %q", e.Message)
	}
}

func TestPagesOfASignedInAccountNameIt(t *testing.T) {
	rec := &webRecorder{}
	h := newHarnessOpts(t, nil, func(o *server.Options) { o.WebLog = rec })
	admin := h.visitor()
	admin.login()
	admin.get("/audit")
	if _, ok := rec.has("TimeVault: /audit user=admin "); !ok {
		t.Errorf("lines: %v, want the audit page opened by admin", rec.messages())
	}
	// The path that was typed never reaches the web log, only the route that matched.
	admin.get("/jobs/12345")
	admin.get("/explorer?q=secret+plans&path=hr/salaries")
	for _, m := range rec.messages() {
		if strings.Contains(m, "secret") || strings.Contains(m, "salaries") {
			t.Errorf("the web log holds what the visitor typed: %q", m)
		}
	}
	if _, ok := rec.has("TimeVault: /jobs/12345 user=admin "); !ok {
		t.Errorf("lines: %v, want the route of the job page with its number", rec.messages())
	}
}

func TestWhatPeopleDoReachesTheWebLogThroughTheAuditTrail(t *testing.T) {
	rec := &webRecorder{}
	h := newHarnessWith(t, func(c *service.Config, _ *config.Deployment) {
		c.Audit.SetMirror(func(e audit.Event) { rec.Write(weblog.Audit(e)) })
	})
	seed(h)
	admin := h.visitor()
	admin.login()
	if e, ok := rec.has("TimeVault: auth.login user=admin"); !ok || e.IP != admin.addr || e.Status != 200 || e.Level != slog.LevelInfo {
		t.Errorf("the sign-in: %+v (found %v), lines %v", e, ok, rec.messages())
	}

	// A failed sign-in is a warning, and the name that was typed (it may be a password) is not copied.
	guest := h.visitor()
	res, _ := guest.postForm("/login", url.Values{"username": {"not-a-name-but-a-password"}, "password": {"x"}})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sign-in with a wrong password = %d", res.StatusCode)
	}
	e, ok := rec.has("TimeVault: auth.login_failed")
	if !ok || e.Message != "TimeVault: auth.login_failed user=-" || e.Level != slog.LevelWarn || e.Status != 403 || e.IP != guest.addr {
		t.Errorf("the failed sign-in: %+v (found %v)", e, ok)
	}
	for _, m := range rec.messages() {
		if strings.Contains(m, "password") {
			t.Errorf("the name that was typed is in the web log: %q", m)
		}
	}

	// A backup that was asked for, and its result, name the folder and the account.
	src := itoa(h.env.Src.ID)
	status, out := admin.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	admin.waitJob(num(out["job"].(map[string]any)["id"]))
	if _, ok := rec.has("TimeVault: backup.request source=" + src + " user=admin"); !ok {
		t.Errorf("no line for the request of the backup: %v", rec.messages())
	}
	finish, ok := rec.has("TimeVault: backup.finish source=" + src + " point=")
	if !ok || !strings.Contains(finish.Message, " user=admin ") || !strings.HasSuffix(finish.Message, "(s)") {
		t.Errorf("no line for the result of the backup: %v", rec.messages())
	}

	// A refusal names the route that matched and the account, and nothing that was typed.
	alice := h.visitor()
	alice.loginAs(userID)
	if res, _ := alice.postForm("/settings/sources", url.Values{"name": {"x"}, "root_path": {"D:\\secret\\hr"}}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("an ordinary account adding a folder = %d, want 403", res.StatusCode)
	}
	denied, ok := rec.has("TimeVault: access.denied")
	if !ok || denied.Message != "TimeVault: access.denied method=POST route=/settings/sources user="+userID ||
		denied.Level != slog.LevelWarn || denied.Status != 403 || denied.IP != alice.addr {
		t.Errorf("the refusal: %+v (found %v), lines %v", denied, ok, rec.messages())
	}
	for _, m := range rec.messages() {
		if strings.Contains(m, "secret") {
			t.Errorf("what was typed is in the web log: %q", m)
		}
	}
}
