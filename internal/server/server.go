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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
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
}

// Server serves the web UI and the JSON API.
type Server struct {
	svc      *service.Service
	auth     *auth.Manager
	opts     Options
	log      *slog.Logger
	pages    map[string]*template.Template
	assets   fs.FS
	assetVer string
	mux      *http.ServeMux
}

// New builds the server. webFS must contain the "templates" and "assets" folders.
func New(svc *service.Service, am *auth.Manager, webFS fs.FS, opts Options, log *slog.Logger) (*Server, error) {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{svc: svc, auth: am, opts: opts, log: log}
	var err error
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
	return http.MaxBytesHandler(h, maxFormBytes)
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
	m.HandleFunc("POST /logout", s.authed(s.handleLogout))

	m.HandleFunc("GET /{$}", s.authed(s.handleHome))
	m.HandleFunc("/", s.handleNotFound)
	m.HandleFunc("GET /explorer", s.authed(s.handleExplorer))
	m.HandleFunc("GET /history", s.authed(s.handleHistory))
	m.HandleFunc("GET /versions", s.authed(s.handleVersions))
	m.HandleFunc("GET /backups", s.authed(s.handleBackups))
	m.HandleFunc("POST /backups/sources", s.authed(s.handleAddSource))
	m.HandleFunc("POST /backups/run", s.authed(s.handleRunBackup))
	m.HandleFunc("POST /backups/verify", s.authed(s.handleVerify))
	m.HandleFunc("GET /jobs/{id}", s.authed(s.handleJob))
	m.HandleFunc("POST /jobs/{id}/cancel", s.authed(s.handleCancelJob))
	m.HandleFunc("POST /restore/preview", s.authed(s.handleRestorePreview))
	m.HandleFunc("POST /restore/start", s.authed(s.handleRestoreStart))
	m.HandleFunc("GET /audit", s.authed(s.handleAudit))
	m.HandleFunc("GET /status", s.authed(s.handleStatus))

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

type authedHandler func(w http.ResponseWriter, r *http.Request, sess auth.Session)

// authed requires a valid session and, for state-changing methods, a valid CSRF token.
func (s *Server) authed(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.auth.Session(r)
		if !ok {
			if isAPI(r) {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized", "sign in first")
				return
			}
			target := "/login"
			if r.Method == http.MethodGet && r.Header.Get(partialHdr) == "" {
				target += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		if err := s.auth.CheckCSRF(r, sess); err != nil {
			a := s.actor(r, sess)
			s.svc.Audit(r.Context(), audit.Event{Actor: a.User, Action: audit.ActionAccessDenied, Result: audit.ResultDenied,
				RemoteAddr: a.Remote, Detail: map[string]any{"reason": "csrf", "method": r.Method, "path": r.URL.Path}})
			if isAPI(r) {
				writeJSONError(w, http.StatusForbidden, "csrf", "missing or invalid CSRF token")
				return
			}
			s.fail(w, r, http.StatusForbidden, "errors.csrf")
			return
		}
		next(w, r.WithContext(auth.WithSession(r.Context(), sess)), sess)
	}
}

func isAPI(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") }

func (s *Server) actor(r *http.Request, sess auth.Session) service.Actor {
	return service.Actor{User: sess.User, Remote: clientIP(r)}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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

// safeNext returns next when it is a local path, otherwise "/".
//
// http.Redirect cleans a relative target (dropping "." elements), and browsers
// treat a backslash as a slash and silently drop tabs and newlines inside a URL,
// so "/./\evil.example" or "/<TAB>/evil.example" would end up as "//evil.example".
// Any backslash or control character is therefore refused.
func safeNext(next string) string {
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") {
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
	case errors.Is(err, pathutil.ErrUnsafePath):
		return http.StatusBadRequest, "errors.bad_path"
	case errors.Is(err, backup.ErrInvalidSource), errors.Is(err, restore.ErrInvalidRequest), errors.Is(err, search.ErrBadQuery):
		return http.StatusBadRequest, ""
	case errors.Is(err, catalog.ErrConflict), errors.Is(err, catalog.ErrBusy):
		return http.StatusConflict, ""
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

// publicMessage is the text of an error that is safe to show to a signed-in user.
func publicMessage(err error, key string) string {
	if key == "errors.internal" {
		return "internal error"
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
	p.Data = errorData{Status: status, Key: key, Detail: detail, Ref: ref}
	s.render(w, r, "error", status, p)
}
