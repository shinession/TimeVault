package server_test

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"timevault/internal/auth"
	"timevault/internal/server"
)

const ssoHost = "http://sso.test:8080"

var ssoTokenShape = regexp.MustCompile(`^[a-z2-7]{24}$`)

// ssoHarness is a server that offers single sign-on at ssoHost and checks the browser address, as in production.
func ssoHarness(t *testing.T, tune func(*server.Options)) *harness {
	t.Helper()
	return newHarnessOpts(t, nil, func(o *server.Options) {
		o.SSOHost, o.SSORequireSameIP = ssoHost, true
		o.Deployment.SSOHost = ssoHost
		if tune != nil {
			tune(o)
		}
	})
}

type ssoStart struct {
	Token     string `json:"token"`
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
}

func (h *harness) ssoStart(next string) (int, ssoStart) {
	h.t.Helper()
	res, body := h.postForm("/auth/sso/start", url.Values{"next": {next}})
	var out ssoStart
	_ = json.Unmarshal([]byte(body), &out)
	return res.StatusCode, out
}

// ssoPoll asks for the outcome of a sign-in after letting a little time pass, as the browser does between looks.
func (h *harness) ssoPoll(token string) map[string]any {
	h.t.Helper()
	h.clock.Advance(2 * time.Second)
	res, body := h.postForm("/auth/sso/poll", url.Values{"token": {token}})
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("poll = %d %s", res.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		h.t.Fatalf("poll answer %q: %v", body, err)
	}
	return out
}

// signedInAs reports who the browser is signed in as, "" for nobody.
func (h *harness) signedInAs() string {
	h.t.Helper()
	_, body := h.get("/api/v1/session")
	var s struct {
		User string `json:"user"`
	}
	_ = json.Unmarshal([]byte(body), &s)
	return s.User
}

// auditEvents lists the newest events of one action, as an administrator sees them.
func auditEvents(h *harness, action string) []map[string]any {
	h.t.Helper()
	adm := h.visitor()
	adm.login()
	_, out := adm.api("GET", "/api/v1/audit?limit=50&action="+action, nil)
	var events []map[string]any
	for _, e := range out["events"].([]any) {
		events = append(events, e.(map[string]any))
	}
	return events
}

func detailOf(e map[string]any) map[string]any {
	d, _ := e["detail"].(map[string]any)
	return d
}

func okResult(h *harness, ecode, windowsUser string) auth.SSOResult {
	return auth.SSOResult{Status: "ok", ECode: ecode, WindowsUser: windowsUser, IP: h.addr, At: time.Now()}
}

func TestSSOIsOfferedOnlyWhenItIsConfigured(t *testing.T) {
	off := newHarness(t)
	if _, body := off.get("/login"); strings.Contains(body, "data-sso-login") {
		t.Error("the login page offers single sign-on although no sign-on service is configured")
	}
	if status, _ := off.ssoStart("/"); status != http.StatusNotFound {
		t.Errorf("start without a sign-on service = %d, want 404", status)
	}
	if res, _ := off.postForm("/auth/sso/poll", url.Values{"token": {"abcdefghijklmnopqrstuvwx"}}); res.StatusCode != http.StatusNotFound {
		t.Errorf("poll without a sign-on service = %d, want 404", res.StatusCode)
	}

	on := ssoHarness(t, nil)
	res, body := on.get("/login?next=%2Fexplorer")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "data-sso-login") || !strings.Contains(body, `data-start="/auth/sso/start"`) ||
		!strings.Contains(body, `data-next="/explorer"`) {
		t.Fatalf("login page = %d, want the single sign-on button:\n%s", res.StatusCode, body)
	}
	assertCSPClean(t, "/login with single sign-on", body)
	// The password form stays.
	if !strings.Contains(body, `action="/login"`) {
		t.Error("the password form is gone")
	}

	// The running configuration names the sign-on service, on the page and in the API.
	adm := on.visitor()
	adm.login()
	if _, page := adm.get("/settings"); !strings.Contains(page, ssoHost) {
		t.Errorf("the Settings page does not show the sign-on service:\n%s", page)
	}
	if _, out := adm.api("GET", "/api/v1/settings", nil); out["sso_host"] != ssoHost {
		t.Errorf("settings sso_host = %v, want %q", out["sso_host"], ssoHost)
	}
}

func TestSSOStartIssuesATokenAndTheAddressOfTheSignOnPage(t *testing.T) {
	h := ssoHarness(t, nil)
	v := h.visitor()
	status, a := v.ssoStart("/backups")
	if status != http.StatusOK || !ssoTokenShape.MatchString(a.Token) {
		t.Fatalf("start = %d %+v", status, a)
	}
	if want := ssoHost + "/sso/api/login?token=" + a.Token; a.URL != want || a.ExpiresIn != 300 {
		t.Fatalf("start = %+v, want the sign-on page %q valid for 300 seconds", a, want)
	}
	if _, b := v.ssoStart("/backups"); b.Token == a.Token || !ssoTokenShape.MatchString(b.Token) {
		t.Errorf("a second sign-in got the token %q again", b.Token)
	}
	if res, _ := v.postForm("/auth/sso/start", url.Values{"next": {"/"}}, map[string]string{"Origin": "https://evil.example"}); res.StatusCode != http.StatusForbidden {
		t.Errorf("start from a foreign site = %d, want 403", res.StatusCode)
	}
}

func TestSSOSignsInOnceTheSignOnServiceHasRecordedTheUser(t *testing.T) {
	h := ssoHarness(t, nil)
	v := h.visitor()
	_, start := v.ssoStart("/backups")

	if got := v.ssoPoll(start.Token); got["status"] != "pending" {
		t.Fatalf("poll before the sign-on service answered = %v, want pending", got)
	}
	if v.signedInAs() != "" {
		t.Fatal("signed in before the sign-on service answered")
	}

	h.dir.setSSO(start.Token, okResult(v, userID, `CO\e1001`))
	got := v.ssoPoll(start.Token)
	if got["status"] != "ok" || got["next"] != "/backups" {
		t.Fatalf("poll after the sign-on service answered = %v, want ok and the page asked for", got)
	}
	if who := v.signedInAs(); who != userID {
		t.Fatalf("signed in as %q, want %s", who, userID)
	}
	// The session works like any other.
	if res, _ := v.get("/backups"); res.StatusCode != http.StatusOK {
		t.Fatalf("/backups = %d", res.StatusCode)
	}

	// The result is used up: it cannot sign anybody in a second time, not even the same browser.
	other := h.visitor()
	if again := v.ssoPoll(start.Token); again["status"] != "expired" {
		t.Errorf("second poll = %v, want expired", again)
	}
	if again := other.ssoPoll(start.Token); again["status"] != "expired" {
		t.Errorf("poll of the used token from another browser = %v, want expired", again)
	}

	logins := auditEvents(h, "auth.login")
	var found map[string]any
	for _, e := range logins {
		if d := detailOf(e); d["method"] == "sso" {
			found = e
		}
	}
	if found == nil || found["actor"] != userID || found["remote_addr"] != v.addr || detailOf(found)["windows_user"] != `CO\e1001` {
		t.Fatalf("no audit record of the single sign-on in %v", logins)
	}
}

func TestSSORefusesAnAddressThatIsNotTheBrowsers(t *testing.T) {
	h := ssoHarness(t, nil)
	v := h.visitor()

	// The sign-on page was opened by somebody else: the sign-on service saw another address.
	_, start := v.ssoStart("/")
	h.dir.setSSO(start.Token, auth.SSOResult{Status: "ok", ECode: adminID, WindowsUser: `CO\e2002`, IP: "203.0.113.99", At: time.Now()})
	if got := v.ssoPoll(start.Token); got["status"] != "mismatch" {
		t.Fatalf("poll = %v, want mismatch", got)
	}
	if v.signedInAs() != "" {
		t.Fatal("signed in although the sign-on service saw another address")
	}
	if got := v.ssoPoll(start.Token); got["status"] != "expired" {
		t.Errorf("the refused token can be used again: %v", got)
	}

	// The address of the sign-on service may carry a port, and IPv6 may be written in several ways.
	_, start = v.ssoStart("/")
	h.dir.setSSO(start.Token, okResult(v, userID, `CO\e1001`))
	res := okResult(v, userID, `CO\e1001`)
	res.IP = v.addr + ":51234"
	h.dir.setSSO(start.Token, res)
	if got := v.ssoPoll(start.Token); got["status"] != "ok" {
		t.Fatalf("an address with a port = %v, want ok", got)
	}

	// The browser that asks is not the one that started the sign-in.
	v2 := h.visitor()
	_, start = v2.ssoStart("/")
	h.dir.setSSO(start.Token, okResult(v2, userID, `CO\e1001`))
	thief := h.visitor()
	if got := thief.ssoPoll(start.Token); got["status"] != "mismatch" || thief.signedInAs() != "" {
		t.Fatalf("poll from another browser = %v, signed in as %q", got, thief.signedInAs())
	}

	var reasons []string
	for _, e := range auditEvents(h, "auth.login_failed") {
		reasons = append(reasons, detailOf(e)["reason"].(string))
	}
	if !contains(reasons, "address_mismatch") || !contains(reasons, "browser_address_changed") {
		t.Errorf("failed sign-ins are audited with reasons %v", reasons)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestSSOWithoutTheAddressCheck(t *testing.T) {
	h := ssoHarness(t, func(o *server.Options) { o.SSORequireSameIP = false })
	v := h.visitor()
	_, start := v.ssoStart("/")
	h.dir.setSSO(start.Token, auth.SSOResult{Status: "ok", ECode: userID, WindowsUser: `CO\e1001`, IP: "unknown", At: time.Now()})
	if got := v.ssoPoll(start.Token); got["status"] != "ok" || v.signedInAs() != userID {
		t.Fatalf("poll = %v, signed in as %q; the check is off, so the address does not matter", got, v.signedInAs())
	}
}

func TestSSOFailuresNeverSignIn(t *testing.T) {
	h := ssoHarness(t, nil)
	h.dir.put(fakeAccount{ID: "E7007", Name: "Gone Person", Password: "x", Role: auth.RoleUser, Inactive: true})

	cases := []struct {
		name   string
		result func(v *harness) auth.SSOResult
		want   string
		reason string
	}{
		{"no windows user", func(v *harness) auth.SSOResult { return auth.SSOResult{Status: "no_user", IP: v.addr} }, "no_user", "no_windows_user"},
		{"unknown employee", func(v *harness) auth.SSOResult { return okResult(v, "E0000", `CO\e0000`) }, "denied", "no_active_account"},
		{"deactivated account", func(v *harness) auth.SSOResult { return okResult(v, "E7007", `CO\e7007`) }, "denied", "no_active_account"},
		{"unreadable result", func(v *harness) auth.SSOResult { return auth.SSOResult{Status: "unreadable"} }, "denied", "result_unreadable"},
	}
	for _, c := range cases {
		v := h.visitor()
		_, start := v.ssoStart("/")
		h.dir.setSSO(start.Token, c.result(v))
		if got := v.ssoPoll(start.Token); got["status"] != c.want {
			t.Errorf("%s: poll = %v, want %s", c.name, got, c.want)
		}
		if who := v.signedInAs(); who != "" {
			t.Errorf("%s: signed in as %q", c.name, who)
		}
		if got := v.ssoPoll(start.Token); got["status"] != "expired" {
			t.Errorf("%s: the token is still usable: %v", c.name, got)
		}
	}
	var reasons []string
	for _, e := range auditEvents(h, "auth.login_failed") {
		d := detailOf(e)
		if d["method"] != "sso" {
			continue
		}
		reasons = append(reasons, d["reason"].(string))
	}
	for _, c := range cases {
		if !contains(reasons, c.reason) {
			t.Errorf("no audit record with the reason %q in %v", c.reason, reasons)
		}
	}
}

func TestSSOWaitsOutAnUnavailableDirectory(t *testing.T) {
	h := ssoHarness(t, nil)
	v := h.visitor()
	_, start := v.ssoStart("/")
	h.dir.setSSO(start.Token, okResult(v, userID, `CO\e1001`))

	h.dir.setDown(true)
	if got := v.ssoPoll(start.Token); got["status"] != "unavailable" {
		t.Fatalf("poll while the directory is down = %v, want unavailable", got)
	}
	h.dir.setDown(false)
	if got := v.ssoPoll(start.Token); got["status"] != "ok" || v.signedInAs() != userID {
		t.Fatalf("poll after the directory is back = %v; an outage must not use up the sign-in", got)
	}
}

func TestSSOTokensAreUnknownExpiredOrBounded(t *testing.T) {
	h := ssoHarness(t, nil)
	v := h.visitor()
	if got := v.ssoPoll("abcdefghijklmnopqrstuvwx"); got["status"] != "expired" {
		t.Errorf("a token this server never issued = %v, want expired", got)
	}
	if res, _ := v.postForm("/auth/sso/poll", url.Values{"token": {"abcdefghijklmnopqrstuvwx"}}, map[string]string{"Origin": "https://evil.example"}); res.StatusCode != http.StatusForbidden {
		t.Errorf("poll from a foreign site = %d, want 403", res.StatusCode)
	}

	// The sign-on service keeps its result for five minutes, and so does the server keep the sign-in.
	_, start := v.ssoStart("/")
	h.dir.setSSO(start.Token, okResult(v, userID, `CO\e1001`))
	h.clock.Advance(6 * time.Minute)
	if got := v.ssoPoll(start.Token); got["status"] != "expired" || v.signedInAs() != "" {
		t.Errorf("poll after six minutes = %v", got)
	}

	// An anonymous browser cannot make the server remember an unlimited number of sign-ins.
	busy := h.visitor()
	for i := 0; i < 5; i++ {
		if status, _ := busy.ssoStart("/"); status != http.StatusOK {
			t.Fatalf("start %d = %d", i, status)
		}
	}
	if status, _ := busy.ssoStart("/"); status != http.StatusTooManyRequests {
		t.Errorf("sixth waiting sign-in of one address = %d, want 429", status)
	}
	if status, _ := h.visitor().ssoStart("/"); status != http.StatusOK {
		t.Errorf("another address is refused because of the first: %d", status)
	}
}

func TestSSOLooksAtTheSignOnResultOncePerSecondAtMost(t *testing.T) {
	h := ssoHarness(t, nil)
	v := h.visitor()
	_, start := v.ssoStart("/")
	v.ssoPoll(start.Token) // advances the clock and asks once
	before := h.dir.looks()
	for i := 0; i < 5; i++ {
		res, body := v.postForm("/auth/sso/poll", url.Values{"token": {start.Token}})
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "pending") {
			t.Fatalf("hurried poll = %d %s", res.StatusCode, body)
		}
	}
	if after := h.dir.looks(); after != before {
		t.Errorf("the sign-on result was read %d more times within one second", after-before)
	}
}

// loginRemotes lists the addresses of the sign-ins of a user, newest first.
func loginRemotes(h *harness) []string {
	var out []string
	for _, e := range auditEvents(h, "auth.login") {
		if e["actor"] == userID {
			out = append(out, e["remote_addr"].(string))
		}
	}
	return out
}

func TestClientAddressBehindTrustedProxies(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.8.0.0/16"), netip.MustParsePrefix("192.168.7.9/32")}
	h := newHarnessOpts(t, nil, func(o *server.Options) { o.TrustedProxies = proxies })

	cases := []struct {
		name, peer, forwarded, want string
	}{
		{"a proxy forwards a client", "10.8.0.5", "203.0.113.7", "203.0.113.7"},
		{"the header carries a port", "10.8.0.5", "203.0.113.7:51234", "203.0.113.7"},
		{"the client invented the left of the header", "10.8.0.5", "198.51.100.1, 203.0.113.7", "203.0.113.7"},
		{"the nearest address that is not a proxy", "10.8.0.5", "203.0.113.7, 10.8.1.1, 192.168.7.9", "203.0.113.7"},
		{"an IPv6 client", "10.8.0.5", "[2001:db8::7]:5000", "2001:db8::7"},
		{"a hop that is no address", "10.8.0.5", "203.0.113.7, not-an-address", "10.8.0.5"},
		{"nothing forwarded", "10.8.0.5", "", "10.8.0.5"},
		{"a client that is no proxy cannot name itself", "198.51.100.77", "203.0.113.7", "198.51.100.77"},
	}
	for _, c := range cases {
		v := h.visitor()
		hdr := map[string]string{"X-Test-Client": c.peer}
		if c.forwarded != "" {
			hdr["X-Forwarded-For"] = c.forwarded
		}
		v.addr = ""
		if res, _ := v.postForm("/login", url.Values{"username": {userID}, "password": {h.dir.password(userID)}}, hdr); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s: sign-in = %d", c.name, res.StatusCode)
		}
		if got := loginRemotes(h); len(got) == 0 || got[0] != c.want {
			t.Errorf("%s: the sign-in is recorded from %v, want %s", c.name, got, c.want)
		}
	}

	// Without proxies the header is never believed.
	plain := newHarness(t)
	v := plain.visitor()
	if res, _ := v.postForm("/login", url.Values{"username": {userID}, "password": {plain.dir.password(userID)}},
		map[string]string{"X-Test-Client": "10.8.0.5", "X-Forwarded-For": "203.0.113.7"}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-in = %d", res.StatusCode)
	}
	if got := loginRemotes(plain); len(got) == 0 || got[0] != "10.8.0.5" {
		t.Errorf("without trusted proxies the sign-in is recorded from %v, want the connection's address", got)
	}
}

func TestSSOBehindAProxyUsesTheForwardedAddress(t *testing.T) {
	h := ssoHarness(t, func(o *server.Options) { o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.8.0.0/16")} })
	v := h.visitor() // every browser reaches the server through the proxy at v.addr, with its own real address
	hdr := map[string]string{"X-Forwarded-For": "203.0.113.50:40000"}
	res, body := v.postForm("/auth/sso/start", url.Values{"next": {"/"}}, hdr)
	var start ssoStart
	if res.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &start) != nil {
		t.Fatalf("start = %d %s", res.StatusCode, body)
	}
	h.dir.setSSO(start.Token, auth.SSOResult{Status: "ok", ECode: userID, WindowsUser: `CO\e1001`, IP: "203.0.113.50", At: time.Now()})
	h.clock.Advance(2 * time.Second)
	_, body = v.postForm("/auth/sso/poll", url.Values{"token": {start.Token}}, hdr)
	if !strings.Contains(body, `"ok"`) || v.signedInAs() != userID {
		t.Fatalf("poll = %s; the address behind the proxy is the browser's, so it matches", body)
	}

	// Another browser behind the same proxy has another address and cannot use this sign-in.
	_, body = v.postForm("/auth/sso/start", url.Values{"next": {"/"}}, hdr)
	_ = json.Unmarshal([]byte(body), &start)
	h.dir.setSSO(start.Token, auth.SSOResult{Status: "ok", ECode: adminID, WindowsUser: `CO\e2002`, IP: "198.51.100.8", At: time.Now()})
	h.clock.Advance(2 * time.Second)
	_, body = v.postForm("/auth/sso/poll", url.Values{"token": {start.Token}}, hdr)
	if !strings.Contains(body, "mismatch") {
		t.Fatalf("poll = %s, want mismatch", body)
	}
}
