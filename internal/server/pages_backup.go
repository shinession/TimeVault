package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"timevault/internal/access"
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
	// Level is what the visitor may do with the folder.
	Level access.Level
	// CanBackup says that the visitor may back the folder up and verify its recovery points; SignInToBackup that a
	// visitor who has not signed in is offered the sign-in for it, because an account could.
	CanBackup      bool
	SignInToBackup bool
	Active         *jobView
	Attempts       []catalog.Point
	// Minutes is the interval of the schedule; 0 when there is none.
	Minutes int
	// NextRun is when the schedule starts the next backup; nil without a schedule or when the source is switched off.
	NextRun *time.Time
}

type backupsData struct {
	Sources        []sourceCard
	Jobs           []jobView
	RestoreEnabled bool
	// RestoreRoot is the restore folder as people reach it over the network, empty when they cannot be told;
	// RestoreReal is the folder on the server's disk, for administrators.
	RestoreRoot string
	RestoreReal string
}

func (s *Server) backupsData(r *http.Request, sess auth.Session) (*backupsData, error) {
	ctx := r.Context()
	signedIn, view := sess.SignedIn(), s.viewerFor(ctx, sess)
	sources, err := s.visibleSources(ctx, sess)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, src := range sources {
		names[src.ID] = src.Name
	}
	filter := jobs.Filter{Limit: 25}
	filter.OnlySources, filter.SourceIDs = restrictTo(sess, sources)
	recent, err := s.svc.Jobs(ctx, filter)
	if err != nil {
		return nil, err
	}
	d := &backupsData{RestoreEnabled: s.svc.RestoreEnabled()}
	d.RestoreRoot, d.RestoreReal = view.restoreRoot()
	active := map[int64]*jobView{}
	for _, j := range recent {
		// Restores are for accounts to see, like their job pages.
		if j.Kind == jobs.KindRestore && !signedIn {
			continue
		}
		v := s.viewJob(view.job(j), names)
		d.Jobs = append(d.Jobs, v)
		if j.Kind == jobs.KindBackup && j.Active() && j.SourceID != nil {
			if _, ok := active[*j.SourceID]; !ok {
				vc := v
				active[*j.SourceID] = &vc
			}
		}
	}
	now := time.Now()
	for _, src := range sources {
		attempts, err := s.svc.Attempts(ctx, src.ID, 12)
		if err != nil {
			return nil, err
		}
		for i := range attempts {
			attempts[i] = view.point(attempts[i])
		}
		card := sourceCard{SourceSummary: src, Active: active[src.ID], Attempts: attempts, NextRun: nextRun(src.Source, now)}
		card.Level = levelOn(sess, src.Source)
		card.CanBackup = card.Level >= access.Backup
		card.SignInToBackup = !signedIn && src.Access.OffersAccounts(access.Backup)
		if src.ScheduleMinutes != nil {
			card.Minutes = *src.ScheduleMinutes
		}
		d.Sources = append(d.Sources, card)
	}
	return d, nil
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	d, err := s.backupsData(r, sess)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "backups.title", "backups")
	p.Data = d
	s.render(w, r, "backups", http.StatusOK, p)
}

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	src, ok := s.source(w, r, sess, intParam(r, "source_id"), access.Backup)
	if !ok {
		return
	}
	job, created, err := s.svc.StartBackup(r.Context(), s.actor(r, sess), src.ID, r.PostFormValue("rehash") != "")
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if !created {
		// The job that is already there may be an automatic one that is forgotten when it finds nothing changed,
		// so its page is not promised; the Backups page shows it for as long as it exists.
		s.setFlash(w, "info", "flash.backup_already")
		http.Redirect(w, r, "/backups", http.StatusSeeOther)
		return
	}
	s.setFlash(w, "ok", "flash.backup_queued")
	http.Redirect(w, r, "/jobs/"+strconv.FormatInt(job.ID, 10), http.StatusSeeOther)
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	src, ok := s.source(w, r, sess, intParam(r, "source_id"), access.Backup)
	if !ok {
		return
	}
	job, created, err := s.svc.StartVerify(r.Context(), s.actor(r, sess), src.ID, intParam(r, "point_id"), r.PostFormValue("deep") != "")
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
	View   jobView
	Source *catalog.Source
	// CanCancel says that the visitor may stop the job: the job is still running and they may back the folder up,
	// or it is their own restore.
	CanCancel bool
	Point     *catalog.Point
	Events    []jobs.Event
	Items     []restore.StoredItem
	ItemsNext int64
	Status    string
	// Dest is where a restore wrote, as people reach it over the network; DestReal is the folder on the server's
	// disk, for administrators.
	Dest     string
	DestReal string
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
	// The job of a folder the visitor may not browse is not there for them.
	src, err := s.jobSource(ctx, sess, job, access.Browse)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	// A restore job shows where files were written and why some were not, so it is for accounts only.
	if job.Kind == jobs.KindRestore {
		verified, ok := s.requireSignIn(w, r)
		if !ok {
			return
		}
		sess = verified
	}
	// Error texts and the log can name folders and settings of the server; only accounts see them, and only
	// administrators see the folders on the server's disk in them.
	view := s.viewerFor(ctx, sess)
	if !sess.SignedIn() {
		job.Error = ""
	}
	job = view.job(job)
	d := &jobData{Source: src}
	names := map[int64]string{}
	if src != nil {
		names[src.ID] = src.Name
		d.CanCancel = job.Active() && mayCancel(sess, levelOn(sess, *src), job)
	} else if isAdmin(sess) {
		d.CanCancel = job.Active()
	}
	if job.PointID != nil {
		if pt, err := s.cat().GetPoint(ctx, *job.PointID); err == nil {
			d.Point = &pt
		}
	}
	d.View = s.viewJob(job, names)
	if sess.SignedIn() {
		events, err := s.svc.JobEvents(ctx, id, 0, 300)
		if err != nil {
			s.handleErr(w, r, err)
			return
		}
		d.Events = view.events(events)
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
		d.Items = view.restoreItems(items)
		if len(items) == 200 {
			d.ItemsNext = items[len(items)-1].ID
		}
		if d.View.Request != nil {
			d.Dest, d.DestReal = view.destination(d.View.Request.DestName)
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
	ctx := r.Context()
	job, err := s.svc.Job(ctx, id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	src, err := s.jobSource(ctx, sess, job, access.Browse)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	level := access.Full
	if src != nil {
		level = levelOn(sess, *src)
	}
	if !mayCancel(sess, level, job) {
		s.levelDenied(w, r, sess, job.SourceID, access.Backup)
		return
	}
	ok, err := s.svc.CancelJob(ctx, s.actor(r, sess), id)
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
