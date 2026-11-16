package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"timevault/web"
)

type seenRequest struct {
	called            bool
	path, raw, search string
}

// baseProbe serves requests the way the application does: it records what it sees and answers with the Location the
// request asks for in X-Want-Location, so the tests can watch the base path being taken off and put on.
func baseProbe(s *Server) (http.Handler, *seenRequest) {
	seen := &seenRequest{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = seenRequest{called: true, path: r.URL.Path, raw: r.URL.RawPath, search: r.URL.RawQuery}
		if loc := r.Header.Get("X-Want-Location"); loc != "" {
			w.Header().Set("Location", loc)
		}
		if r.Header.Get("X-Implicit-Header") != "" {
			_, _ = w.Write([]byte("body"))
			return
		}
		w.WriteHeader(http.StatusSeeOther)
	})
	return s.withBase(inner), seen
}

func TestWithBaseTakesThePrefixOffARequest(t *testing.T) {
	h, seen := baseProbe(&Server{base: "/tv"})
	for _, c := range []struct{ target, path, raw, search string }{
		{"/tv/explorer?source=1", "/explorer", "", "source=1"},
		{"/tv/", "/", "", ""},
		{"/tv/a%2Fb", "/a/b", "/a%2Fb", ""},
		{"/tv/static/css/app.css", "/static/css/app.css", "", ""},
		// A proxy that removed the prefix already: the request is served as it is.
		{"/explorer?source=1", "/explorer", "", "source=1"},
		{"/", "/", "", ""},
		// Only a whole segment is the prefix.
		{"/tvx/explorer", "/tvx/explorer", "", ""},
		{"/tv-old/", "/tv-old/", "", ""},
	} {
		*seen = seenRequest{}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.target, nil))
		if !seen.called || seen.path != c.path || seen.raw != c.raw || seen.search != c.search {
			t.Errorf("%s: the application saw %+v, want path %q raw %q query %q", c.target, *seen, c.path, c.raw, c.search)
		}
	}
}

func TestWithBaseSendsTheBarePrefixToTheOneWithASlash(t *testing.T) {
	h, seen := baseProbe(&Server{base: "/tv"})
	for _, c := range []struct {
		method, target string
		status         int
		location       string
	}{
		{http.MethodGet, "/tv", http.StatusMovedPermanently, "/tv/"},
		{http.MethodHead, "/tv", http.StatusMovedPermanently, "/tv/"},
		{http.MethodGet, "/tv?source=1&x=%2F", http.StatusMovedPermanently, "/tv/?source=1&x=%2F"},
		// A form post keeps its method.
		{http.MethodPost, "/tv", http.StatusPermanentRedirect, "/tv/"},
	} {
		*seen = seenRequest{}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, nil))
		if rec.Code != c.status || rec.Header().Get("Location") != c.location || seen.called {
			t.Errorf("%s %s = %d %q (application called: %v), want %d %q without the application", c.method, c.target, rec.Code,
				rec.Header().Get("Location"), seen.called, c.status, c.location)
		}
	}
}

func TestWithBasePutsThePrefixOnTheLocationsOfTheApplication(t *testing.T) {
	h, _ := baseProbe(&Server{base: "/tv"})
	for _, c := range []struct{ asked, header, want string }{
		{"/tv/x", "/explorer", "/tv/explorer"},
		{"/x", "/explorer?source=1&path=a%20b", "/tv/explorer?source=1&path=a%20b"},
		{"/tv/x", "/", "/tv/"},
		{"/tv/x", "/login?next=%2Fbackups", "/tv/login?next=%2Fbackups"},
		{"/tv/x", "/api/v1/jobs/7", "/tv/api/v1/jobs/7"},
		// An address that names a host, one that has none and is not a path, and one the browser would read as a host.
		{"/tv/x", "https://sso.example.test/login", "https://sso.example.test/login"},
		{"/tv/x", "next/page", "next/page"},
		{"/tv/x", "//evil.example/", "//evil.example/"},
		{"/tv/x", "", ""},
	} {
		for _, implicit := range []bool{false, true} {
			req := httptest.NewRequest(http.MethodGet, c.asked, nil)
			req.Header.Set("X-Want-Location", c.header)
			if implicit {
				req.Header.Set("X-Implicit-Header", "1")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Location"); got != c.want {
				t.Errorf("%s answering %q (implicit header: %v): Location = %q, want %q", c.asked, c.header, implicit, got, c.want)
			}
		}
	}

	// Without a base path nothing is changed, and the handler is not even wrapped.
	plain, _ := baseProbe(&Server{})
	req := httptest.NewRequest(http.MethodGet, "/explorer", nil)
	req.Header.Set("X-Want-Location", "/explorer")
	rec := httptest.NewRecorder()
	plain.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); got != "/explorer" {
		t.Errorf("without a base path Location = %q, want /explorer", got)
	}
}

// The ServeMux redirects on its own (a folder without its slash, an address that is not clean), and the base path has
// to be on those redirects too.
func TestWithBasePutsThePrefixOnTheRedirectsOfTheServeMux(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.NotFoundHandler()))
	h := (&Server{base: "/tv"}).withBase(mux)
	for target, want := range map[string]string{"/tv/static": "/tv/static/", "/static": "/tv/static/", "/tv//static/x": "/tv/static/x"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if got := rec.Header().Get("Location"); got != want || rec.Code/100 != 3 {
			t.Errorf("%s = %d %q, want a redirect to %q", target, rec.Code, got, want)
		}
	}
}

func TestAddressesInsideTheApplicationAndForTheBrowser(t *testing.T) {
	s := &Server{base: "/tv"}
	if got := s.u("/explorer"); got != "/tv/explorer" {
		t.Errorf("u = %q", got)
	}
	if got := s.href("/explorer", "source", 3, "path", "a b", "empty", ""); got != "/tv/explorer?path=a+b&source=3" {
		t.Errorf("href = %q", got)
	}
	if got := s.cookiePath(); got != "/tv" {
		t.Errorf("cookiePath = %q", got)
	}
	plain := &Server{}
	if plain.u("/explorer") != "/explorer" || plain.href("/") != "/" || plain.cookiePath() != "/" {
		t.Errorf("without a base path the addresses are the application's own: %q %q %q", plain.u("/explorer"), plain.href("/"), plain.cookiePath())
	}
}

func TestLocalTakesTheBasePathOffAnAddressTheBrowserSentBack(t *testing.T) {
	s := &Server{base: "/tv"}
	for in, want := range map[string]string{
		"/history?source=1":    "/history?source=1",
		"/tv/history?source=1": "/history?source=1",
		"/tv":                  "/",
		"/tv/":                 "/",
		"/tv?x=1":              "/?x=1",
		"/tvx/history":         "/tvx/history",
		"/tv-old/":             "/tv-old/",
		"":                     "",
	} {
		if got := s.local(in); got != want {
			t.Errorf("local(%q) = %q, want %q", in, got, want)
		}
	}
	if got := (&Server{}).local("/tv/history"); got != "/tv/history" {
		t.Errorf("without a base path local changes %q", got)
	}
}

func TestSafeNextWithABasePath(t *testing.T) {
	s := &Server{base: "/tv"}
	for in, want := range map[string]string{
		"/explorer?source=1":    "/explorer?source=1",
		"/tv/explorer?source=1": "/explorer?source=1",
		"/tv":                   "/",
		"/tv/":                  "/",
		"/tv?x=1":               "/?x=1",
		"/tvx/y":                "/tvx/y",
		"":                      "/",
		"https://evil.example/": "/",
		"//evil.example":        "/",
		// Taking the prefix off must not leave an address the browser reads as another host.
		"/tv//evil.example":                         "/",
		"/tv/\\evil.example":                        "/",
		"/tv/\t/evil.example":                       "/",
		"/tv/%2F/evil.example":                      "/%2F/evil.example",
		"/tv/./\\evil.example":                      "/",
		"/tv/" + strings.Repeat("a", maxNextLength): "/",
	} {
		if got := s.safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
	if got := (&Server{}).safeNext("/tv/explorer"); got != "/tv/explorer" {
		t.Errorf("without a base path safeNext changes %q", got)
	}
}

// A page that is written with an address that starts with a slash goes to the root of the host, outside the base
// path, and nothing would tell until the site is run behind a prefix. These checks keep the places that make addresses
// honest: the templates use the url function, the cookies take their path from the server.
func TestNoTemplateWritesAnAddressThatIgnoresTheBasePath(t *testing.T) {
	rooted := regexp.MustCompile(`(?:href|src|action|formaction|data-[a-z-]+)="/`)
	count := 0
	err := fs.WalkDir(web.FS, "templates", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(web.FS, name)
		if err != nil {
			return err
		}
		count++
		for i, line := range strings.Split(string(b), "\n") {
			if rooted.MatchString(line) {
				t.Errorf("%s:%d has an address that starts with a slash; write it with {{url ...}} so the base path is put on: %s", name, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil || count < 10 {
		t.Fatalf("walked %d templates: %v", count, err)
	}
}

func TestNoCookieIsLimitedToTheWholeSite(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `Path: "/"`) {
			t.Errorf("%s sets a cookie for the whole host; use s.cookiePath() so the cookie stays under the base path", f.Name())
		}
	}
}
