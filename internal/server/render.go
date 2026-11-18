package server

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"timevault/internal/access"
	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/catalog"
	"timevault/internal/pathutil"
	"timevault/internal/service"
)

// Flash is a one-time message shown after a redirect.
type Flash struct {
	Kind    string // ok | error | info
	Message string
}

// Page is the data every template receives.
type Page struct {
	Title string
	Nav   string
	Lang  string
	// User is the sign-in identity and empty for a visitor; Name is what the header shows.
	User         string
	Name         string
	CSRF         string
	Version      string
	AssetVer     string
	Flash        *Flash
	Data         any
	Bare         bool
	AuthDisabled bool
	// RequestURI is where the language links return to; it must be reachable with a GET.
	RequestURI string
	// Location is the folder or file the page shows, the way people reach it over the network (its display folder).
	Location string
	// LocationReal is the same place on the server's own disk; only administrators get it.
	LocationReal string
	// Notice is a sentence for the whole site, shown under the header: the service is about to restart for a new
	// version. Empty when there is none, and for visitors who are not signed in.
	Notice string
	// AvatarURL is where the pictures of the accounts are (see Options.AvatarURL); empty when there are none.
	AvatarURL string

	loc    *Locale
	tz     *time.Location
	sess   auth.Session
	avatar func(name string) string
}

// SignedIn reports whether the visitor has signed in. The Can* methods tell
// the templates which controls to offer; the server enforces the same rules.
// What a visitor may do with one folder (back it up, change its settings) is not here: the pages that show a
// folder carry that level themselves, see access.go.
func (p *Page) SignedIn() bool { return p.sess.SignedIn() }

// CanRestore reports whether the visitor may restore from the folders they can browse: every signed-in account may.
func (p *Page) CanRestore() bool { return p.sess.SignedIn() }
func (p *Page) CanAudit() bool   { return p.sess.Can(auth.PermAudit) }
func (p *Page) CanManage() bool  { return p.sess.Can(auth.PermManage) }

// ShowPhysical reports whether the visitor may see the folders of the server's own disk. Everybody else sees the
// display folders, the paths people use over the network.
func (p *Page) ShowPhysical() bool { return p.sess.Can(auth.PermManage) }

// T translates a message key; extra arguments are formatted into it.
func (p *Page) T(key string, args ...any) string { return p.loc.T(key, args...) }

// IsZh reports whether the interface language is Chinese.
func (p *Page) IsZh() bool { return p.Lang == "zh" }

// Time formats a time in the server's time zone, to the minute.
func (p *Page) Time(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(p.tz).Format("2006-01-02 15:04")
}

// TimeS formats a time in the server's time zone, to the second.
func (p *Page) TimeS(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(p.tz).Format("2006-01-02 15:04:05")
}

// TimeP is Time for an optional value.
func (p *Page) TimeP(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return p.Time(*t)
}

// TimeSP is TimeS for an optional value.
func (p *Page) TimeSP(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return p.TimeS(*t)
}

func (p *Page) hasFlash() bool { return p.Flash != nil }

// AuditDuration formats how long the operation of an audit event ran, "-" when it has no duration.
func (p *Page) AuditDuration(e audit.Event) string {
	if !e.HasDuration() {
		return "-"
	}
	return formatDuration(e.Duration)
}

// DurationMS formats a length of time given in milliseconds, "-" when it is not known.
func (p *Page) DurationMS(ms *int64) string {
	if ms == nil {
		return "-"
	}
	return formatDuration(time.Duration(*ms) * time.Millisecond)
}

// AuditSpeed is how fast a backup or restore moved data: the bytes it wrote over the time it ran, for example
// "41.2 MB/s". It is empty when the event has no duration or no data to speak of.
func (p *Page) AuditSpeed(e audit.Event) string {
	var key string
	switch e.Action {
	case audit.ActionBackupFinish:
		key = "new_bytes"
	case audit.ActionRestoreFinish:
		key = "bytes"
	default:
		return ""
	}
	written, ok := detailNumber(e.Detail, key)
	if !ok || written <= 0 || !e.HasDuration() {
		return ""
	}
	return humanBytes(int64(written/e.Duration.Seconds())) + "/s"
}

// detailNumber reads a number of an audit detail, which is a float64 once it has been through JSON.
func detailNumber(detail map[string]any, key string) (float64, bool) {
	switch n := detail[key].(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// formatDuration writes a duration compactly and without a language: 850ms, 12.3s, 3m 12s, 1h 02m.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		s := int(d.Round(time.Second) / time.Second)
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	}
	m := int(d.Round(time.Minute) / time.Minute)
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

// Soon reports whether t is about to arrive, for texts such as "any moment now".
func (p *Page) Soon(t *time.Time) bool { return t != nil && time.Until(*t) < 30*time.Second }

func (p *Page) label(prefix, v string) string {
	key := prefix + v
	if messageExists(key) {
		return p.T(key)
	}
	return v
}

// The *Label methods translate the fixed vocabularies of the data model.
func (p *Page) VerifyLabel(v string) string         { return p.label("verify.", v) }
func (p *Page) StateLabel(v string) string          { return p.label("state.", v) }
func (p *Page) PhaseLabel(v string) string          { return p.label("phase.", v) }
func (p *Page) KindLabel(v string) string           { return p.label("kind.", v) }
func (p *Page) JobKindLabel(v string) string        { return p.label("jobkind.", v) }
func (p *Page) PolicyLabel(v string) string         { return p.label("policy.", v) }
func (p *Page) ActionLabel(v string) string         { return p.label("action.", v) }
func (p *Page) ItemStatusLabel(v string) string     { return p.label("item.", v) }
func (p *Page) AuditActionLabel(v string) string    { return p.label("auditaction.", v) }
func (p *Page) UpdateStateLabel(v string) string    { return p.label("update.state.", v) }
func (p *Page) AuditResultLabel(v string) string    { return p.label("auditresult.", v) }
func (p *Page) ComponentLabel(v string) string      { return p.label("component.", v) }
func (p *Page) ComponentStateLabel(v string) string { return p.label("cstate.", v) }
func (p *Page) LiveLabel(v service.LiveStatus) string {
	return p.label("live.", string(v))
}
func (p *Page) NowLabel(v catalog.NowStatus) string { return p.label("now.", string(v)) }

// LevelLabel names a level a visitor can have on a folder.
func (p *Page) LevelLabel(l access.Level) string { return p.label("level.", l.String()) }

// LevelHint says what a level allows.
func (p *Page) LevelHint(l access.Level) string { return p.label("level.hint.", l.String()) }

// pagePair hands a template that is called with a single value both the page, for its translations, and a value of
// its own.
type pagePair struct {
	Page  *Page
	Value any
}

// ComponentDetail is the detail text of a status row: translated when the
// service supplied a key for it, otherwise exactly as reported.
func (p *Page) ComponentDetail(c service.Component) string {
	if c.Key != "" {
		if key := "status.detail." + c.Key; messageExists(key) {
			return p.T(key, c.Args...)
		}
	}
	return c.Detail
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case uint:
		return int(n)
	case uint64:
		return int(n)
	}
	return 0
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"bytes":     humanBytes,
		"shortHash": shortHash,
		"hex":       func(b []byte) string { return hex.EncodeToString(b) },
		"add":       func(a, b int) int { return a + b },
		"int":       toInt,
		"url":       s.href,
		"route":     buildURL,
		"basePath":  func() string { return s.base },
		"joinRel":   pathutil.JoinRel,
		"pct":       percent,
		"iso":       func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"lower":     strings.ToLower,
		"join":      strings.Join,
		"pair":      func(p *Page, v any) pagePair { return pagePair{Page: p, Value: v} },
	}
}

func humanBytes(n int64) string {
	if n < 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

func shortHash(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	h := hex.EncodeToString(b)
	if len(h) > 12 {
		h = h[:12]
	}
	return h
}

func percent(n, total int64) int {
	if total <= 0 {
		return 0
	}
	p := int(n * 100 / total)
	return max(0, min(p, 100))
}

// buildURL joins a path and key/value pairs into a query string, leaving out
// empty strings, zeros and false values.
func buildURL(p string, kv ...any) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		key, _ := kv[i].(string)
		switch v := kv[i+1].(type) {
		case string:
			if v != "" {
				q.Set(key, v)
			}
		case int:
			if v != 0 {
				q.Set(key, fmt.Sprint(v))
			}
		case int64:
			if v != 0 {
				q.Set(key, fmt.Sprint(v))
			}
		case bool:
			if v {
				q.Set(key, "1")
			}
		}
	}
	if len(q) == 0 {
		return p
	}
	return p + "?" + q.Encode()
}

func parseTemplates(webFS fs.FS, funcs template.FuncMap) (map[string]*template.Template, error) {
	files, err := fs.Glob(webFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	pages := map[string]*template.Template{}
	for _, f := range files {
		base := path.Base(f)
		if base == "layout.html" || base == "partials.html" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(webFS, "templates/layout.html", "templates/partials.html", f)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", base, err)
		}
		pages[strings.TrimSuffix(base, ".html")] = t
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no page templates found")
	}
	return pages, nil
}

// returnURI is the address of the page being served, or the home page when the page answers a
// form post: such an address has no GET route, so a language switch would end on a 404.
func returnURI(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return r.URL.RequestURI()
	}
	return "/"
}

func (s *Server) newPage(r *http.Request, sess auth.Session, titleKey, nav string) *Page {
	loc := s.locale(r)
	p := &Page{
		Nav: nav, Lang: loc.lang, User: sess.User, Name: sess.Name, CSRF: sess.CSRF, Version: s.opts.Version, AssetVer: s.assetVer,
		AuthDisabled: sess.AuthDisabled, RequestURI: returnURI(r), loc: loc, tz: s.opts.Location, sess: sess,
		Notice: s.updateNotice(sess, loc), AvatarURL: s.opts.AvatarURL, avatar: s.avatarID,
	}
	if p.Name == "" {
		p.Name = p.User
	}
	if titleKey != "" {
		p.Title = loc.T(titleKey)
	}
	if c, err := r.Cookie(cookieFlash); err == nil {
		kind, key, _ := strings.Cut(c.Value, ":")
		if (kind == "ok" || kind == "error" || kind == "info") && messageExists(key) {
			p.Flash = &Flash{Kind: kind, Message: loc.T(key)}
		}
	}
	return p
}

const cookieFlash = "tv_flash"

// setFlash stores a message key for the next page view.
func (s *Server) setFlash(w http.ResponseWriter, kind, key string) {
	http.SetCookie(w, &http.Cookie{Name: cookieFlash, Value: kind + ":" + key, Path: s.cookiePath(), MaxAge: 60, HttpOnly: true,
		Secure: s.opts.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearFlash(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieFlash, Value: "", Path: s.cookiePath(), MaxAge: -1, HttpOnly: true,
		Secure: s.opts.CookieSecure, SameSite: http.SameSiteLaxMode})
}

// render executes a page template. A request with the partial header only gets
// the content block, which the UI script swaps in without a full reload.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, status int, p *Page) {
	t, ok := s.pages[name]
	if !ok {
		s.log.Error("unknown template", "name", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	partial := r.Header.Get(partialHdr) != ""
	block := "layout.html"
	if partial {
		block = "content"
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, p); err != nil {
		s.log.Error("render template", "name", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	if partial {
		h.Set("X-TV-Title", url.PathEscape(p.Title))
		h.Set("X-TV-Nav", p.Nav)
		h.Set("X-TV-Notice", url.PathEscape(p.Notice))
	}
	if p.Flash != nil {
		s.clearFlash(w)
	}
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// crumb is one element of a breadcrumb trail.
type crumb struct {
	Name string
	Rel  string
	Last bool
}

func crumbsFor(rel string) []crumb {
	var out []crumb
	if rel == "" {
		return out
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		out = append(out, crumb{Name: part, Rel: strings.Join(parts[:i+1], "/"), Last: i == len(parts)-1})
	}
	return out
}
