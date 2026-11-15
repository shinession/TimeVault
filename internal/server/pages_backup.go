package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/restore"
	"timevault/internal/service"
)

// jobView is a job prepared for display.
type jobView struct {
	Job        jobs.Job
	SourceName string
	Active     bool
	// Pct is -1 while the total is unknown.
	Pct      int
	Phase    string
	Counters []counter
	Duration string
	Request  *restore.Request
	Totals   *restore.Totals
}

type counter struct {
	Key   string
	Value int64
	Bytes bool
}

func (s *Server) viewJob(j jobs.Job, sources map[int64]string) jobView {
	v := jobView{Job: j, Active: j.Active(), Pct: -1, Phase: j.Phase}
	if j.SourceID != nil {
		v.SourceName = sources[*j.SourceID]
	}
	end := time.Now()
	if j.FinishedAt != nil {
		end = *j.FinishedAt
	}
	if j.StartedAt != nil {
		v.Duration = end.Sub(*j.StartedAt).Round(time.Second).String()
	}
	switch j.Kind {
	case jobs.KindBackup, jobs.KindVerify:
		var p backup.Progress
		if json.Unmarshal(j.Progress, &p) == nil && p.Phase != "" {
			v.Counters = []counter{
				{"counter.files", p.Files, false}, {"counter.dirs", p.Dirs, false},
				{"counter.added", p.Added, false}, {"counter.modified", p.Modified, false}, {"counter.deleted", p.Deleted, false},
				{"counter.skipped", p.Skipped, false}, {"counter.new_objects", p.NewObjects, false}, {"counter.bytes_hashed", p.BytesHashed, true},
			}
		}
		if j.State == jobs.StateSucceeded {
			v.Pct = 100
		}
	case jobs.KindRestore:
		var p restore.Progress
		if json.Unmarshal(j.Progress, &p) == nil && p.Phase != "" {
			v.Pct = percent(p.Items, p.ItemsTotal)
			v.Counters = []counter{
				{"counter.items", p.Items, false}, {"counter.created", p.Created, false}, {"counter.overwritten", p.Overwritten, false},
				{"counter.renamed", p.Renamed, false}, {"counter.skipped", p.Skipped, false}, {"counter.failed", p.Failed, false},
				{"counter.bytes_written", p.Bytes, true},
			}
		}
		if j.State == jobs.StateSucceeded {
			v.Pct = 100
		}
		if req, totals, ok := s.svc.RestoreJobRequest(j); ok {
			v.Request, v.Totals = &req, &totals
		}
	}
	return v
}

// --- Backups page ---------------------------------------------------------------------

type sourceCard struct {
	catalog.SourceSummary
	Active   *jobView
	Attempts []catalog.Point
}

type addSourceForm struct {
	Name     string
	RootPath string
	Excludes string
	Error    string
}

type backupsData struct {
	Sources         []sourceCard
	Jobs            []jobView
	Form            addSourceForm
	RestoreEnabled  bool
	RestoreRoot     string
	DefaultExcludes string
}

func (s *Server) backupsData(r *http.Request, form addSourceForm) (*backupsData, error) {
	ctx := r.Context()
	sources, err := s.svc.Sources(ctx)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, src := range sources {
		names[src.ID] = src.Name
	}
	recent, err := s.svc.Jobs(ctx, nil, "", 25)
	if err != nil {
		return nil, err
	}
	d := &backupsData{Form: form, RestoreEnabled: s.svc.RestoreEnabled(), RestoreRoot: s.svc.RestoreRoot(),
		DefaultExcludes: strings.Join(backup.DefaultExcludes, "\n")}
	if d.Form.Excludes == "" && d.Form.Name == "" && d.Form.RootPath == "" {
		d.Form.Excludes = d.DefaultExcludes
	}
	active := map[int64]*jobView{}
	for _, j := range recent {
		v := s.viewJob(j, names)
		d.Jobs = append(d.Jobs, v)
		if j.Kind == jobs.KindBackup && j.Active() && j.SourceID != nil {
			if _, ok := active[*j.SourceID]; !ok {
				vc := v
				active[*j.SourceID] = &vc
			}
		}
	}
	for _, src := range sources {
		attempts, err := s.svc.Attempts(ctx, src.ID, 12)
		if err != nil {
			return nil, err
		}
		d.Sources = append(d.Sources, sourceCard{SourceSummary: src, Active: active[src.ID], Attempts: attempts})
	}
	return d, nil
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	d, err := s.backupsData(r, addSourceForm{})
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "backups.title", "backups")
	p.Data = d
	s.render(w, r, "backups", http.StatusOK, p)
}

func (s *Server) handleAddSource(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	form := addSourceForm{Name: r.PostFormValue("name"), RootPath: r.PostFormValue("root_path"), Excludes: r.PostFormValue("excludes")}
	_, err := s.svc.AddSource(r.Context(), s.actor(r, sess), backup.SourceRequest{
		Name: form.Name, RootPath: form.RootPath, Excludes: strings.Split(strings.ReplaceAll(form.Excludes, "\r", ""), "\n"),
	})
	if err != nil {
		status, key := classify(err)
		if status == http.StatusInternalServerError {
			s.handleErr(w, r, err)
			return
		}
		form.Error = err.Error()
		if key != "" && !errors.Is(err, backup.ErrInvalidSource) {
			form.Error = s.newPage(r, sess, "", "").T(key)
		}
		d, derr := s.backupsData(r, form)
		if derr != nil {
			s.handleErr(w, r, derr)
			return
		}
		p := s.newPage(r, sess, "backups.title", "backups")
		p.Data = d
		s.render(w, r, "backups", status, p)
		return
	}
	s.setFlash(w, "ok", "flash.source_added")
	http.Redirect(w, r, "/backups", http.StatusSeeOther)
}

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	job, created, err := s.svc.StartBackup(r.Context(), s.actor(r, sess), intParam(r, "source_id"), r.PostFormValue("rehash") != "")
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if !created {
		s.setFlash(w, "info", "flash.backup_already")
	} else {
		s.setFlash(w, "ok", "flash.backup_queued")
	}
	http.Redirect(w, r, "/jobs/"+strconv.FormatInt(job.ID, 10), http.StatusSeeOther)
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	job, created, err := s.svc.StartVerify(r.Context(), s.actor(r, sess), intParam(r, "source_id"), intParam(r, "point_id"), r.PostFormValue("deep") != "")
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if created {
		s.setFlash(w, "ok", "flash.verify_queued")
	} else {
		s.setFlash(w, "info", "flash.verify_already")
	}
	http.Redirect(w, r, "/jobs/"+strconv.FormatInt(job.ID, 10), http.StatusSeeOther)
}

// --- Job page -----------------------------------------------------------------------------

type jobData struct {
	View      jobView
	Source    *catalog.Source
	Point     *catalog.Point
	Events    []jobs.Event
	Items     []restore.StoredItem
	ItemsNext int64
	Status    string
	RestoreAt string
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	ctx := r.Context()
	job, err := s.svc.Job(ctx, id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	d := &jobData{}
	names := map[int64]string{}
	if job.SourceID != nil {
		if src, err := s.svc.Source(ctx, *job.SourceID); err == nil {
			d.Source = &src
			names[src.ID] = src.Name
		}
	}
	if job.PointID != nil {
		if pt, err := s.cat().GetPoint(ctx, *job.PointID); err == nil {
			d.Point = &pt
		}
	}
	d.View = s.viewJob(job, names)
	if d.Events, err = s.svc.JobEvents(ctx, id, 0, 300); err != nil {
		s.handleErr(w, r, err)
		return
	}
	if job.Kind == jobs.KindRestore {
		d.Status = r.FormValue("status")
		if d.Status != "failed" && d.Status != "ok" && d.Status != "skipped" {
			d.Status = ""
		}
		after := intParam(r, "after")
		items, err := s.svc.RestoreItems(ctx, id, d.Status, after, 200)
		if err != nil {
			s.handleErr(w, r, err)
			return
		}
		d.Items = items
		if len(items) == 200 {
			d.ItemsNext = items[len(items)-1].ID
		}
		if d.View.Request != nil {
			d.RestoreAt = s.svc.RestoreRoot()
		}
	}
	p := s.newPage(r, sess, "job.title", "backups")
	p.Data = d
	s.render(w, r, "job", http.StatusOK, p)
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	ok, err := s.svc.CancelJob(r.Context(), s.actor(r, sess), id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if ok {
		s.setFlash(w, "info", "flash.job_cancel_requested")
	}
	http.Redirect(w, r, "/jobs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) cat() *catalog.Store { return s.svc.Catalog() }
