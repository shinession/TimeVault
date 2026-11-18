package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"timevault/internal/access"
	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/config"
	"timevault/internal/service"
)

const defaultScheduleMinutes = 60

// errBadMinutes is the form-level counterpart of the engine's range check: the field did not hold a number.
var errBadMinutes = fmt.Errorf("%w: the interval must be a whole number of minutes between 1 and 43200", backup.ErrInvalidSource)

// errBadVersions is the same for the number of versions kept per file.
var errBadVersions = fmt.Errorf("%w: the number of versions must be a whole number from 0 to %d", backup.ErrInvalidSource, backup.MaxVersionsLimit)

// nextRun is when the schedule of a source starts its next backup. It is nil
// without a schedule and while the source is switched off.
func nextRun(src catalog.Source, now time.Time) *time.Time {
	if !src.Enabled || src.ScheduleMinutes == nil {
		return nil
	}
	next := now
	if src.LastRunAt != nil {
		if t := src.LastRunAt.Add(time.Duration(*src.ScheduleMinutes) * time.Minute); t.After(now) {
			next = t
		}
	}
	return &next
}

// settingsForm holds the fields of a monitored folder as typed, so a rejected form can be shown back unchanged.
type settingsForm struct {
	Name        string
	DisplayPath string
	RootPath    string
	Excludes    string
	Enabled     bool
	Scheduled   bool
	Minutes     string
	// MaxVersions is the number of versions kept per file as typed; "0" keeps them all.
	MaxVersions string
	// Access is who may do what as ticked in the editor; AccessSet is false when the form had no editor, which
	// leaves the access of the folder as it is. AccessRev is the digest of the grants the editor was built from.
	Access    access.Spec
	AccessSet bool
	AccessRev string
}

// untouched reports whether the editor of the form still says what the grants it was built from said: nobody
// ticked or unticked anything. Such a form leaves the grants alone, so that saving a schedule cannot bring back
// grants that somebody else took away while the form was open.
func (f settingsForm) untouched() bool {
	if !f.AccessSet || f.AccessRev == "" {
		return false
	}
	list, err := f.Access.List()
	return err == nil && list.Digest() == f.AccessRev
}

func formOf(src catalog.Source) settingsForm {
	f := settingsForm{
		Name: src.Name, DisplayPath: src.DisplayPath, RootPath: src.RootPath, Excludes: strings.Join(src.Excludes, "\n"),
		Enabled: src.Enabled, Minutes: strconv.Itoa(defaultScheduleMinutes), MaxVersions: "0", Access: src.Access.Spec(), AccessSet: true,
		AccessRev: src.Access.Digest(),
	}
	if src.ScheduleMinutes != nil {
		f.Scheduled, f.Minutes = true, strconv.Itoa(*src.ScheduleMinutes)
	}
	if src.MaxVersions != nil {
		f.MaxVersions = strconv.Itoa(*src.MaxVersions)
	}
	return f
}

func parseSourceForm(r *http.Request) (settingsForm, backup.SourceRequest, error) {
	f := settingsForm{
		Name: r.PostFormValue("name"), DisplayPath: r.PostFormValue("display_path"), RootPath: r.PostFormValue("root_path"),
		Excludes: r.PostFormValue("excludes"), Enabled: r.PostFormValue("enabled") != "", Scheduled: r.PostFormValue("schedule") != "",
		Minutes: strings.TrimSpace(r.PostFormValue("schedule_minutes")), MaxVersions: strings.TrimSpace(r.PostFormValue("max_versions")),
	}
	f.Access, f.AccessSet = accessSpecOf(r)
	f.AccessRev = r.PostFormValue("access_rev")
	req := backup.SourceRequest{
		Name: f.Name, DisplayPath: f.DisplayPath, RootPath: f.RootPath, Excludes: strings.Split(strings.ReplaceAll(f.Excludes, "\r", ""), "\n"),
		ConfirmPrune: r.PostFormValue("confirm_prune") != "",
	}
	if f.Scheduled {
		n, err := strconv.Atoi(f.Minutes)
		if err != nil {
			return f, req, errBadMinutes
		}
		req.ScheduleMinutes = &n
	}
	if f.MaxVersions != "" {
		n, err := strconv.Atoi(f.MaxVersions)
		if err != nil || n < 0 || n > backup.MaxVersionsLimit {
			return f, req, errBadVersions
		}
		req.MaxVersions = &n
	} else {
		f.MaxVersions = "0"
	}
	return f, req, nil
}

type settingsSource struct {
	catalog.SourceSummary
	// Level is what the visitor may do with the folder. CanEdit is true with full control of it.
	Level   access.Level
	CanEdit bool
	// Who says who may do what with the folder; only those who may change it are told.
	Who []accessRow
	// Minutes is the interval of the schedule; 0 when there is none.
	Minutes int
	// Versions is how many versions of a file are kept; 0 when all of them are.
	Versions int
	NextRun  *time.Time
	// HasPoints locks the folder path: history belongs to a folder.
	HasPoints bool
	Form      settingsForm
	// Open shows the edit form: it was asked for (?edit=) or it was rejected. The access editor is only built for
	// an open form, because it lists every account of the directory.
	Open   bool
	Picker accessPicker
	// Error shows why the form was rejected; NeedsConfirm adds the box that confirms a limit that deletes older
	// versions, NeedsLockout the one that confirms taking away the visitor's own full control of the folder.
	Error        string
	NeedsConfirm bool
	NeedsLockout bool
}

type settingsData struct {
	Sources []settingsSource
	AddForm settingsForm
	// AddOpen shows the form that adds a folder (?add=1, or it was rejected); AddPicker is its access editor.
	AddOpen    bool
	AddPicker  accessPicker
	AddError   string
	Deployment config.Deployment
	Version    string
	// Repository is the health of the backup folder, empty when it could not be determined.
	Repository service.Component
	// RepoFreePct is the free space of the backup folder in percent, -1 when unknown.
	RepoFreePct    int
	RestoreEnabled bool
}

// settingsFailure carries a rejected form, or the form that was asked for, into the page.
type settingsFailure struct {
	add       *settingsForm
	addError  string
	editID    int64
	editForm  *settingsForm
	editError string
	// confirm asks the edit form to show the box that confirms deleting older versions; lockout the box that
	// confirms taking away the visitor's own full control of the folder.
	confirm bool
	lockout bool
}

func (s *Server) settingsData(r *http.Request, sess auth.Session, fail settingsFailure) (*settingsData, error) {
	ctx := r.Context()
	view := s.viewerFor(ctx, sess)
	sources, err := s.visibleSources(ctx, sess)
	if err != nil {
		return nil, err
	}
	d := &settingsData{
		Deployment: s.opts.Deployment, Version: s.opts.Version, RestoreEnabled: s.svc.RestoreEnabled(), RepoFreePct: -1,
		AddForm: settingsForm{Excludes: strings.Join(backup.DefaultExcludes, "\n"), Minutes: strconv.Itoa(defaultScheduleMinutes), MaxVersions: "0", AccessSet: true},
	}
	if fail.add != nil {
		d.AddForm, d.AddError = *fail.add, fail.addError
	}
	// The accounts are only needed to name people (for those who may see who has access) and to build an editor.
	var accounts []auth.Account
	var listed, fetched bool
	directory := func() ([]auth.Account, bool) {
		if !fetched {
			accounts, listed = s.directoryAccounts(ctx)
			fetched = true
		}
		return accounts, listed
	}
	if isAdmin(sess) && (fail.add != nil || r.FormValue("add") != "") {
		d.AddOpen = true
		accs, ok := directory()
		d.AddPicker = buildPicker(d.AddForm.Access, accs, ok)
	}
	now := time.Now()
	editID := fail.editID
	if editID == 0 && fail.add == nil {
		editID = intParam(r, "edit")
	}
	for _, src := range sources {
		card := settingsSource{SourceSummary: src, NextRun: nextRun(src.Source, now), Form: formOf(src.Source), Level: levelOn(sess, src.Source)}
		card.CanEdit = card.Level >= access.Full
		if src.ScheduleMinutes != nil {
			card.Minutes = *src.ScheduleMinutes
		}
		if src.MaxVersions != nil {
			card.Versions = *src.MaxVersions
		}
		if card.HasPoints, err = s.cat().HasPoints(ctx, src.ID); err != nil {
			return nil, err
		}
		if card.CanEdit {
			accs, _ := directory()
			card.Who = summarize(src.Access, accs)
			if editID == src.ID {
				if fail.editForm != nil {
					card.Form, card.Error, card.NeedsConfirm, card.NeedsLockout = *fail.editForm, fail.editError, fail.confirm, fail.lockout
				}
				card.Open = true
				accs, ok := directory()
				card.Picker = buildPicker(card.Form.Access, accs, ok)
				card.Picker.Rev = card.Form.AccessRev
			}
		}
		d.Sources = append(d.Sources, card)
	}
	for _, c := range s.svc.Health(ctx).Components {
		if c.Name != "repository" {
			continue
		}
		d.Repository = c
		d.Repository.Detail = view.text(c.Detail)
		if c.Key == "repo_free" && len(c.Args) == 2 {
			if free, ok := c.Args[1].(int); ok {
				d.RepoFreePct = free
			}
		}
	}
	return d, nil
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	d, err := s.settingsData(r, sess, settingsFailure{})
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "settings.title", "settings")
	p.Data = d
	s.render(w, r, "settings", http.StatusOK, p)
}

// settingsRejected shows the settings page again with the form that was rejected and why.
func (s *Server) settingsRejected(w http.ResponseWriter, r *http.Request, sess auth.Session, fail settingsFailure, err error) {
	status, key := classify(err)
	if errors.Is(err, service.ErrNotFound) || (status == http.StatusInternalServerError && !errors.Is(err, errBadMinutes) && !errors.Is(err, errBadVersions)) {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "settings.title", "settings")
	msg := err.Error()
	var invalid *backup.SourceError
	var badAccess *access.SpecError
	var unconfirmed *service.PruneNotConfirmedError
	var changed *accessChangedError
	switch {
	case errors.As(err, &unconfirmed):
		fail.confirm = true
		msg = p.T("settings.prune_confirm", unconfirmed.Versions, unconfirmed.Files)
	case errors.Is(err, errLockout):
		fail.lockout = true
		msg = p.T("errors.lockout_not_confirmed")
	case errors.As(err, &changed):
		// The lists are shown as they are now, with what else was typed, so that the change can be made again.
		if fail.editForm != nil {
			fail.editForm.Access, fail.editForm.AccessRev = changed.Current.Spec(), changed.Current.Digest()
		}
		msg = p.T("errors.access_changed")
	case errors.Is(err, errBadMinutes):
		msg = p.T("settings.minutes_error")
	case errors.Is(err, errBadVersions):
		msg = p.T("settings.versions_error")
	case errors.As(err, &invalid) && messageExists(invalid.Key):
		msg = p.T(invalid.Key, invalid.Args...)
	case errors.As(err, &badAccess) && messageExists(badAccess.Key):
		msg = p.T(badAccess.Key, p.specArgs(badAccess.Args)...)
	case key != "" && !errors.Is(err, backup.ErrInvalidSource) && !errors.Is(err, access.ErrInvalid):
		msg = p.T(key)
	}
	if fail.add != nil {
		fail.addError = msg
	} else {
		fail.editError = msg
	}
	d, derr := s.settingsData(r, sess, fail)
	if derr != nil {
		s.handleErr(w, r, derr)
		return
	}
	p.Data = d
	s.render(w, r, "settings", status, p)
}

// specArgs shows the levels among the arguments of an access error by their names in the page's language.
func (p *Page) specArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		if level, ok := a.(access.Level); ok {
			a = p.LevelLabel(level)
		}
		out[i] = a
	}
	return out
}

func (s *Server) handleAddSource(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	form, req, err := parseSourceForm(r)
	if err == nil {
		// A new folder is seen by administrators only until somebody is given access to it.
		var grants access.List
		if grants, err = s.grantsOf(r.Context(), form.Access, nil); err == nil {
			req.Access = &grants
			_, err = s.svc.AddSource(r.Context(), s.actor(r, sess), req)
		}
	}
	if err != nil {
		s.settingsRejected(w, r, sess, settingsFailure{add: &form}, err)
		return
	}
	s.setFlash(w, "ok", "flash.source_added")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// handleUpdateSource saves the settings of a folder. Whoever has full control of it may, but the folder on the
// server's disk is only changed by an administrator: a form from anybody else must not carry it.
func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	cur, ok := s.source(w, r, sess, id, access.Full)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	form, req, err := parseSourceForm(r)
	if !isAdmin(sess) {
		if form.RootPath != "" {
			s.rootPathDenied(w, r, sess, id)
			return
		}
		// The folder on the server is not theirs to change, and the update must not write it at all: it could
		// undo a change that an administrator makes at the same moment.
		req.RootPath, req.KeepRoot, form.RootPath = "", true, cur.RootPath
	}
	rev := ""
	if err == nil && form.AccessSet && !form.untouched() {
		var grants access.List
		if grants, err = s.grantsOf(r.Context(), form.Access, cur.Access); err == nil {
			err = s.lockout(sess, grants, r.PostFormValue("confirm_lockout") != "")
			req.Access, rev = &grants, form.AccessRev
		}
	}
	req.Authorize = authorizeChange(sess, access.Full, rev)
	if err == nil {
		_, err = s.svc.UpdateSource(r.Context(), s.actor(r, sess), id, req, form.Enabled)
	}
	if errors.Is(err, service.ErrForbidden) {
		s.levelDenied(w, r, sess, &id, access.Full)
		return
	}
	if err != nil {
		s.settingsRejected(w, r, sess, settingsFailure{editID: id, editForm: &form}, err)
		return
	}
	s.setFlash(w, "ok", "flash.source_updated")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
