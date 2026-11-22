package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"timevault/internal/server"
)

const tvBase = "/tv"

// prefixedHarness serves the site under /tv, and its browsers ask for /tv/... as they do behind a reverse proxy that
// leaves the prefix on; stripped() is the same browser behind a proxy that takes the prefix off before the request
// reaches the application.
func prefixedHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarnessOpts(t, nil, func(o *server.Options) { o.BasePath = tvBase })
	h.prefix = tvBase
	return h
}

func (h *harness) stripped() *harness {
	v := *h
	v.strip = true
	return &v
}

// pageAddress finds the addresses that start with a slash in the links, forms, assets and script settings of a page.
var pageAddress = regexp.MustCompile(`\s(?:href|src|action|formaction|data-start|data-poll)="(/[^"]*)"`)

// assertUnderBase fails for every address of a page that would lead out of the base path.
func assertUnderBase(t *testing.T, what, body string) {
	t.Helper()
	all := pageAddress.FindAllStringSubmatch(body, -1)
	if len(all) < 5 {
		t.Errorf("%s: only %d addresses found, so the check proves nothing:\n%s", what, len(all), body)
	}
	for _, m := range all {
		if a := m[1]; a != tvBase && !strings.HasPrefix(a, tvBase+"/") {
			t.Errorf("%s has an address outside the base path: %q", what, a)
		}
	}
	if !strings.Contains(body, `<meta name="tv-base" content="`+tvBase+`">`) {
		t.Errorf("%s does not tell the script the base path", what)
	}
}

func location(res *http.Response) string { return res.Header.Get("Location") }

func cookieNamed(res *http.Response, name string) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestEveryPageCarriesTheBasePath(t *testing.T) {
	h := prefixedHarness(t)
	seed(h)
	listFolder(h) // 253 entries: the pager and the sort links appear
	point := h.env.Backup()
	src, pid := itoa(h.env.Src.ID), itoa(point.ID)

	admin := h.visitor()
	admin.loginAs(adminID)
	status, out := admin.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	job := itoa(num(out["job"].(map[string]any)["id"]))
	admin.waitJob(num(out["job"].(map[string]any)["id"]))

	pages := []string{
		"/explorer?source=" + src,
		"/explorer?source=" + src + "&path=many",
		"/explorer?source=" + src + "&path=many&page=2&per_page=50&sort=size&dir=desc",
		"/history?source=" + src + "&point=" + pid,
		"/history?source=" + src + "&point=" + pid + "&path=many&page=2&per_page=50",
		"/versions?source=" + src + "&path=a.txt",
		"/backups", "/jobs/" + job, "/audit", "/settings", "/status",
	}
	for _, page := range pages {
		for name, browser := range map[string]*harness{"proxy keeps the prefix": admin, "proxy removes it": admin.stripped()} {
			res, body := browser.get(page)
			if res.StatusCode != http.StatusOK {
				t.Errorf("%s %s = %d", name, page, res.StatusCode)
				continue
			}
			assertUnderBase(t, name+" "+page, body)
			assertCSPClean(t, page, body)
		}
	}

	// The pages that need a form post, an error page and the sign-in page.
	res, body := admin.postForm("/restore/preview", url.Values{"source_id": {src}, "point_id": {pid}, "paths": {"a.txt"}, "dest_name": {"base-path"}, "policy": {"skip"}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `action="/tv/restore/start"`) || !strings.Contains(body, `href="/tv/history?`) {
		t.Fatalf("restore preview = %d:\n%s", res.StatusCode, body)
	}
	assertUnderBase(t, "the restore preview", body)

	res, body = admin.get("/no-such-page")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown page = %d", res.StatusCode)
	}
	assertUnderBase(t, "the error page", body)

	anon := h.visitor()
	res, body = anon.get("/login?next=%2Ftv%2Fexplorer%3Fsource%3D" + src)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `action="/tv/login"`) {
		t.Fatalf("login page = %d:\n%s", res.StatusCode, body)
	}
	assertUnderBase(t, "the login page", body)
	// The address to return to is kept as the application knows it, whichever way it arrived.
	if want := `name="next" value="/explorer?source=` + src + `"`; !strings.Contains(body, want) {
		t.Errorf("the login form does not carry the page to return to as %s:\n%s", want, body)
	}
	if res, body = anon.get("/explorer?source=" + src); !strings.Contains(body, `href="/tv/login?next=%2Fexplorer%3Fsource%3D`+src+`"`) {
		t.Errorf("the sign-in button of an anonymous visitor does not lead back to the page under the base path:\n%s", body)
	}
}

// The files of the pages come from under the base path, with or without it on the request.
func TestAssetsAreServedUnderTheBasePathAndWithout(t *testing.T) {
	h := prefixedHarness(t)
	for _, path := range []string{"/static/css/app.css", "/static/js/app.js", "/static/js/theme-init.js", "/static/img/logo.svg", "/static/img/icons.svg"} {
		for name, browser := range map[string]*harness{"proxy keeps the prefix": h, "proxy removes it": h.stripped()} {
			if res, body := browser.get(path); res.StatusCode != http.StatusOK || len(body) < 100 {
				t.Errorf("%s %s = %d, %d bytes", name, path, res.StatusCode, len(body))
			}
		}
	}
	// The page asks for them under the base path.
	_, page := h.get("/login")
	for _, want := range []string{`href="/tv/static/css/app.css?v=`, `src="/tv/static/js/app.js?v=`, `src="/tv/static/js/theme-init.js?v=`, `href="/tv/static/img/logo.svg"`, `<use href="/tv/static/img/icons.svg#`} {
		if !strings.Contains(page, want) {
			t.Errorf("the login page lacks %s", want)
		}
	}
	// A folder without its slash is sent to the one with it, under the base path (the ServeMux answers with 307).
	if res, _ := h.get("/static"); res.StatusCode/100 != 3 || location(res) != "/tv/static/" {
		t.Errorf("/static = %d %q, want a redirect to /tv/static/", res.StatusCode, location(res))
	}
}

func TestRedirectsStayUnderTheBasePath(t *testing.T) {
	h := prefixedHarness(t)
	seed(h)
	h.env.Backup()
	src := itoa(h.env.Src.ID)

	for name, browser := range map[string]*harness{"the proxy keeps the prefix": h, "the proxy removes the prefix": h.stripped()} {
		t.Run(name, func(t *testing.T) {
			b := browser.visitor()
			if res, _ := b.get("/"); res.StatusCode != http.StatusSeeOther || location(res) != "/tv/explorer" {
				t.Errorf("/ = %d %q, want 303 /tv/explorer", res.StatusCode, location(res))
			}
			// What needs an account leads to the sign-in form, and back again.
			if res, _ := b.get("/audit"); res.StatusCode != http.StatusSeeOther || location(res) != "/tv/login?next=%2Faudit" {
				t.Errorf("anonymous /audit = %d %q, want 303 /tv/login?next=%%2Faudit", res.StatusCode, location(res))
			}
			if res, _ := b.postForm("/backups/run", url.Values{"source_id": {src}}); res.StatusCode != http.StatusSeeOther || location(res) != "/tv/login" {
				t.Errorf("anonymous POST /backups/run = %d %q, want 303 /tv/login", res.StatusCode, location(res))
			}

			// The language links: the address they return to may come with or without the prefix.
			for _, next := range []string{"%2Fexplorer%3Fsource%3D" + src, "%2Ftv%2Fexplorer%3Fsource%3D" + src} {
				res, _ := b.get("/lang?set=zh&next=" + next)
				if res.StatusCode != http.StatusSeeOther || location(res) != "/tv/explorer?source="+src {
					t.Errorf("/lang with next=%s = %d %q, want 303 /tv/explorer?source=%s", next, res.StatusCode, location(res), src)
				}
				if c := cookieNamed(res, "tv_lang"); c == nil || c.Path != tvBase {
					t.Errorf("the language cookie = %+v, want one limited to %s", c, tvBase)
				}
			}

			// The page to return to after signing in.
			for next, want := range map[string]string{
				"/history?source=" + src:        "/tv/history?source=" + src,
				"/tv/history?source=" + src:     "/tv/history?source=" + src,
				"/tv":                           "/tv/",
				"/":                             "/tv/",
				"":                              "/tv/",
				"/tv//evil.example":             "/tv/",
				"//evil.example":                "/tv/",
				"https://evil.example/":         "/tv/",
				"/tv/\\evil.example":            "/tv/",
				"/explorer?source=1&path=a%2Fb": "/tv/explorer?source=1&path=a%2Fb",
			} {
				v := browser.visitor()
				res, _ := v.postForm("/login", url.Values{"username": {"admin"}, "password": {password}, "next": {next}})
				if res.StatusCode != http.StatusSeeOther || location(res) != want {
					t.Errorf("sign-in with next=%q = %d %q, want 303 %q", next, res.StatusCode, location(res), want)
				}
				if c := cookieNamed(res, "tv_session"); c == nil || c.Path != tvBase || !c.HttpOnly {
					t.Errorf("the session cookie = %+v, want an HttpOnly one limited to %s", c, tvBase)
				}
			}

			// Signed in: forms, the flash message and signing out.
			admin := browser.visitor()
			admin.login()
			if res, _ := admin.get("/login"); res.StatusCode != http.StatusSeeOther || location(res) != "/tv/" {
				t.Errorf("/login while signed in = %d %q, want 303 /tv/", res.StatusCode, location(res))
			}
			res, _ := admin.postForm("/backups/run", url.Values{"source_id": {src}})
			if !regexp.MustCompile(`^/tv/jobs/\d+$`).MatchString(location(res)) || res.StatusCode != http.StatusSeeOther {
				t.Errorf("starting a backup = %d %q, want 303 /tv/jobs/<id>", res.StatusCode, location(res))
			}
			res, _ = admin.postForm("/settings/sources", url.Values{"name": {"Second " + name}, "root_path": {t.TempDir()}})
			if res.StatusCode != http.StatusSeeOther || location(res) != "/tv/settings" {
				t.Errorf("adding a folder = %d %q, want 303 /tv/settings", res.StatusCode, location(res))
			}
			if c := cookieNamed(res, "tv_flash"); c == nil || c.Path != tvBase {
				t.Errorf("the flash cookie = %+v, want one limited to %s", c, tvBase)
			}
			res, _ = admin.postForm("/logout", nil)
			if res.StatusCode != http.StatusSeeOther || location(res) != "/tv/login" {
				t.Errorf("sign out = %d %q, want 303 /tv/login", res.StatusCode, location(res))
			}
			if c := cookieNamed(res, "tv_session"); c == nil || c.Path != tvBase || c.MaxAge >= 0 {
				t.Errorf("the cookie of the ended session = %+v, want the same path and an end of life", c)
			}
		})
	}
}

// The bare prefix has no page of its own: it leads to the one with a slash, whichever way the proxy passes it on.
func TestTheBarePrefixLeadsToThePageWithASlash(t *testing.T) {
	h := prefixedHarness(t)
	for _, c := range []struct {
		method, suffix string
		status         int
		want           string
	}{
		{http.MethodGet, "", http.StatusMovedPermanently, "/tv/"},
		{http.MethodGet, "?source=1", http.StatusMovedPermanently, "/tv/?source=1"},
		{http.MethodPost, "", http.StatusPermanentRedirect, "/tv/"},
	} {
		res, _ := h.do(c.method, c.suffix, nil, nil) // h.prefix + "" is "/tv"
		if res.StatusCode != c.status || location(res) != c.want {
			t.Errorf("%s /tv%s = %d %q, want %d %q", c.method, c.suffix, res.StatusCode, location(res), c.status, c.want)
		}
	}
}

// The API lives under the base path too, and says so in the Location headers of what it creates.
func TestTheAPIAnswersUnderTheBasePath(t *testing.T) {
	h := prefixedHarness(t)
	seed(h)
	admin := h.visitor()
	admin.loginAs(adminID)

	post := func(path string, payload map[string]any) *http.Response {
		b, _ := json.Marshal(payload)
		res, _ := admin.do(http.MethodPost, path, strings.NewReader(string(b)), map[string]string{"X-CSRF-Token": admin.csrf, "Content-Type": "application/json"})
		return res
	}
	res := post("/api/v1/sources", map[string]any{"name": "Through the API", "root_path": t.TempDir()})
	if res.StatusCode != http.StatusCreated || location(res) != "/tv/api/v1/sources" {
		t.Errorf("POST /api/v1/sources = %d %q, want 201 /tv/api/v1/sources", res.StatusCode, location(res))
	}
	res = post("/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if res.StatusCode != http.StatusAccepted || !regexp.MustCompile(`^/tv/api/v1/jobs/\d+$`).MatchString(location(res)) {
		t.Errorf("POST /api/v1/backups = %d %q, want 202 /tv/api/v1/jobs/<id>", res.StatusCode, location(res))
	}
	// The path that the Location names can be followed, also through a proxy that takes the prefix off.
	if st, out := admin.stripped().api("GET", strings.TrimPrefix(location(res), tvBase), nil); st != http.StatusOK || out["job"] == nil {
		t.Errorf("the job at %s = %d %v", location(res), st, out)
	}

	// An unknown path and an unsigned request are answered as JSON, not sent to the sign-in page.
	if st, out := h.visitor().api("GET", "/api/v1/audit", nil); st != http.StatusUnauthorized || out["error"] == nil {
		t.Errorf("anonymous audit through the API = %d %v", st, out)
	}

	// The running configuration names the prefix in the API, for the local administrator; an administrator of the user
	// directory does not get it.
	if _, out := admin.api("GET", "/api/v1/settings", nil); out["base_path"] != nil {
		t.Errorf("an administrator of the user directory gets base_path %v", out["base_path"])
	}
	local := h.visitor()
	local.login()
	if _, out := local.api("GET", "/api/v1/settings", nil); out["base_path"] != tvBase {
		t.Errorf("settings base_path = %v, want %s", out["base_path"], tvBase)
	}
}

// Back from a file's versions to the folder, whichever way the address of the folder was written.
func TestTheBackLinkOfAFilePageKeepsTheBasePath(t *testing.T) {
	h := prefixedHarness(t)
	seed(h)
	point := h.env.Backup()
	src, pid := itoa(h.env.Src.ID), itoa(point.ID)
	b := h.visitor()
	b.loginAs(userID)

	// The history page links a file with the address to come back to.
	_, page := b.get("/history?source=" + src + "&point=" + pid + "&path=docs")
	m := regexp.MustCompile(`href="(/tv/versions\?[^"]*)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the history page links no file:\n%s", page)
	}
	link := strings.ReplaceAll(m[1], "&amp;", "&")
	u, err := url.Parse(link)
	if err != nil || !strings.HasPrefix(u.Query().Get("back"), "/history?") {
		t.Fatalf("the link to the file = %q (back = %q); the address to come back to is the application's own", link, u.Query().Get("back"))
	}
	wantBack := `href="/tv` + strings.ReplaceAll(u.Query().Get("back"), "&", "&amp;") + `"`
	_, versions := b.get(strings.TrimPrefix(link, tvBase))
	if !strings.Contains(versions, wantBack) {
		t.Errorf("the versions page lacks %s:\n%s", wantBack, versions)
	}
	// The same with the base path written into it, as an address copied out of the browser.
	withBase := "/versions?source=" + src + "&path=docs/b.txt&back=" + url.QueryEscape("/tv/history?source="+src+"&point="+pid+"&path=docs")
	_, versions = b.get(withBase)
	if want := `href="/tv/history?path=docs&amp;point=` + pid + `&amp;source=` + src; !strings.Contains(versions, `href="/tv/history?`) || strings.Contains(versions, `/tv/tv/`) {
		t.Errorf("the back link with the base path written into it is wrong (wanted something like %s):\n%s", want, versions)
	}
	// Something that is not a page of the application is not followed: the Back button stays on the folder. (The language
	// links carry the address of the page, back parameter included, as text in their next parameter.)
	_, versions = b.get("/versions?source=" + src + "&path=docs/b.txt&back=" + url.QueryEscape("//evil.example/history"))
	if foreign := regexp.MustCompile(`href="(//|https?:)`); foreign.MatchString(versions) || !strings.Contains(versions, `href="/tv/explorer?path=docs&amp;source=`+src+`"`) {
		t.Errorf("the versions page follows a foreign address, or lost its own Back button:\n%s", versions)
	}
}

func TestSingleSignOnReturnsToThePageUnderTheBasePath(t *testing.T) {
	h := ssoHarness(t, func(o *server.Options) { o.BasePath = tvBase })
	h.prefix = tvBase
	v := h.visitor()

	_, page := v.get("/login?next=%2Fbackups")
	for _, want := range []string{`data-start="/tv/auth/sso/start"`, `data-poll="/tv/auth/sso/poll"`, `data-next="/backups"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the login page lacks %s:\n%s", want, page)
		}
	}
	assertUnderBase(t, "the login page with single sign-on", page)

	// The script sends the address of the page as the browser knows it, with the prefix.
	status, start := v.ssoStart("/tv/backups")
	if status != http.StatusOK {
		t.Fatalf("start = %d", status)
	}
	h.dir.setSSO(start.Token, okResult(v, userID, `CO\e1001`))
	got := v.ssoPoll(start.Token)
	if got["status"] != "ok" || got["next"] != "/tv/backups" {
		t.Fatalf("poll = %v, want ok and the page under the base path", got)
	}
	if who := v.signedInAs(); who != userID {
		t.Fatalf("signed in as %q, want %s", who, userID)
	}
	// The session cookie works for the pages of the application.
	if res, _ := v.get("/backups"); res.StatusCode != http.StatusOK {
		t.Fatalf("/backups = %d", res.StatusCode)
	}

	// An address that leaves the site is not followed, with the prefix written into it or not.
	v2 := h.visitor()
	_, start = v2.ssoStart("/tv//evil.example/")
	h.dir.setSSO(start.Token, okResult(v2, userID, `CO\e1001`))
	if got := v2.ssoPoll(start.Token); got["status"] != "ok" || got["next"] != "/tv/" {
		t.Fatalf("poll of a sign-in that asked for a foreign address = %v, want ok and /tv/", got)
	}
}
