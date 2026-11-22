// Package service is the application layer shared by the JSON API and the web
// UI: it orchestrates the catalog, backup engine, restore manager, job queue,
// search provider and audit trail, so both front ends behave identically.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/access"
	"timevault/internal/audit"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/pathutil"
	"timevault/internal/repository"
	"timevault/internal/restore"
	"timevault/internal/search"
	"timevault/internal/weblog"
)

// ErrNotFound is returned for unknown sources, points, jobs and paths.
var ErrNotFound = errors.New("not found")

// ErrForbidden is returned when a visitor who can see a monitored folder is not allowed to do what was asked of it.
var ErrForbidden = errors.New("you are not allowed to do this")

// ErrSourceDisabled is returned when a backup is requested for a monitored folder that is switched off.
var ErrSourceDisabled = errors.New("the monitored folder is switched off")

// Actor identifies who performs an operation, for the audit trail.
type Actor struct {
	User   string
	Remote string
}

// DirectoryChecker probes the user directory that sign-ins are checked against.
type DirectoryChecker interface {
	Ping(ctx context.Context) error
	// Addr is the address of the directory server, for display.
	Addr() string
}

// Config wires a Service.
type Config struct {
	Pool    *pgxpool.Pool
	Catalog *catalog.Store
	Repo    *repository.Store
	Engine  *backup.Engine
	Restore *restore.Manager
	Jobs    *jobs.Runner
	Audit   *audit.Logger
	Search  search.Provider
	Logger  *slog.Logger
	// Directory is optional; without it Health reports no user directory.
	Directory DirectoryChecker
	// SchedulerTick is how often RunScheduler looks for due sources; zero means 15 seconds.
	SchedulerTick time.Duration
	// Scheduler spreads the automatic backups out; the zero value starts every due source without any spacing.
	Scheduler SchedulerOptions
	// ServerHost is the name of this server as users type it in \\server\share paths (TIMEVAULT_SERVER_HOST).
	// When it is set, the display path of every monitored folder is required and must use it.
	ServerHost string
	// RestoreDisplayRoot is the network path users reach the restore folder by, normalised
	// (TIMEVAULT_RESTORE_DISPLAY_ROOT); empty when there is none.
	RestoreDisplayRoot string
	// WebLog takes the lines the scheduler writes to the web log (a backup queued, a start that failed); nil means
	// none. What a backup does when it finishes reaches the web log through the audit trail.
	WebLog weblog.Sink
}

// Service is the application facade. It embeds the Reader of its own server, so the reads of the catalog, the jobs and the
// audit trail (Sources, Points, ListHistory, Jobs, AuditEvents and the rest of reader.go) are its methods too.
type Service struct {
	*Reader
	pool               *pgxpool.Pool
	cat                *catalog.Store
	repo               *repository.Store
	engine             *backup.Engine
	restore            *restore.Manager
	jobs               *jobs.Runner
	audit              *audit.Logger
	search             search.Provider
	log                *slog.Logger
	web                weblog.Sink
	directory          DirectoryChecker
	schedulerTick      time.Duration
	sched              SchedulerOptions
	pace               pace
	serverHost         string
	restoreDisplayRoot string
	folders            folderInfos
	// probeDownUntil is when the search engine is asked about changes again after it failed to answer (unix
	// nanoseconds).
	probeDownUntil atomic.Int64
	// pruning holds the folders whose older versions are being deleted; sweepPending says that repository content
	// nobody needs any more is still to be removed, and sweeping that a sweep is running.
	pruning      sync.Map
	sweepPending atomic.Bool
	sweeping     atomic.Bool
}

// New builds a Service and registers the job handlers on the runner.
func New(cfg Config) *Service {
	s := &Service{
		pool: cfg.Pool, cat: cfg.Catalog, repo: cfg.Repo, engine: cfg.Engine, restore: cfg.Restore,
		jobs: cfg.Jobs, audit: cfg.Audit, search: cfg.Search, log: cfg.Logger, web: cfg.WebLog,
		directory: cfg.Directory, schedulerTick: cfg.SchedulerTick, sched: cfg.Scheduler,
		serverHost: cfg.ServerHost, restoreDisplayRoot: cfg.RestoreDisplayRoot,
	}
	if s.search == nil {
		s.search = search.NewDisabled()
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	s.Reader = &Reader{
		pool: cfg.Pool, cat: cfg.Catalog, audit: cfg.Audit,
		// The content of a version is looked for in the repository of this server.
		present: func(sha []byte) bool {
			hash, err := repository.HashFromBytes(sha)
			if err != nil {
				return false
			}
			_, err = s.repo.Stat(hash)
			return err == nil
		},
		roots: func() Roots {
			return Roots{Repository: s.repo.Root(), Restore: s.restore.Root(), RestoreDisplay: s.restoreDisplayRoot}
		},
	}
	s.jobs.Handle(jobs.KindBackup, s.runBackup)
	s.jobs.Handle(jobs.KindVerify, s.runVerify)
	s.jobs.Handle(jobs.KindRestore, s.runRestore)
	s.jobs.Handle(jobs.KindPrune, s.runPrune)
	return s
}

// Catalog exposes the catalog for read-only queries by the front ends.
func (s *Service) Catalog() *catalog.Store { return s.cat }

// RestoreEnabled reports whether a restore folder is configured.
func (s *Service) RestoreEnabled() bool { return s.restore.Enabled() }

// RestoreRoot is the folder restores are written below.
func (s *Service) RestoreRoot() string { return s.restore.Root() }

// ServerHost is the name users know this server by in \\server\share paths, or "" when it is not configured.
func (s *Service) ServerHost() string { return s.serverHost }

// RestoreDisplayRoot is the network path users reach the restore folder by, or "" when there is none.
func (s *Service) RestoreDisplayRoot() string { return s.restoreDisplayRoot }

// SearchProvider is the configured search provider.
func (s *Service) SearchProvider() search.Provider { return s.search }

func (s *Service) record(ctx context.Context, a Actor, action, targetType, targetID string, sourceID *int64, result string, detail map[string]any) error {
	return s.recordTimed(ctx, a, action, targetType, targetID, sourceID, result, detail, 0)
}

// recordTimed is record for an operation that ran for took; zero means it has no duration.
func (s *Service) recordTimed(ctx context.Context, a Actor, action, targetType, targetID string, sourceID *int64, result string, detail map[string]any, took time.Duration) error {
	return s.audit.Record(ctx, audit.Event{
		Actor: a.User, Action: action, TargetType: targetType, TargetID: targetID,
		SourceID: sourceID, Result: result, RemoteAddr: a.Remote, Detail: detail, Duration: took,
	})
}

// Audit writes an event on behalf of the HTTP layer (sign-ins, denials).
func (s *Service) Audit(ctx context.Context, e audit.Event) { _ = s.audit.Record(ctx, e) }

// --- Sources -------------------------------------------------------------

// AddSource validates and registers a source. See withDisplayPath for the rules of its display path.
func (s *Service) AddSource(ctx context.Context, a Actor, req backup.SourceRequest) (catalog.Source, error) {
	req, err := s.withDisplayPath(req)
	if err != nil {
		return catalog.Source{}, err
	}
	ns, err := s.engine.PrepareSource(ctx, req, a.User)
	if err != nil {
		return catalog.Source{}, err
	}
	src, err := s.cat.CreateSource(ctx, ns)
	if err != nil {
		return catalog.Source{}, err
	}
	id := src.ID
	detail := map[string]any{"name": src.Name, "root": src.RootPath, "excludes": src.Excludes}
	if src.ScheduleMinutes != nil {
		detail["schedule_minutes"] = *src.ScheduleMinutes
	}
	if src.DisplayPath != "" {
		detail["display"] = src.DisplayPath
	}
	if src.MaxVersions != nil {
		detail["max_versions"] = *src.MaxVersions
	}
	detail["access"] = src.Access.Spec()
	_ = s.record(ctx, a, audit.ActionSourceCreate, "source", fmt.Sprint(id), &id, audit.ResultOK, detail)
	return src, nil
}

// UpdateSource replaces the name, folder, exclude patterns, schedule, display path and enabled
// flag of a source. The folder of a source that already has recovery points is
// fixed, because its history describes that folder; the display path can change
// at any time. An update that changes nothing succeeds without an audit record;
// otherwise the record lists only the fields that changed, with their old and
// new values.
func (s *Service) UpdateSource(ctx context.Context, a Actor, id int64, req backup.SourceRequest, enabled bool) (catalog.Source, error) {
	cur, err := s.Source(ctx, id)
	if err != nil {
		return catalog.Source{}, err
	}
	req, err = s.withDisplayPath(req)
	if err != nil {
		return catalog.Source{}, err
	}
	ns, err := s.engine.PrepareSourceUpdate(ctx, cur, req)
	if err != nil {
		return catalog.Source{}, err
	}
	// While the older versions of the folder are being deleted the limit cannot change: the run that is under way
	// would go on with the old one.
	if !sameLimit(cur.MaxVersions, ns.MaxVersions) && s.pruneActive(ctx, id) {
		return catalog.Source{}, ErrPruneRunning
	}
	if err := s.checkLowering(ctx, id, cur, ns.MaxVersions, req.ConfirmPrune); err != nil {
		return catalog.Source{}, err
	}
	if catalog.RootKey(ns.RootPath) != catalog.RootKey(cur.RootPath) {
		// A backup that starts while the folder is being changed would record the files of the old folder for the
		// new one. The writer lock of the source is held from this check until the change is stored; it is busy
		// (ErrBusy) while a backup of the source runs.
		lock, err := s.cat.LockSource(ctx, id)
		if err != nil {
			return catalog.Source{}, err
		}
		defer lock.Unlock()
		has, err := s.cat.HasPoints(ctx, id)
		if err != nil {
			return catalog.Source{}, err
		}
		if has {
			return catalog.Source{}, fmt.Errorf("%w: the folder of a source that already has backups cannot be changed; disable it and add the new folder as a new source", backup.ErrInvalidSource)
		}
	}
	// The grants that this change replaces are the ones stored when it is applied, read under the lock of the source
	// in its transaction, not the ones read above: the record of the change must not blame it for a change of the
	// grants (or of the folder) that somebody else made meanwhile.
	grantsBefore, haveBefore := cur.Access, false
	authorize := func(current access.List) error {
		grantsBefore, haveBefore = current, true
		if req.Authorize != nil {
			return req.Authorize(current)
		}
		return nil
	}
	src, err := s.cat.UpdateSource(ctx, id, catalog.SourceUpdate{
		Name: ns.Name, RootPath: ns.RootPath, KeepRoot: req.KeepRoot, Excludes: ns.Excludes, Enabled: enabled, ScheduleMinutes: ns.ScheduleMinutes,
		DisplayPath: ns.DisplayPath, MaxVersions: ns.MaxVersions, Access: req.Access, Authorize: authorize,
	})
	if errors.Is(err, catalog.ErrNotFound) {
		return catalog.Source{}, ErrNotFound
	}
	if err != nil {
		return catalog.Source{}, err
	}
	changed := src
	if req.KeepRoot {
		changed.RootPath = cur.RootPath
	}
	if haveBefore {
		cur.Access = grantsBefore
	}
	if changes := sourceChanges(cur, changed); len(changes) > 0 {
		_ = s.record(ctx, a, audit.ActionSourceUpdate, "source", fmt.Sprint(id), &id, audit.ResultOK, changes)
	}
	// A lower limit takes effect at once, not only with the next backup.
	if limitLowered(cur.MaxVersions, src.MaxVersions) {
		if has, err := s.cat.HasPoints(ctx, id); err == nil && has {
			if _, _, err := s.StartPrune(ctx, a, id); err != nil {
				s.log.Warn("queue the deletion of old versions", "source", id, "error", err)
			}
		}
	}
	return src, nil
}

// sourceChanges describes what an edit changed, as old and new values per field.
func sourceChanges(before, after catalog.Source) map[string]any {
	changes := map[string]any{}
	change := func(field string, from, to any) { changes[field] = map[string]any{"old": from, "new": to} }
	if before.Name != after.Name {
		change("name", before.Name, after.Name)
	}
	if before.RootPath != after.RootPath {
		change("root", before.RootPath, after.RootPath)
	}
	if !slices.Equal(before.Excludes, after.Excludes) {
		change("excludes", before.Excludes, after.Excludes)
	}
	if before.Enabled != after.Enabled {
		change("enabled", before.Enabled, after.Enabled)
	}
	if (before.ScheduleMinutes == nil) != (after.ScheduleMinutes == nil) ||
		(before.ScheduleMinutes != nil && *before.ScheduleMinutes != *after.ScheduleMinutes) {
		change("schedule_minutes", before.ScheduleMinutes, after.ScheduleMinutes)
	}
	if before.DisplayPath != after.DisplayPath {
		change("display", before.DisplayPath, after.DisplayPath)
	}
	if (before.MaxVersions == nil) != (after.MaxVersions == nil) ||
		(before.MaxVersions != nil && *before.MaxVersions != *after.MaxVersions) {
		change("max_versions", before.MaxVersions, after.MaxVersions)
	}
	if !before.Access.Equal(after.Access) {
		change("access", before.Access.Spec(), after.Access.Spec())
	}
	return changes
}

// --- Jobs --------------------------------------------------------------------

type backupParams struct {
	SourceID int64 `json:"source_id"`
	Rehash   bool  `json:"rehash"`
	// Scheduled marks a run started by the scheduler; it records nothing when
	// the source has not changed since the last recovery point.
	Scheduled bool `json:"scheduled"`
}

type verifyParams struct {
	PointID int64 `json:"point_id"`
	Deep    bool  `json:"deep"`
}

type restoreParams struct {
	Request restore.Request `json:"request"`
	Actor   string          `json:"actor"`
	Remote  string          `json:"remote"`
	Digest  string          `json:"plan_digest"`
	Totals  restore.Totals  `json:"plan_totals"`
}

// StartBackup queues a backup of a source. created is false when one is
// already queued or running for it.
func (s *Service) StartBackup(ctx context.Context, a Actor, sourceID int64, rehash bool) (job jobs.Job, created bool, err error) {
	src, err := s.Source(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if !src.Enabled {
		return jobs.Job{}, false, ErrSourceDisabled
	}
	job, created, err = s.jobs.Enqueue(ctx, jobs.NewJob{
		Kind: jobs.KindBackup, SourceID: &sourceID, Params: backupParams{SourceID: sourceID, Rehash: rehash}, RequestedBy: a.User,
	})
	if err != nil {
		return job, false, err
	}
	if created {
		_ = s.record(ctx, a, audit.ActionBackupRequest, "source", fmt.Sprint(sourceID), &sourceID, audit.ResultOK,
			map[string]any{"job": job.ID, "rehash": rehash})
	}
	return job, created, nil
}

// StartVerify queues an integrity check of a recovery point.
func (s *Service) StartVerify(ctx context.Context, a Actor, sourceID, pointID int64, deep bool) (job jobs.Job, created bool, err error) {
	p, err := s.Point(ctx, sourceID, pointID)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if p.State != catalog.StateCommitted {
		return jobs.Job{}, false, errors.New("only committed recovery points can be verified")
	}
	if s.pruneActive(ctx, sourceID) {
		return jobs.Job{}, false, ErrPruneRunning
	}
	job, created, err = s.jobs.Enqueue(ctx, jobs.NewJob{
		Kind: jobs.KindVerify, SourceID: &sourceID, PointID: &pointID,
		Params: verifyParams{PointID: pointID, Deep: deep}, RequestedBy: a.User,
	})
	if err != nil {
		return job, false, err
	}
	if created {
		_ = s.record(ctx, a, audit.ActionVerifyRequest, "point", fmt.Sprint(pointID), &sourceID, audit.ResultOK,
			map[string]any{"job": job.ID, "deep": deep, "seq": p.Seq})
	}
	return job, created, nil
}

// CancelJob asks a job to stop. The audit record belongs to the folder of the job, so that it is shown to the same
// people as the rest of that folder's events.
func (s *Service) CancelJob(ctx context.Context, a Actor, jobID int64) (bool, error) {
	job, err := s.Job(ctx, jobID)
	if err != nil {
		return false, err
	}
	ok, err := s.jobs.Cancel(ctx, jobID)
	if err != nil {
		return false, err
	}
	if ok {
		_ = s.record(ctx, a, audit.ActionJobCancel, "job", fmt.Sprint(jobID), job.SourceID, audit.ResultOK, nil)
	}
	return ok, nil
}

type backupReporter struct{ rep *jobs.Reporter }

func (r backupReporter) Phase(name string)           { r.rep.SetPhase(name) }
func (r backupReporter) Progress(p backup.Progress)  { r.rep.SetProgress(p) }
func (r backupReporter) Event(level, message string) { r.rep.Event(level, message) }

type restoreReporter struct{ rep *jobs.Reporter }

func (r restoreReporter) Phase(name string)           { r.rep.SetPhase(name) }
func (r restoreReporter) Progress(p restore.Progress) { r.rep.SetProgress(p) }
func (r restoreReporter) Event(level, message string) { r.rep.Event(level, message) }

func (s *Service) runBackup(ctx context.Context, job jobs.Job, rep *jobs.Reporter) error {
	var p backupParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return fmt.Errorf("invalid job parameters: %w", err)
	}
	started := time.Now()
	var items *int64
	if s.sched.Probe {
		if src, err := s.cat.GetSource(ctx, p.SourceID); err == nil {
			items = s.probeItems(ctx, src.RootPath)
		}
	}
	point, err := s.engine.Run(ctx, p.SourceID, backup.RunOptions{Rehash: p.Rehash, SkipUnchanged: p.Scheduled}, backupReporter{rep})
	took := time.Since(started)
	end := catalog.RunEnd{Duration: took}
	if err == nil || errors.Is(err, backup.ErrUnchanged) {
		end.WalkStarted, end.WalkItems = started, items
	}
	s.recordRun(ctx, p.SourceID, end)
	if p.Scheduled {
		s.pace.ended(time.Now(), took)
	}
	if errors.Is(err, backup.ErrUnchanged) {
		return jobs.ErrForget
	}
	actor := Actor{User: job.RequestedBy}
	sourceID := p.SourceID
	if err != nil {
		_ = s.recordTimed(ctx, actor, audit.ActionBackupFinish, "source", fmt.Sprint(sourceID), &sourceID, audit.ResultFailed,
			map[string]any{"job": job.ID, "error": truncate(err.Error(), 300)}, took)
		return err
	}
	if _, err := s.pool.Exec(context.WithoutCancel(ctx), `UPDATE jobs SET point_id = $2 WHERE id = $1`, job.ID, point.ID); err != nil {
		s.log.Warn("link job to its recovery point", "job", job.ID, "error", err)
	}
	_ = s.recordTimed(ctx, actor, audit.ActionBackupFinish, "point", fmt.Sprint(point.ID), &sourceID, audit.ResultOK,
		map[string]any{"job": job.ID, "seq": point.Seq, "files": point.FileCount, "added": point.AddedCount,
			"modified": point.ModifiedCount, "deleted": point.DeletedCount, "skipped": point.SkippedCount,
			"bytes": point.TotalBytes, "new_bytes": point.NewObjectBytes, "new_objects": point.NewObjectCount}, took)
	// The folder's limit of versions per file is applied right after the backup that may have gone over it. The
	// duration recorded above is the backup's own.
	s.pruneAfterBackup(ctx, rep, actor, sourceID, point.Seq)
	return nil
}

func (s *Service) runVerify(ctx context.Context, job jobs.Job, rep *jobs.Reporter) error {
	var p verifyParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return fmt.Errorf("invalid job parameters: %w", err)
	}
	if err := s.waitWhilePruning(ctx, rep, job.SourceID); err != nil {
		return err
	}
	started := time.Now()
	res, err := s.engine.Verify(ctx, p.PointID, p.Deep, backupReporter{rep})
	actor := Actor{User: job.RequestedBy}
	if err != nil {
		_ = s.recordTimed(ctx, actor, audit.ActionVerifyFinish, "point", fmt.Sprint(p.PointID), job.SourceID, audit.ResultFailed,
			map[string]any{"job": job.ID, "error": truncate(err.Error(), 300)}, time.Since(started))
		return err
	}
	for _, problem := range res.Problems {
		rep.Event("error", problem)
	}
	rep.Event("info", fmt.Sprintf("checked %d object(s): %d missing, %d corrupt, catalog manifest %s",
		res.Objects, res.Missing, res.Corrupt, manifestText(res)))
	result := audit.ResultOK
	if !res.OK() {
		result = audit.ResultFailed
	}
	_ = s.recordTimed(ctx, actor, audit.ActionVerifyFinish, "point", fmt.Sprint(p.PointID), job.SourceID, result,
		map[string]any{"job": job.ID, "deep": p.Deep, "objects": res.Objects, "missing": res.Missing, "corrupt": res.Corrupt, "manifest_ok": res.ManifestOK},
		time.Since(started))
	if !res.OK() {
		return fmt.Errorf("integrity check failed: %d missing, %d corrupt object(s)%s", res.Missing, res.Corrupt,
			choose(res.ManifestOK, "", ", catalog manifest mismatch"))
	}
	return nil
}

func choose(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// manifestText says how the catalog manifest of a verified point came out.
func manifestText(res backup.VerifyResult) string {
	if res.ManifestSkipped {
		return "was not compared: the recovery point is partial because old versions were deleted"
	}
	return choose(res.ManifestOK, "matches", "DOES NOT MATCH")
}

// --- Restore -----------------------------------------------------------------

// PreviewRestore builds the dry-run plan and the confirmation token for it.
func (s *Service) PreviewRestore(ctx context.Context, a Actor, req restore.Request) (restore.Plan, string, error) {
	if err := s.checkRestorePoint(ctx, req); err != nil {
		return restore.Plan{}, "", err
	}
	plan, err := s.restore.Preview(ctx, req)
	if err != nil {
		return plan, "", err
	}
	sourceID := req.SourceID
	_ = s.record(ctx, a, audit.ActionRestorePreview, "point", fmt.Sprint(req.PointID), &sourceID, audit.ResultOK,
		map[string]any{"paths": plan.Request.Paths, "dest": plan.Request.DestName, "items": plan.Totals.Items,
			"blocked": plan.Totals.Blocked, "overwrite": plan.Totals.Overwrite})
	return plan, s.restore.IssueToken(plan, a.User), nil
}

// checkRestorePoint answers a recovery point that is not one of the folder like one that does not exist (as Point
// does for the history pages), so that the answer of a restore cannot tell a visitor that a number is a point of a
// folder they may not browse. The restore package itself calls that an invalid request.
func (s *Service) checkRestorePoint(ctx context.Context, req restore.Request) error {
	_, err := s.Point(ctx, req.SourceID, req.PointID)
	return err
}

// StartRestore queues a confirmed restore. Without a valid token for exactly
// this plan, or without an explicit overwrite confirmation when files would be
// replaced, nothing is queued. The audit record is written first: a restore
// that cannot be audited does not run.
func (s *Service) StartRestore(ctx context.Context, a Actor, req restore.Request, token, idempotencyKey string, confirmOverwrite bool) (jobs.Job, error) {
	// A repeated submission (double click, retry) returns the job it created the first
	// time. By then the destination has changed, so re-checking the preview would only fail. The key is chosen
	// by the caller, so it only finds the caller's own restore of this folder: a key that another account or
	// another folder has used is refused, instead of handing out that job.
	if idempotencyKey != "" {
		existing, found, err := s.jobs.FindByKey(ctx, jobs.KindRestore, idempotencyKey)
		if err != nil {
			return jobs.Job{}, err
		}
		if found {
			if !ownRestore(existing, a, req.SourceID) {
				return jobs.Job{}, errKeyUsed
			}
			return existing, nil
		}
	}
	if err := s.checkRestorePoint(ctx, req); err != nil {
		return jobs.Job{}, err
	}
	plan, err := s.restore.Confirm(ctx, req, token, a.User)
	if err != nil {
		return jobs.Job{}, err
	}
	if s.pruneActive(ctx, plan.Request.SourceID) {
		return jobs.Job{}, ErrPruneRunning
	}
	if plan.Totals.Overwrite > 0 && !confirmOverwrite {
		return jobs.Job{}, fmt.Errorf("%w: replacing %d existing file(s) must be confirmed explicitly", restore.ErrNotConfirmed, plan.Totals.Overwrite)
	}
	if plan.Totals.Blocked > 0 && !plan.Request.AllowPartial {
		return jobs.Job{}, fmt.Errorf("%w: %d item(s) cannot be restored", restore.ErrBlocked, plan.Totals.Blocked)
	}
	sourceID, pointID := plan.Request.SourceID, plan.Request.PointID
	params := restoreParams{Request: plan.Request, Actor: a.User, Remote: a.Remote, Digest: plan.Digest, Totals: plan.Totals}
	if err := s.record(ctx, a, audit.ActionRestoreRequest, "point", fmt.Sprint(pointID), &sourceID, audit.ResultOK,
		map[string]any{"paths": plan.Request.Paths, "dest": plan.DestDir, "policy": plan.Request.Policy, "items": plan.Totals.Items,
			"bytes": plan.Totals.Bytes, "overwrite": plan.Totals.Overwrite, "blocked": plan.Totals.Blocked,
			"partial_allowed": plan.Request.AllowPartial, "overwrite_confirmed": confirmOverwrite, "plan_digest": plan.Digest}); err != nil {
		return jobs.Job{}, errors.New("the restore was not started because it could not be recorded in the audit log")
	}
	job, created, err := s.jobs.Enqueue(ctx, jobs.NewJob{
		Kind: jobs.KindRestore, SourceID: &sourceID, PointID: &pointID, Params: params,
		RequestedBy: a.User, IdempotencyKey: idempotencyKey,
	})
	if err == nil && !created && !ownRestore(job, a, sourceID) {
		// Two requests with the same key at the same moment: the other one won, and it is not this caller's.
		return jobs.Job{}, errKeyUsed
	}
	return job, err
}

// errKeyUsed is returned when the idempotency key of a restore was used by a restore that is not the caller's own
// of the same folder.
var errKeyUsed = fmt.Errorf("%w: this idempotency key was used by another restore; choose a new one", catalog.ErrConflict)

// ownRestore reports whether job is a restore that actor requested for the source.
func ownRestore(job jobs.Job, actor Actor, sourceID int64) bool {
	return job.Kind == jobs.KindRestore && job.RequestedBy == actor.User && job.SourceID != nil && *job.SourceID == sourceID
}

func (s *Service) runRestore(ctx context.Context, job jobs.Job, rep *jobs.Reporter) error {
	var p restoreParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return fmt.Errorf("invalid job parameters: %w", err)
	}
	if err := s.waitWhilePruning(ctx, rep, &p.Request.SourceID); err != nil {
		return err
	}
	started := time.Now()
	res, err := s.restore.Execute(ctx, job.ID, p.Request, restoreReporter{rep})
	actor := Actor{User: p.Actor, Remote: p.Remote}
	result := audit.ResultOK
	detail := map[string]any{"job": job.ID, "dest": p.Request.DestName, "created": res.Created, "overwritten": res.Overwritten,
		"renamed": res.Renamed, "skipped": res.Skipped, "failed": res.Failed, "bytes": res.Bytes}
	if err != nil {
		result = audit.ResultFailed
		detail["error"] = truncate(err.Error(), 300)
	}
	sourceID := p.Request.SourceID
	_ = s.recordTimed(ctx, actor, audit.ActionRestoreFinish, "point", fmt.Sprint(p.Request.PointID), &sourceID, result, detail, time.Since(started))
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- Search ------------------------------------------------------------------

// SearchHit is one search result relative to a source.
type SearchHit struct {
	Name    string
	Rel     string
	DirRel  string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// SearchPage is one page of search results.
type SearchPage struct {
	Hits       []SearchHit
	Total      int
	NextOffset int
	Dropped    int
	Health     search.Health
}

// ErrSearchUnavailable is returned when the engine is disabled or unreachable;
// the UI then shows a degraded state and browsing keeps working.
var ErrSearchUnavailable = errors.New("search is unavailable")

// Search runs a bounded search confined to a source.
func (s *Service) Search(ctx context.Context, src catalog.Source, text string, offset, limit int) (SearchPage, error) {
	page, err := s.search.Search(ctx, search.Query{Text: text, Root: src.RootPath, Offset: offset, Limit: limit})
	if err != nil {
		health := s.search.Health(ctx)
		switch {
		case errors.Is(err, search.ErrBadQuery):
			return SearchPage{Health: health}, err
		default:
			return SearchPage{Health: health}, fmt.Errorf("%w: %v", ErrSearchUnavailable, err)
		}
	}
	out := SearchPage{Total: page.Total, NextOffset: page.NextOffset, Dropped: page.Dropped, Health: search.Health{State: search.StateOK, CheckedAt: time.Now()}}
	for _, r := range page.Items {
		rel, ok := relativeTo(src.RootPath, r.Path)
		if !ok {
			out.Dropped++
			continue
		}
		hit := SearchHit{Name: r.Name, Rel: rel, DirRel: pathutil.ParentRel(rel), IsDir: r.IsDir, Size: r.Size, ModTime: r.ModTime}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}

func relativeTo(root, full string) (string, bool) {
	if !pathutil.Within(root, full) {
		return "", false
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(full[len(strings.TrimRight(root, `\/`)):], `\`), `/`)
	rel, err := pathutil.CleanRel(trimmed)
	if err != nil {
		return "", false
	}
	return rel, true
}

// --- Health ------------------------------------------------------------------

// Component is the status of one dependency. Detail is English text for the
// API; the web UI shows the translation of Key (with Args) when Key is set.
type Component struct {
	Name   string
	State  string // ok | degraded | disabled | failed
	Detail string
	Key    string
	Args   []any
}

// HealthReport is the overall service status.
type HealthReport struct {
	Components []Component
	Queued     int
	Running    int
	CheckedAt  time.Time
}

// OK reports whether nothing is failed.
func (h HealthReport) OK() bool {
	for _, c := range h.Components {
		if c.State == "failed" {
			return false
		}
	}
	return true
}

// directoryTimeout bounds one reachability check of the user directory.
const directoryTimeout = 3 * time.Second

func (s *Service) pingDirectory(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, directoryTimeout)
	defer cancel()
	return s.directory.Ping(ctx)
}

// Health checks every dependency; it never fails, problems are reported in the result.
func (s *Service) Health(ctx context.Context) HealthReport {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rep := HealthReport{CheckedAt: time.Now()}
	add := func(name, state, detail, key string, args ...any) {
		rep.Components = append(rep.Components, Component{Name: name, State: state, Detail: detail, Key: key, Args: args})
	}

	if err := s.pool.Ping(ctx); err != nil {
		add("database", "failed", "the database does not answer", "db_down")
	} else {
		add("database", "ok", "", "")
	}

	if h := s.repo.Health(); h.OK {
		detail, key, args := h.Path, "", []any(nil)
		if h.TotalBytes > 0 {
			free := int(float64(h.FreeBytes) / float64(h.TotalBytes) * 100)
			detail, key, args = fmt.Sprintf("%s — %d%% free", h.Path, free), "repo_free", []any{h.Path, free}
		}
		state := "ok"
		if h.TotalBytes > 0 && float64(h.FreeBytes)/float64(h.TotalBytes) < 0.05 {
			state = "degraded"
		}
		add("repository", state, detail, key, args...)
	} else {
		add("repository", "failed", h.Detail, "")
	}

	sh := s.search.Health(ctx)
	switch sh.State {
	case search.StateOK:
		detail := sh.Detail
		if detail == "" {
			detail = s.search.Name()
		}
		add("search", "ok", detail, sh.Key, sh.Args...)
	case search.StateDisabled:
		add("search", "disabled", sh.Detail, "search_disabled")
	default:
		add("search", "degraded", sh.Detail, sh.Key, sh.Args...)
	}

	// Browsing and the local administrator work without the directory, so a silent
	// one is degraded, not failed. The error text stays out of the detail: it can
	// carry connection settings.
	if s.directory == nil {
		add("directory", "disabled", "no user directory configured", "directory_off")
	} else {
		addr := s.directory.Addr()
		if err := s.pingDirectory(ctx); err != nil {
			s.log.Debug("user directory check failed", "error", err)
			add("directory", "degraded", "the user directory does not answer", "directory_down")
		} else {
			add("directory", "ok", addr, "directory_ok", addr)
		}
	}

	if s.restore.Enabled() {
		add("restore", "ok", s.restore.Root(), "")
	} else {
		add("restore", "disabled", "no restore folder configured", "restore_disabled")
	}
	add("vss", "disabled", "not implemented in the MVP: recovery points are live scans of the file system", "vss_disabled")

	var queued, running int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state = 'queued'), count(*) FILTER (WHERE state = 'running') FROM jobs`).Scan(&queued, &running)
	rep.Queued, rep.Running = queued, running
	add("jobs", "ok", fmt.Sprintf("%d running, %d queued", running, queued), "jobs_detail", running, queued)
	return rep
}
