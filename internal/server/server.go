// Package server is the HTTP front end: server-rendered pages, a small JSON
// API, sign-in, CSRF protection and security headers.
package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"timevault/internal/access"
	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/config"
	"timevault/internal/pathutil"
	"timevault/internal/restore"
	"timevault/internal/search"
	"timevault/internal/service"
)

const (
	cookieLang   = "tv_lang"
	maxFormBytes = 1 << 20
	partialHdr   = "X-TV-Partial"
)

// Options configures the HTTP server.
type Options struct {
	Version      string
	Location     *time.Location
	CookieSecure bool
	// Deployment is shown, without any secret, on the Settings page.
	Deployment config.Deployment
	// SSOHost is the address of the single sign-on service as scheme://host[:port]; empty turns single sign-on off.
	SSOHost string
	// SSORequireSameIP makes a single sign-on succeed only when the browser that started it and the sign-on service
	// saw the same address.
	SSORequireSameIP bool
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header is believed when they connect.
	TrustedProxies []netip.Prefix
	// BasePath is the path prefix the site is served under behind a reverse proxy, such as /tv. Empty means the
	// site is at the root of its address. See basepath.go.
	BasePath string
	// Now is the clock of the single sign-on flows; nil means time.Now.
	Now func() time.Time
}

// Server serves the web UI and the JSON API.
type Server struct {
	svc      *service.Service
	auth     *auth.Manager
	opts     Options
	base     string
	log      *slog.Logger
	pages    map[string]*template.Template
	assets   fs.FS
	assetVer string
	mux      *http.ServeMux
	sso      ssoFlows
}

// New builds the server. webFS must contain the "templates" and "assets" folders.
func New(svc *service.Service, am *auth.Manager, webFS fs.FS, opts Options, log *slog.Logger) (*Server, error) {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	base, err := config.ParseBasePath(opts.BasePath)
	if err != nil {
		return nil, fmt.Errorf("server: base path %w", err)
	}
	opts.BasePath = base
	opts.Deployment.BasePath = base
	s := &Server{svc: svc, auth: am, opts: opts, base: base, log: log}
	if s.pages, err = parseTemplates(webFS, s.funcs()); err != nil {
		return nil, err
	}
	if s.assets, err = fs.Sub(webFS, "assets"); err != nil {
		return nil, err
	}
	if s.assetVer, err = assetVersion(s.assets); err != nil {
		return nil, err
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s, nil
}

// assetVersion fingerprints the embedded assets. Pages put it in the asset URLs,
// so a browser fetches them again whenever their content changes, even when the
// program version stays the same.
func assetVersion(assets fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(assets, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(h, name)
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("server: fingerprint assets: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// Handler returns the complete HTTP handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.securityHeaders(h)
	h = s.requestLog(h)
	h = s.recoverer(h)
	h = http.MaxBytesHandler(h, maxFormBytes)
	return s.withBase(h)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	m.Handle("GET /static/", http.StripPrefix("/static/", s.staticHandler()))
	m.HandleFunc("GET /lang", s.handleLang)
	m.HandleFunc("GET /login", s.handleLoginForm)
	m.HandleFunc("POST /login", s.handleLogin)
	m.HandleFunc("POST /auth/sso/start", s.handleSSOStart)
	m.HandleFunc("POST /auth/sso/poll", s.handleSSOPoll)
	m.HandleFunc("POST /logout", s.need(auth.PermAccount, s.handleLogout))

	// Anyone may look at the folders that let guests browse; a folder that does not is not there for them. What a
	// signed-in account may do with a folder is decided per folder (see access.go), by the handlers. These pages
	// decide what to show by who is asking, so an account that looks signed in is first checked with the user
	// directory (openChecked): a deactivated account or one that lost its role must not keep what only it could see.
	m.HandleFunc("GET /{$}", s.openChecked(s.handleHome))
	m.HandleFunc("/", s.handleNotFound)
	m.HandleFunc("GET /explorer", s.openChecked(s.handleExplorer))
	m.HandleFunc("GET /history", s.openChecked(s.handleHistory))
	m.HandleFunc("GET /versions", s.openChecked(s.handleVersions))
	m.HandleFunc("GET /backups", s.openChecked(s.handleBackups))
	m.HandleFunc("GET /jobs/{id}", s.openChecked(s.handleJob))

	// Everything that works on data needs an account; the folder-level check is made by the handler.
	m.HandleFunc("POST /backups/run", s.need(auth.PermAccount, s.handleRunBackup))
	m.HandleFunc("POST /backups/verify", s.need(auth.PermAccount, s.handleVerify))
	m.HandleFunc("POST /jobs/{id}/cancel", s.need(auth.PermAccount, s.handleCancelJob))
	m.HandleFunc("POST /restore/preview", s.need(auth.PermAccount, s.handleRestorePreview))
	m.HandleFunc("POST /restore/start", s.need(auth.PermAccount, s.handleRestoreStart))
	m.HandleFunc("GET /audit", s.need(auth.PermAudit, s.handleAudit))
	m.HandleFunc("GET /status", s.need(auth.PermAudit, s.handleStatus))
	m.HandleFunc("GET /settings", s.need(auth.PermAudit, s.handleSettings))
	m.HandleFunc("POST /settings/sources", s.need(auth.PermManage, s.handleAddSource))
	// Whoever has full control of a folder may change its settings; the handler checks that.
	m.HandleFunc("POST /settings/sources/{id}", s.need(auth.PermAccount, s.handleUpdateSource))

	s.apiRoutes()
}

// --- Middleware ----------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
			return
		}
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				id := newID()
				s.log.Error("panic while handling a request", "id", id, "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(p))
				s.fail(w, r, http.StatusInternalServerError, "errors.internal", id)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
			"base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

type sessionHandler func(w http.ResponseWriter, r *http.Request, sess auth.Session)

// open serves everyone, for what does not depend on who asks. The session is the visitor's when they are signed in
// and the zero Session otherwise. Only the cookie is read, so the answer is the cookie's and never depends on the
// user directory being reachable; it must not decide what a visitor may see or do.
func (s *Server) open(next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := s.auth.Session(r)
		next(w, r.WithContext(auth.WithSession(r.Context(), sess)), sess)
	}
}

// openChecked serves everyone like open, for the pages and endpoints that show more to an account than to a
// visitor: which folders there are (a folder may be for some accounts only), who requested a job, why it failed,
// the folders on the server's disk. A visitor who looks signed in is first checked with the user directory, so an
// account that was deactivated meanwhile, or that lost its role, sees what anyone sees. While the directory cannot
// be reached the session is trusted as issued (see auth.Manager.Verify), so browsing keeps working.
func (s *Server) openChecked(next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := s.signedIn(w, r)
		next(w, r.WithContext(auth.WithSession(r.Context(), sess)), sess)
	}
}

// signedIn returns the session of a request after the account has been
// re-checked against the user directory. A session whose account is gone or
// deactivated is ended on the spot.
func (s *Server) signedIn(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := s.auth.Session(r)
	if !ok {
		return auth.Session{}, false
	}
	sess, err := s.auth.Verify(r.Context(), sess)
	if err != nil {
		// Only a verdict about the account ends the session; a request that was abandoned says nothing.
		if errors.Is(err, auth.ErrInvalidCredentials) {
			s.auth.ClearSession(w)
		}
		return auth.Session{}, false
	}
	return sess, true
}

// need runs next only for a signed-in account that may do p, and, for
// state-changing methods, only with a valid CSRF token.
func (s *Server) need(p auth.Permission, next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.signedIn(w, r)
		if !ok {
			s.askToSignIn(w, r)
			return
		}
		if err := s.auth.CheckCSRF(r, sess); err != nil {
			s.denied(w, r, sess, "csrf", "errors.csrf", http.StatusForbidden, "csrf", "missing or invalid CSRF token")
			return
		}
		if !sess.Can(p) {
			s.denied(w, r, sess, "forbidden", "errors.forbidden", http.StatusForbidden, "forbidden", "you are not allowed to do this")
			return
		}
		next(w, r.WithContext(auth.WithSession(r.Context(), sess)), sess)
	}
}

// requireSignIn is for handlers that serve everyone but some of their
// content only signed-in accounts: it returns the verified session, or asks
// the visitor to sign in and reports false.
func (s *Server) requireSignIn(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := s.signedIn(w, r)
	if !ok {
		s.askToSignIn(w, r)
	}
	return sess, ok
}

// askToSignIn answers a request that needs an account but has none: the API
// gets a 401, a page leads to the sign-in form and back again.
func (s *Server) askToSignIn(w http.ResponseWriter, r *http.Request) {
	if isAPI(r) {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "sign in first")
		return
	}
	target := "/login"
	if r.Method == http.MethodGet && r.Header.Get(partialHdr) == "" {
		target += "?next=" + url.QueryEscape(r.URL.RequestURI())
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// denied audits and answers a request that was understood but refused.
func (s *Server) denied(w http.ResponseWriter, r *http.Request, sess auth.Session, reason, pageKey string, status int, apiCode, apiMessage string) {
	s.refused(w, r, sess, nil, map[string]any{"reason": reason}, pageKey, status, apiCode, apiMessage)
}

// refused is denied for a request that concerns a monitored folder: the audit record belongs to the folder, so
// people who cannot see the folder do not see the record either. The record names the route that matched, never
// the address as it was typed: that text is the visitor's, and it would be shown to others in the audit log.
func (s *Server) refused(w http.ResponseWriter, r *http.Request, sess auth.Session, source *int64, detail map[string]any, pageKey string, status int, apiCode, apiMessage string) {
	a := s.actor(r, sess)
	detail["method"], detail["path"] = r.Method, routeOf(r)
	s.svc.Audit(r.Context(), audit.Event{Actor: a.User, Action: audit.ActionAccessDenied, Result: audit.ResultDenied,
		SourceID: source, RemoteAddr: a.Remote, Detail: detail})
	if isAPI(r) {
		writeJSONError(w, status, apiCode, apiMessage)
		return
	}
	s.fail(w, r, status, pageKey)
}

func isAPI(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") }

var (
	routeWildcard = regexp.MustCompile(`\{[^{}]*\}`)
	routeNumber   = regexp.MustCompile(`^[0-9]{1,18}$`)
)

// routeOf names the route of a request: the pattern that matched, with the numbers it captured filled in
// (/jobs/12/cancel) and the other wildcards left as they are, so that no text a visitor typed gets into it.
func routeOf(r *http.Request) string {
	route := r.Pattern
	if _, rest, found := strings.Cut(route, " "); found {
		route = rest
	}
	if route == "" {
		return "(no route)"
	}
	return routeWildcard.ReplaceAllStringFunc(route, func(wildcard string) string {
		if v := r.PathValue(strings.Trim(wildcard, "{}.")); routeNumber.MatchString(v) {
			return v
		}
		return wildcard
	})
}

func (s *Server) actor(r *http.Request, sess auth.Session) service.Actor {
	return service.Actor{User: sess.User, Remote: s.clientIP(r)}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// staticHandler serves the embedded assets. A request that carries the content
// fingerprint (?v=) can be cached for good, because the URL changes with the
// content; anything else must be revalidated so an update shows up at once.
func (s *Server) staticHandler() http.Handler {
	files := http.FileServerFS(s.assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") == s.assetVer {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// --- Request helpers -----------------------------------------------------------

// truncate shortens s to at most n runes.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func intParam(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue(name)), 10, 64)
	return v
}

func intParamDefault(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	if err != nil || v < lo || v > hi {
		return def
	}
	return v
}

// maxNextLength is the longest address a sign-in returns to. No link or page makes a longer one, and the server keeps
// it in memory while a single sign-on waits.
const maxNextLength = 2048

// safeNext returns next when it is a local path, otherwise "/".
//
// http.Redirect cleans a relative target (dropping "." elements), and browsers
// treat a backslash as a slash and silently drop tabs and newlines inside a URL,
// so "/./\evil.example" or "/<TAB>/evil.example" would end up as "//evil.example".
// Any backslash or control character is therefore refused.
func safeNext(next string) string {
	if next == "" || len(next) > maxNextLength || next[0] != '/' || strings.HasPrefix(next, "//") {
		return "/"
	}
	for _, r := range next {
		if r == '\\' || r < 0x20 || r == 0x7f {
			return "/"
		}
	}
	return next
}

// status maps an error to an HTTP status and a message key.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrNotFound), errors.Is(err, catalog.ErrNotFound):
		return http.StatusNotFound, "errors.not_found"
	case errors.Is(err, service.ErrForbidden):
		return http.StatusForbidden, "errors.forbidden"
	case errors.Is(err, errLockout):
		return http.StatusConflict, "errors.lockout_not_confirmed"
	case errors.Is(err, pathutil.ErrUnsafePath):
		return http.StatusBadRequest, "errors.bad_path"
	case errors.Is(err, backup.ErrInvalidSource), errors.Is(err, access.ErrInvalid), errors.Is(err, restore.ErrInvalidRequest), errors.Is(err, search.ErrBadQuery):
		return http.StatusBadRequest, ""
	case errors.Is(err, catalog.ErrConflict):
		return http.StatusConflict, ""
	case errors.Is(err, catalog.ErrBusy):
		return http.StatusConflict, "errors.source_busy"
	case errors.Is(err, restore.ErrDisabled):
		return http.StatusConflict, "errors.restore_disabled"
	case errors.Is(err, restore.ErrNotRestorable):
		return http.StatusConflict, "errors.not_restorable"
	case errors.Is(err, restore.ErrStalePreview):
		return http.StatusConflict, "errors.stale_preview"
	case errors.Is(err, restore.ErrNotConfirmed):
		return http.StatusUnprocessableEntity, "errors.not_confirmed"
	case errors.Is(err, restore.ErrBlocked):
		return http.StatusUnprocessableEntity, "errors.blocked"
	case errors.Is(err, service.ErrSourceUnavailable):
		return http.StatusServiceUnavailable, "errors.source_unavailable"
	case errors.Is(err, service.ErrSourceDisabled):
		return http.StatusConflict, "errors.source_disabled"
	case errors.Is(err, service.ErrPruneRunning):
		return http.StatusConflict, "errors.prune_running"
	}
	var unconfirmed *service.PruneNotConfirmedError
	if errors.As(err, &unconfirmed) {
		return http.StatusConflict, "errors.prune_not_confirmed"
	}
	var changed *accessChangedError
	if errors.As(err, &changed) {
		return http.StatusConflict, "errors.access_changed"
	}
	return http.StatusInternalServerError, "errors.internal"
}

// handleErr answers a failed request: a JSON error for the API, an error page otherwise.
func (s *Server) handleErr(w http.ResponseWriter, r *http.Request, err error) {
	status, key := classify(err)
	if status == http.StatusInternalServerError {
		id := newID()
		s.log.Error("request failed", "id", id, "method", r.Method, "path", r.URL.Path, "error", err)
		if isAPI(r) {
			writeJSONError(w, status, "internal", "internal error, reference "+id)
			return
		}
		s.fail(w, r, status, key, id)
		return
	}
	msg := err.Error()
	if key != "" && !isAPI(r) {
		msg = ""
	}
	if isAPI(r) {
		writeJSONError(w, status, strings.TrimPrefix(key, "errors."), publicMessage(err, key))
		return
	}
	s.failWithDetail(w, r, status, key, msg)
}

// publicMessage is the text of an error that is safe to show to a visitor.
func publicMessage(err error, key string) string {
	switch {
	case key == "errors.internal":
		return "internal error"
	case errors.Is(err, service.ErrSourceUnavailable):
		// The error text names the folder and the system's reason; the API is open to everyone.
		return "the monitored folder cannot be read right now"
	}
	return err.Error()
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, key string, ref ...string) {
	id := ""
	if len(ref) > 0 {
		id = ref[0]
	}
	s.failPage(w, r, status, key, "", id)
}

// handleNotFound answers every path no route matches.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if isAPI(r) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such endpoint")
		return
	}
	s.fail(w, r, http.StatusNotFound, "errors.not_found")
}

type errorData struct {
	Status int
	Key    string
	Detail string
	Ref    string
}

func (s *Server) failWithDetail(w http.ResponseWriter, r *http.Request, status int, key, detail string) {
	s.failPage(w, r, status, key, detail, "")
}

func (s *Server) failPage(w http.ResponseWriter, r *http.Request, status int, key, detail, ref string) {
	if key == "" {
		key = "errors.generic"
	}
	sess, _ := s.auth.Session(r)
	p := s.newPage(r, sess, "errors.title", "")
	// A folder somebody may not see is answered like one that does not exist; a visitor who has not signed in is
	// told that signing in may show it.
	if status == http.StatusNotFound && !sess.SignedIn() && detail == "" {
		detail = p.T("errors.sign_in_hint")
	}
	p.Data = errorData{Status: status, Key: key, Detail: detail, Ref: ref}
	s.render(w, r, "error", status, p)
}
