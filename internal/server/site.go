package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/config"
)

// ErrJobsRunning is what SiteBackend.Restart returns while a job runs: a restart would cut it off.
var ErrJobsRunning = errors.New("server: jobs are running")

// SiteBackend is what the Site settings page needs of the service. The page is not offered without it.
type SiteBackend struct {
	// Base answers the settings of the environment and the .env file. The Site settings are checked together with
	// them, because some settings depend on both (the web log needs the Redis, an update address needs automatic
	// migrations).
	Base config.Lookup
	// Stored reads the Site settings that the database holds now, by key; Save stores them (see sitesettings.Store).
	Stored func(ctx context.Context) (map[string]string, error)
	Save   func(ctx context.Context, set map[string]string, reset []string, by string) error
	// Running is what the service started with: config.SiteEffective of the values it read at start. A Site setting
	// only takes effect after a restart, so the page says so while Stored differs from it.
	Running map[string]string
	// Restart asks the service to stop and start again. It returns ErrJobsRunning while a job runs. Nil when the
	// service cannot restart itself.
	Restart func() error
}

// siteFormPrefix is put in front of the key of a setting to name its form field.
const siteFormPrefix = "s_"

type siteOption struct {
	Value, Label string
	Selected     bool
}

type siteField struct {
	Key, Name, Label, Help string
	// Kind is the config.SiteKind: text, number, bool or choice.
	Kind    string
	Value   string
	Default string
	Options []siteOption
}

type siteGroup struct {
	Title, Hint string
	Fields      []siteField
}

// siteFact is one line of what is set in the environment or the .env file, which the page only shows.
type siteFact struct {
	Label, Value string
	// Code shows the value in a monospace font: it is a name, an address or a path.
	Code bool
}

type siteData struct {
	Groups []siteGroup
	Facts  []siteFact
	// Pending is true while the stored settings differ from the ones the service runs with.
	Pending    bool
	CanRestart bool
	// Errors say why a form was rejected.
	Errors []string
}

// needSite is need for the Site settings: only the local administrator may see them. Anybody else, signed in or not
// asked, is told that there is no such page, and the attempt is audited.
func (s *Server) needSite(next sessionHandler) http.HandlerFunc {
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
		if s.opts.Site == nil || !sess.Can(auth.PermSite) {
			s.denied(w, r, sess, "site", "errors.not_found", http.StatusNotFound, "not_found", "no such endpoint")
			return
		}
		next(w, r.WithContext(auth.WithSession(r.Context(), sess)), sess)
	}
}

// knownSite keeps the values of the keys that are Site settings of this version; a row of a later version that was
// stored before a downgrade is not this version's business.
func knownSite(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		if _, ok := config.SiteSettingByKey(k); ok {
			out[k] = v
		}
	}
	return out
}

// diffSite says what has to be stored and what has to be removed to turn old into next.
func diffSite(old, next map[string]string) (set map[string]string, reset []string) {
	set = map[string]string{}
	for k, v := range next {
		if old[k] != v {
			set[k] = v
		}
	}
	for k := range old {
		if _, kept := next[k]; !kept {
			reset = append(reset, k)
		}
	}
	sort.Strings(reset)
	return set, reset
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// siteView builds the page: the form with the values in effect (or the values that were typed, for a form that was
// rejected) and the settings that only the environment can change.
func (s *Server) siteView(ctx context.Context, p *Page, typed map[string]string, problems []string) (*siteData, error) {
	b := s.opts.Site
	stored, err := b.Stored(ctx)
	if err != nil {
		return nil, err
	}
	effective := config.SiteEffective(stored)
	d := &siteData{Errors: problems, Pending: !reflect.DeepEqual(effective, b.Running), CanRestart: b.Restart != nil}
	byGroup := map[string][]siteField{}
	for _, set := range config.SiteSettings() {
		value := effective[set.Key]
		if v, ok := typed[set.Key]; ok {
			value = v
		}
		f := siteField{
			Key: set.Key, Name: siteFormPrefix + set.Key, Label: p.T("site.f." + set.Key), Help: p.T("site.h." + set.Key),
			Kind: string(set.Kind), Value: value, Default: set.Default,
		}
		switch set.Kind {
		case config.SiteChoice:
			for _, c := range set.Choices {
				f.Options = append(f.Options, siteOption{Value: c, Label: c, Selected: strings.EqualFold(value, c)})
			}
		case config.SiteBool:
			f.Options = []siteOption{
				{Value: "true", Label: p.T("settings.yes"), Selected: strings.EqualFold(value, "true")},
				{Value: "false", Label: p.T("settings.no"), Selected: !strings.EqualFold(value, "true")},
			}
		}
		byGroup[set.Group] = append(byGroup[set.Group], f)
	}
	for _, g := range config.SiteGroups() {
		d.Groups = append(d.Groups, siteGroup{Title: p.T("site.g." + g), Hint: p.T("site.gh." + g), Fields: byGroup[g]})
	}
	d.Facts = s.siteFacts(ctx, p)
	return d, nil
}

// siteFacts lists what the environment and the .env file hold, as far as it is not a secret.
func (s *Server) siteFacts(ctx context.Context, p *Page) []siteFact {
	dep := s.opts.Deployment
	none := p.T("settings.not_configured")
	orNone := func(v string) siteFact {
		if v == "" {
			return siteFact{Value: none}
		}
		return siteFact{Value: v, Code: true}
	}
	var facts []siteFact
	add := func(label string, f siteFact) {
		f.Label = label
		facts = append(facts, f)
	}
	add(p.T("settings.version"), siteFact{Value: "v" + s.opts.Version})
	add(p.T("settings.listen"), siteFact{Value: dep.ListenAddr, Code: true})
	add(p.T("settings.database"), orNone(dep.Database))
	add(p.T("settings.directory"), orNone(dep.UserDirectory))
	add(p.T("settings.repository"), orNone(dep.RepositoryPath))
	add(p.T("settings.restore_root"), orNone(dep.RestoreRoot))
	admin := siteFact{Value: p.T("settings.none")}
	if dep.LocalAdmin != "" {
		admin = siteFact{Value: dep.LocalAdmin}
	}
	add(p.T("settings.local_admin"), admin)
	if dep.AuthDisabled {
		add(p.T("site.signin_off"), siteFact{Value: p.T("settings.yes")})
	}
	add(p.T("settings.env_file"), func() siteFact {
		if dep.EnvFile == "" {
			return siteFact{Value: p.T("settings.none")}
		}
		return siteFact{Value: dep.EnvFile, Code: true}
	}())
	h := s.svc.SearchProvider().Health(ctx)
	index := p.ComponentStateLabel(string(h.State))
	if h.Detail != "" {
		index += ": " + h.Detail
	}
	add(p.T("site.index"), siteFact{Value: index})
	return facts
}

func (s *Server) handleSite(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	p := s.newPage(r, sess, "site.title", "settings")
	d, err := s.siteView(r.Context(), p, nil, nil)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p.Data = d
	s.render(w, r, "site", http.StatusOK, p)
}

// siteRejected shows the form again, as it was typed, with what is wrong.
func (s *Server) siteRejected(w http.ResponseWriter, r *http.Request, sess auth.Session, typed map[string]string, problems []string) {
	p := s.newPage(r, sess, "site.title", "settings")
	d, err := s.siteView(r.Context(), p, typed, problems)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p.Data = d
	s.render(w, r, "site", http.StatusBadRequest, p)
}

// siteChange works out what saving the posted values would store. typed holds what was typed, for a form that has to
// be shown again. A value that equals the default is not stored, so the default that a later version brings reaches
// whoever never chose a value. A setting that the form does not carry is left as it is.
func siteChange(stored map[string]string, posted func(key string) (string, bool)) (next, typed map[string]string, problems []string) {
	next, typed = knownSite(stored), map[string]string{}
	for _, set := range config.SiteSettings() {
		raw, ok := posted(set.Key)
		if !ok {
			continue
		}
		typed[set.Key] = strings.TrimSpace(raw)
		v, err := config.NormalizeSite(set.Key, raw)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if v == "" || v == set.Default {
			delete(next, set.Key)
		} else {
			next[set.Key] = v
		}
	}
	return next, typed, problems
}

func (s *Server) handleSiteSave(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	b := s.opts.Site
	stored, err := b.Stored(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	next, typed, problems := siteChange(stored, func(key string) (string, bool) {
		v, ok := r.PostForm[siteFormPrefix+key]
		if !ok || len(v) == 0 {
			return "", false
		}
		return v[0], true
	})
	if len(problems) == 0 {
		problems = config.SiteProblems(b.Base, next)
	}
	if len(problems) > 0 {
		s.siteRejected(w, r, sess, typed, problems)
		return
	}
	set, reset := diffSite(knownSite(stored), next)
	if len(set)+len(reset) == 0 {
		s.setFlash(w, "info", "flash.site_unchanged")
		http.Redirect(w, r, "/settings/site", http.StatusSeeOther)
		return
	}
	if err := b.Save(r.Context(), set, reset, sess.User); err != nil {
		s.handleErr(w, r, err)
		return
	}
	s.auditSite(r, sess, audit.ActionSiteUpdate, set, reset)
	s.setFlash(w, "ok", "flash.site_saved")
	http.Redirect(w, r, "/settings/site", http.StatusSeeOther)
}

// auditSite records a change of the Site settings. The audit log is shown to everybody who signs in, so the detail
// names the settings that changed and never their values, which may be addresses or paths.
func (s *Server) auditSite(r *http.Request, sess auth.Session, action string, set map[string]string, reset []string) {
	detail := map[string]any{}
	if len(set) > 0 {
		detail["changed"] = sortedKeys(set)
	}
	if len(reset) > 0 {
		detail["reset"] = reset
	}
	a := s.actor(r, sess)
	s.svc.Audit(r.Context(), audit.Event{Actor: a.User, Action: action, TargetType: "site", RemoteAddr: a.Remote, Detail: detail})
}

func (s *Server) handleSiteRestart(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	b := s.opts.Site
	if b.Restart == nil {
		s.setFlash(w, "error", "flash.site_restart_unavailable")
		http.Redirect(w, r, "/settings/site", http.StatusSeeOther)
		return
	}
	err := b.Restart()
	switch {
	case errors.Is(err, ErrJobsRunning):
		s.setFlash(w, "error", "flash.site_jobs_running")
		http.Redirect(w, r, "/settings/site", http.StatusSeeOther)
		return
	case err != nil:
		s.handleErr(w, r, err)
		return
	}
	s.auditSite(r, sess, audit.ActionSiteRestart, nil, nil)
	p := s.newPage(r, sess, "site.restarting_title", "settings")
	s.render(w, r, "site_restarting", http.StatusOK, p)
}

// --- API -------------------------------------------------------------------------------------

type apiSiteSetting struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Kind    string   `json:"kind"`
	Value   string   `json:"value"`
	Default string   `json:"default"`
	Stored  bool     `json:"stored"`
	Choices []string `json:"choices,omitempty"`
}

// apiSite lists the Site settings with the value in effect, and says whether a restart is needed.
func (s *Server) apiSite(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	b := s.opts.Site
	stored, err := b.Stored(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	stored = knownSite(stored)
	effective := config.SiteEffective(stored)
	out := make([]apiSiteSetting, 0, len(effective))
	for _, set := range config.SiteSettings() {
		_, isStored := stored[set.Key]
		out = append(out, apiSiteSetting{
			Key: set.Key, Group: set.Group, Kind: string(set.Kind), Value: effective[set.Key], Default: set.Default, Stored: isStored, Choices: set.Choices,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": out, "restart_required": !reflect.DeepEqual(effective, b.Running), "can_restart": b.Restart != nil,
	})
}

// apiSiteUpdate saves Site settings. The body is {"settings": {"key": "value", "other": null}}: a null or empty
// value, or one that equals the default, makes the setting use its default. Settings that are not named stay as they are.
func (s *Server) apiSiteUpdate(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body struct {
		Settings map[string]*string `json:"settings"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Settings) == 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "settings must name at least one Site setting")
		return
	}
	b := s.opts.Site
	stored, err := b.Stored(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	var unknown []string
	for k := range body.Settings {
		if _, ok := config.SiteSettingByKey(k); !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		writeJSONError(w, http.StatusBadRequest, "unknown_setting", strings.Join(unknown, ", ")+" is not a Site setting")
		return
	}
	next, _, problems := siteChange(stored, func(key string) (string, bool) {
		v, ok := body.Settings[key]
		if !ok {
			return "", false
		}
		if v == nil {
			return "", true
		}
		return *v, true
	})
	if len(problems) == 0 {
		problems = config.SiteProblems(b.Base, next)
	}
	if len(problems) > 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_setting", strings.Join(problems, "; "))
		return
	}
	set, reset := diffSite(knownSite(stored), next)
	if len(set)+len(reset) > 0 {
		if err := b.Save(r.Context(), set, reset, sess.User); err != nil {
			s.handleErr(w, r, err)
			return
		}
		s.auditSite(r, sess, audit.ActionSiteUpdate, set, reset)
	}
	effective := config.SiteEffective(next)
	writeJSON(w, http.StatusOK, map[string]any{
		"changed": sortedKeys(set), "reset": append([]string{}, reset...), "restart_required": !reflect.DeepEqual(effective, b.Running),
	})
}

// apiSiteRestart asks the service to restart itself and answers at once.
func (s *Server) apiSiteRestart(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	b := s.opts.Site
	if b.Restart == nil {
		writeJSONError(w, http.StatusConflict, "restart_unavailable", "the service cannot restart itself")
		return
	}
	switch err := b.Restart(); {
	case errors.Is(err, ErrJobsRunning):
		writeJSONError(w, http.StatusConflict, "jobs_running", "jobs are running; restart when they have finished")
		return
	case err != nil:
		s.handleErr(w, r, err)
		return
	}
	s.auditSite(r, sess, audit.ActionSiteRestart, nil, nil)
	writeJSON(w, http.StatusAccepted, map[string]any{"restarting": true})
}
