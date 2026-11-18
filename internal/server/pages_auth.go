package server

import (
	"errors"
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
	// SSO is true when the page offers the single sign-on button.
	SSO bool
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.auth.Session(r); ok {
		http.Redirect(w, r, s.safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	p := s.newPage(r, auth.Session{}, "login.title", "")
	p.Bare = true
	p.Data = loginData{Next: s.safeNext(r.URL.Query().Get("next")), SSO: s.ssoEnabled()}
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
	ip := s.clientIP(r)
	login := strings.TrimSpace(r.PostFormValue("username"))
	next := s.safeNext(r.PostFormValue("next"))

	again := func(status int, key string) {
		p := s.newPage(r, auth.Session{}, "login.title", "")
		p.Bare = true
		p.Data = loginData{Next: next, Username: login, Error: p.T(key), SSO: s.ssoEnabled()}
		p.RequestURI = buildURL("/login", "next", next)
		s.render(w, r, "login", status, p)
	}
	// A person may type a password into the name field by mistake, so the audit trail keeps only the start of it.
	typed := truncate(login, 64)
	failed := func(reason string, extra map[string]any) {
		detail := map[string]any{"reason": reason}
		for k, v := range extra {
			detail[k] = v
		}
		s.svc.Audit(r.Context(), audit.Event{Actor: typed, Action: audit.ActionLoginFailed, Result: audit.ResultDenied, RemoteAddr: ip, Detail: detail})
	}

	attempt, throttle := s.auth.Begin(ip, login)
	if throttle != nil {
		reason := "throttled"
		if throttle.ByAccount {
			reason = "account_throttled"
		}
		failed(reason, map[string]any{"retry_after_s": int(throttle.Wait.Seconds())})
		w.Header().Set("Retry-After", strconv.Itoa(int(throttle.Wait.Seconds())+1))
		again(http.StatusTooManyRequests, "login.throttled")
		return
	}
	id, err := s.auth.Login(r.Context(), login, r.PostFormValue("password"))
	switch {
	case errors.Is(err, auth.ErrUnavailable):
		// Not the account's fault, so it does not count against it.
		attempt.Unavailable()
		s.log.Warn("sign-in failed: the user directory cannot be reached", "error", err)
		failed("directory_unavailable", nil)
		again(http.StatusServiceUnavailable, "login.unavailable")
		return
	case err != nil:
		failed("invalid", nil)
		again(http.StatusUnauthorized, "login.invalid")
		return
	}
	attempt.Succeeded()
	sess, err := s.auth.StartSession(w, id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	s.svc.Audit(r.Context(), audit.Event{Actor: sess.User, Action: audit.ActionLogin, RemoteAddr: ip, Detail: map[string]any{"role": string(sess.Role), "method": "password"}})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.auth.ClearSession(w)
	s.svc.Audit(r.Context(), audit.Event{Actor: sess.User, Action: audit.ActionLogout, RemoteAddr: s.clientIP(r)})
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
		http.SetCookie(w, &http.Cookie{Name: cookieLang, Value: lang, Path: s.cookiePath(), MaxAge: 365 * 24 * 3600,
			Secure: s.opts.CookieSecure, SameSite: http.SameSiteLaxMode})
	}
	http.Redirect(w, r, s.safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	sources, err := s.visibleSources(r.Context(), sess)
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
