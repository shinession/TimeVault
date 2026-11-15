package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"timevault/internal/audit"
	"timevault/internal/auth"
)

type loginData struct {
	Next     string
	Username string
	Error    string
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.auth.Session(r); ok {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	p := s.newPage(r, auth.Session{}, "login.title", "")
	p.Bare = true
	p.Data = loginData{Next: safeNext(r.URL.Query().Get("next"))}
	s.render(w, r, "login", http.StatusOK, p)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	if !sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, "errors.csrf")
		return
	}
	ip := clientIP(r)
	user := strings.TrimSpace(r.PostFormValue("username"))
	next := safeNext(r.PostFormValue("next"))

	again := func(status int, key string) {
		p := s.newPage(r, auth.Session{}, "login.title", "")
		p.Bare = true
		p.Data = loginData{Next: next, Username: user, Error: p.T(key)}
		p.RequestURI = buildURL("/login", "next", next)
		s.render(w, r, "login", status, p)
	}
	if wait, ok := s.auth.Allow(ip); !ok {
		s.svc.Audit(r.Context(), audit.Event{Actor: user, Action: audit.ActionLoginFailed, Result: audit.ResultDenied, RemoteAddr: ip,
			Detail: map[string]any{"reason": "throttled", "retry_after_s": int(wait.Seconds())}})
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		again(http.StatusTooManyRequests, "login.throttled")
		return
	}
	if !s.auth.Authenticate(user, r.PostFormValue("password")) {
		s.auth.Failed(ip)
		s.svc.Audit(r.Context(), audit.Event{Actor: user, Action: audit.ActionLoginFailed, Result: audit.ResultDenied, RemoteAddr: ip})
		again(http.StatusUnauthorized, "login.invalid")
		return
	}
	s.auth.Succeeded(ip)
	sess, err := s.auth.StartSession(w, user)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), audit.Event{Actor: sess.User, Action: audit.ActionLogin, RemoteAddr: ip})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.auth.ClearSession(w)
	s.svc.Audit(r.Context(), audit.Event{Actor: sess.User, Action: audit.ActionLogout, RemoteAddr: clientIP(r)})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// sameOrigin rejects a cross-site form post to the login endpoint, which has no session to carry a CSRF token.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("set")
	if lang == "en" || lang == "zh" {
		http.SetCookie(w, &http.Cookie{Name: cookieLang, Value: lang, Path: "/", MaxAge: 365 * 24 * 3600,
			Secure: s.opts.CookieSecure, SameSite: http.SameSiteLaxMode})
	}
	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	sources, err := s.svc.Sources(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	target := "/explorer"
	if len(sources) == 0 {
		target = "/backups"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
