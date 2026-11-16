package server

import (
	"net/http"
	"strings"
)

// The site can be served under a path prefix behind a reverse proxy (TIMEVAULT_BASE_PATH, for example /tv).
//
// Inside the application every address is relative to the application's own root, as if there were no prefix: the
// routes, the targets of the redirects and the paths in the templates are all written without it. The prefix is taken
// off a request on the way in (withBase), so a proxy may remove it or leave it, and it is put on every address the
// browser is given on the way out:
//
//   - the Location headers, by prefixWriter, whichever code wrote them;
//   - the links, forms and assets of the pages, by the url template function;
//   - the paths of the cookies, by cookiePath;
//   - the requests of the script, which reads the prefix from the tv-base meta tag.

// withBase is the outermost handler. A request whose path carries the base path loses it; a request for the base path
// alone is sent to the same address with a slash; every other request is served as it is, because the proxy may have
// removed the prefix already. Without a base path it does nothing.
func (s *Server) withBase(next http.Handler) http.Handler {
	if s.base == "" {
		return next
	}
	stripped := http.StripPrefix(s.base, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == s.base:
			target := s.base + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			code := http.StatusMovedPermanently
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				code = http.StatusPermanentRedirect
			}
			http.Redirect(w, r, target, code)
		case strings.HasPrefix(p, s.base+"/"):
			stripped.ServeHTTP(&prefixWriter{ResponseWriter: w, base: s.base}, r)
		default:
			next.ServeHTTP(&prefixWriter{ResponseWriter: w, base: s.base}, r)
		}
	})
}

// prefixWriter puts the base path in front of the Location header of a response when it names a path of the
// application, like the redirects of the pages, the Location headers of the API and the redirects of the ServeMux. An
// address with a host, and one that is relative to the page, are left alone.
type prefixWriter struct {
	http.ResponseWriter
	base string
	done bool
}

func (w *prefixWriter) WriteHeader(code int) {
	w.prefix()
	w.ResponseWriter.WriteHeader(code)
}

func (w *prefixWriter) Write(b []byte) (int, error) {
	w.prefix()
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection.
func (w *prefixWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *prefixWriter) prefix() {
	if w.done {
		return
	}
	w.done = true
	h := w.Header()
	if loc := h.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
		h.Set("Location", w.base+loc)
	}
}

// u is the address a browser uses for a path of the application.
func (s *Server) u(p string) string { return s.base + p }

// href is buildURL for the browser: the path with the base path in front, and the query string.
func (s *Server) href(p string, kv ...any) string { return s.base + buildURL(p, kv...) }

// cookiePath is the path the cookies of the application are limited to.
func (s *Server) cookiePath() string {
	if s.base == "" {
		return "/"
	}
	return s.base
}

// local turns an address that a browser may have sent back, such as the next or back parameter, into an address
// inside the application: a base path at its start is taken off. The script puts the address of the page, with the base
// path, into the links that lead back to it.
func (s *Server) local(p string) string {
	if s.base == "" {
		return p
	}
	switch {
	case p == s.base:
		return "/"
	case strings.HasPrefix(p, s.base+"/"):
		return p[len(s.base):]
	case strings.HasPrefix(p, s.base+"?"):
		return "/" + p[len(s.base):]
	}
	return p
}

// safeNext is safeNext for the address a sign-in or a language switch returns to, as an address inside the
// application. It checks the address again after the base path is off, because "/tv//host" would become "//host".
func (s *Server) safeNext(next string) string { return safeNext(s.local(safeNext(next))) }
