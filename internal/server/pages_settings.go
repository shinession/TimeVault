package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/config"
	"timevault/internal/service"
)

const defaultScheduleMinutes = 60

// errBadMinutes is the form-level counterpart of the engine's range check: the field did not hold a number.
var errBadMinutes = fmt.Errorf("%w: the interval must be a whole number of minutes between 1 and 43200", backup.ErrInvalidSource)

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
}

func formOf(src catalog.Source) settingsForm {
	f := settingsForm{
		Name: src.Name, DisplayPath: src.DisplayPath, RootPath: src.RootPath, Excludes: strings.Join(src.Excludes, "\n"),
		Enabled: src.Enabled, Minutes: strconv.Itoa(defaultScheduleMinutes),
	}
	if src.ScheduleMinutes != nil {
		f.Scheduled, f.Minutes = true, strconv.Itoa(*src.ScheduleMinutes)
	}
	return f
}

func parseSourceForm(r *http.Request) (settingsForm, backup.SourceRequest, error) {
	f := settingsForm{
		Name: r.PostFormValue("name"), DisplayPath: r.PostFormValue("display_path"), RootPath: r.PostFormValue("root_path"),
		Excludes: r.PostFormValue("excludes"), Enabled: r.PostFormValue("enabled") != "", Scheduled: r.PostFormValue("schedule") != "",
		Minutes: strings.TrimSpace(r.PostFormValue("schedule_minutes")),
	}
	req := backup.SourceRequest{
		Name: f.Name, DisplayPath: f.DisplayPath, RootPath: f.RootPath, Excludes: strings.Split(strings.ReplaceAll(f.Excludes, "\r", ""), "\n"),
	}
	if f.Scheduled {
		n, err := strconv.Atoi(f.Minutes)
		if err != nil {
			return f, req, errBadMinutes
		}
		req.ScheduleMinutes = &n
	}
	return f, req, nil
}

type settingsSource struct {
	catalog.SourceSummary
	// Minutes is the interval of the schedule; 0 when there is none.
	Minutes int
	NextRun *time.Time
	// HasPoints locks the folder path: history belongs to a folder.
	HasPoints bool
	Form      settingsForm
	// Open and Error show the edit form again after it was rejected.
	Open  bool
	Error string
}

type settingsData struct {
	Sources    []settingsSource
	AddForm    settingsForm
	AddError   string
	Deployment config.Deployment
	Version    string
	// Repository is the health of the backup folder, empty when it could not be determined.
	Repository service.Component
	// RepoFreePct is the free space of the backup folder in percent, -1 when unknown.
	RepoFreePct    int
	RestoreEnabled bool
}

// settingsFailure carries a rejected form back into the page.
type settingsFailure struct {
	add       *settingsForm
	addError  string
	editID    int64
	editForm  settingsForm
	editError string
}

func (s *Server) settingsData(r *http.Request, sess auth.Session, fail settingsFailure) (*settingsData, error) {
	ctx := r.Context()
	view := s.viewerFor(ctx, sess)
	sources, err := s.svc.Sources(ctx)
	if err != nil {
		return nil, err
	}
	d := &settingsData{
		Deployment: s.opts.Deployment, Version: s.opts.Version, RestoreEnabled: s.svc.RestoreEnabled(), RepoFreePct: -1,
		AddForm: settingsForm{Excludes: strings.Join(backup.DefaultExcludes, "\n"), Minutes: strconv.Itoa(defaultScheduleMinutes)},
	}
	if fail.add != nil {
		d.AddForm, d.AddError = *fail.add, fail.addError
	}
	now := time.Now()
	for _, src := range sources {
		card := settingsSource{SourceSummary: src, NextRun: nextRun(src.Source, now), Form: formOf(src.Source)}
		if src.ScheduleMinutes != nil {
			card.Minutes = *src.ScheduleMinutes
		}
		if card.HasPoints, err = s.cat().HasPoints(ctx, src.ID); err != nil {
			return nil, err
		}
		if fail.editID == src.ID {
			card.Form, card.Error, card.Open = fail.editForm, fail.editError, true
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
	if errors.Is(err, service.ErrNotFound) || (status == http.StatusInternalServerError && !errors.Is(err, errBadMinutes)) {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "settings.title", "settings")
	msg := err.Error()
	var invalid *backup.SourceError
	switch {
	case errors.Is(err, errBadMinutes):
		msg = p.T("settings.minutes_error")
	case errors.As(err, &invalid) && messageExists(invalid.Key):
		msg = p.T(invalid.Key, invalid.Args...)
	case key != "" && !errors.Is(err, backup.ErrInvalidSource):
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

func (s *Server) handleAddSource(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	form, req, err := parseSourceForm(r)
	if err == nil {
		_, err = s.svc.AddSource(r.Context(), s.actor(r, sess), req)
	}
	if err != nil {
		s.settingsRejected(w, r, sess, settingsFailure{add: &form}, err)
		return
	}
	s.setFlash(w, "ok", "flash.source_added")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	form, req, err := parseSourceForm(r)
	if err == nil {
		_, err = s.svc.UpdateSource(r.Context(), s.actor(r, sess), id, req, form.Enabled)
	}
	if err != nil {
		s.settingsRejected(w, r, sess, settingsFailure{editID: id, editForm: form}, err)
		return
	}
	s.setFlash(w, "ok", "flash.source_updated")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
