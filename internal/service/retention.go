package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"timevault/internal/audit"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/restore"
)

const (
	// pruneHoldPoll is how often a restore or a verification that was queued before a deletion of old versions began
	// looks again whether the deletion is over.
	pruneHoldPoll = time.Second
)

// ErrPruneRunning is returned when a restore or verification is requested for a folder whose older versions are being
// deleted at this moment, and when the version limit of such a folder is to be changed.
var ErrPruneRunning = errors.New("old versions of this folder are being deleted; try again in a minute")

// errFolderBusy means that a restore or a verification of the folder is queued or running. A cleanup that runs inside
// a backup does not wait for it: it leaves the old versions to a cleanup job, which starts when the folder is quiet.
var errFolderBusy = errors.New("a restore or verification of the folder is queued or running")

// PruneNotConfirmedError means that lowering the number of versions kept per file would delete versions for good
// and the request did not say that this is meant.
type PruneNotConfirmedError struct {
	// Files is how many files would lose versions, Versions how many versions in all.
	Files, Versions int64
}

func (e *PruneNotConfirmedError) Error() string {
	return fmt.Sprintf("this limit deletes %d older version(s) of %d file(s) for good; confirm that this is meant", e.Versions, e.Files)
}

type pruneParams struct {
	SourceID int64 `json:"source_id"`
}

// limitLowered reports whether a folder went from keeping more versions of a file to keeping fewer. nil keeps all.
func limitLowered(before, after *int) bool {
	return after != nil && (before == nil || *after < *before)
}

// sameLimit reports whether two version limits are the same; nil keeps every version.
func sameLimit(a, b *int) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// checkLowering refuses to lower the limit of a folder that has history when that would delete versions, unless the
// request confirmed it.
func (s *Service) checkLowering(ctx context.Context, id int64, cur catalog.Source, limit *int, confirmed bool) error {
	if confirmed || !limitLowered(cur.MaxVersions, limit) {
		return nil
	}
	has, err := s.cat.HasPoints(ctx, id)
	if err != nil || !has {
		return err
	}
	preview, err := s.cat.PreviewPrune(ctx, id, *limit)
	if err != nil {
		return err
	}
	if preview.Versions > 0 {
		return &PruneNotConfirmedError{Files: preview.Files, Versions: preview.Versions}
	}
	return nil
}

// StartPrune queues the deletion of the versions that the limit of a folder makes unnecessary. It returns the
// active cleanup of the folder, if there is one, instead of queueing a second. The job waits in the queue, holding
// no worker, until no backup, restore or verification of the folder is running (see jobs.Runner).
func (s *Service) StartPrune(ctx context.Context, a Actor, sourceID int64) (job jobs.Job, created bool, err error) {
	src, err := s.Source(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if src.MaxVersions == nil {
		return jobs.Job{}, false, errors.New("the folder keeps every version; there is nothing to delete")
	}
	return s.jobs.Enqueue(ctx, jobs.NewJob{
		Kind: jobs.KindPrune, SourceID: &sourceID, Params: pruneParams{SourceID: sourceID}, RequestedBy: a.User,
	})
}

// runPrune is the cleanup job of a whole folder. The job queue starts it only while nothing else works on the folder,
// so it neither waits nor has to wait for a backup: it deletes at once.
func (s *Service) runPrune(ctx context.Context, job jobs.Job, rep *jobs.Reporter) error {
	var p pruneParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return fmt.Errorf("invalid job parameters: %w", err)
	}
	if err := s.pruneSource(ctx, rep, Actor{User: job.RequestedBy}, p.SourceID, 0, false); err != nil {
		return err
	}
	rep.SetPhase(restore.PhaseDone)
	return nil
}

// pruneActive reports whether the older versions of a folder are being deleted right now: a cleanup job of the folder
// is running, or a cleanup inside a backup. A cleanup that is only queued does not count, it may wait a long time
// for a backup or a verification that runs.
func (s *Service) pruneActive(ctx context.Context, sourceID int64) bool {
	if _, busy := s.pruning.Load(sourceID); busy {
		return true
	}
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE kind = 'prune' AND source_id = $1 AND state = 'running'`, sourceID).Scan(&n)
	return err == nil && n > 0
}

// restoreOrVerifyActive reports whether a restore or verification of a folder is queued or running.
func (s *Service) restoreOrVerifyActive(ctx context.Context, sourceID int64) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE kind IN ('restore', 'verify') AND source_id = $1 AND state IN ('queued', 'running')`, sourceID).Scan(&n)
	return n > 0, err
}

// waitWhilePruning holds back a restore or a verification that was queued before the deletion of the old versions of
// its folder began, until the deletion is over: it must not start on versions that are about to go. A deletion never
// waits for a restore or verification, so this cannot deadlock; a canceled job stops waiting.
func (s *Service) waitWhilePruning(ctx context.Context, rep *jobs.Reporter, sourceID *int64) error {
	if sourceID == nil {
		return nil
	}
	if _, busy := s.pruning.Load(*sourceID); !busy {
		return nil
	}
	rep.Event("info", "waiting until the old versions of the folder have been deleted")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pruneHoldPoll):
		}
		if _, busy := s.pruning.Load(*sourceID); !busy {
			return nil
		}
	}
}

// pruneSource deletes what the version limit of a folder makes unnecessary: the older versions of its files and then
// the repository content that no version needs any more. since is the sequence number of the recovery point that was
// just committed, so that only the files it changed are looked at, or 0 for every file. insideBackup is set when it
// runs as the last step of a backup: it then gives way to a restore or verification of the folder that is queued or
// running (errFolderBusy) instead of waiting for it. A cleanup job needs no such check, the job queue does not start
// it while one of them runs.
func (s *Service) pruneSource(ctx context.Context, rep *jobs.Reporter, actor Actor, sourceID int64, since int, insideBackup bool) error {
	started := time.Now()
	src, err := s.Source(ctx, sourceID)
	if err != nil {
		return err
	}
	if src.MaxVersions == nil {
		rep.Event("info", "the folder keeps every version; nothing to delete")
		return nil
	}
	limit := *src.MaxVersions

	// The folder is marked before anything else is looked at: a restore or verification that is requested now is
	// turned away, and one that is queued already waits for the deletion to end, rather than starting on versions
	// that are about to go.
	s.pruning.Store(sourceID, true)
	defer s.pruning.Delete(sourceID)

	if insideBackup {
		busy, qerr := s.restoreOrVerifyActive(ctx, sourceID)
		if qerr != nil {
			return qerr
		}
		if busy {
			return errFolderBusy
		}
	}

	res, err := s.engine.Prune(ctx, sourceID, catalog.PruneOptions{Max: limit, SinceSeq: since}, backupReporter{rep})
	detail := map[string]any{"limit": limit, "files": res.Files, "versions": res.Versions, "points_partial": res.PointsMarked}
	if since > 0 {
		detail["since_seq"] = since
	}
	if err != nil {
		detail["error"] = truncate(err.Error(), 300)
		_ = s.recordTimed(ctx, actor, audit.ActionRetentionPrune, "source", fmt.Sprint(sourceID), &sourceID, audit.ResultFailed, detail, time.Since(started))
		return err
	}
	if res.Versions > 0 {
		// Content only they referred to can go now. A sweep that cannot run right away is left for the scheduler.
		sweep, serr := s.engine.SweepObjects(ctx, backupReporter{rep})
		switch {
		case errors.Is(serr, backup.ErrSweepBusy):
			s.sweepPending.Store(true)
			rep.Event("info", "freeing space is postponed until no backup is running")
		case serr != nil:
			s.log.Warn("sweep the repository after deleting old versions", "source", sourceID, "error", serr)
			s.sweepPending.Store(true)
		default:
			detail["objects_freed"], detail["bytes_freed"] = sweep.Objects, sweep.Bytes
			if sweep.More {
				s.sweepPending.Store(true)
			}
		}
	}
	if res.Versions > 0 || since == 0 {
		_ = s.recordTimed(ctx, actor, audit.ActionRetentionPrune, "source", fmt.Sprint(sourceID), &sourceID, audit.ResultOK, detail, time.Since(started))
	}
	return nil
}

// pruneAfterBackup applies the version limit of a folder to the files its newest recovery point changed. It never
// fails the backup. When it cannot do its work, because a restore or verification of the folder is queued or running
// or because something went wrong, the next backup would not make up for it, as it only looks at the files it changed
// itself: a cleanup of the whole folder is queued instead, which survives a restart and runs as soon as nothing else
// works on the folder.
func (s *Service) pruneAfterBackup(ctx context.Context, rep *jobs.Reporter, actor Actor, sourceID int64, seq int) {
	src, err := s.cat.GetSource(ctx, sourceID)
	if err != nil || src.MaxVersions == nil {
		return
	}
	if err := s.pruneSource(ctx, rep, actor, sourceID, seq, true); err != nil {
		level, reason := "warn", "the old versions could not be deleted now: "+truncate(err.Error(), 200)
		if errors.Is(err, errFolderBusy) {
			level, reason = "info", "a restore or verification of the folder is queued or running, so the old versions are not deleted now"
		} else if ctx.Err() == nil {
			s.log.Warn("delete the old versions after a backup", "source", sourceID, "error", err)
		}
		queueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, _, qerr := s.StartPrune(queueCtx, actor, sourceID); qerr != nil {
			s.log.Warn("queue the deletion of old versions", "source", sourceID, "error", qerr)
			rep.Event("warn", reason+"; the cleanup of the whole folder could not be queued: "+truncate(qerr.Error(), 200))
		} else {
			rep.Event(level, reason+"; a cleanup of the whole folder is queued and runs when nothing else works on the folder")
		}
	}
	rep.SetPhase(backup.PhaseCommitted)
}

// sweepIfPending removes repository content nobody needs any more when an earlier sweep could not finish, as soon as
// no backup is running. It runs in the background and never more than once at a time.
func (s *Service) sweepIfPending(ctx context.Context) {
	if !s.sweepPending.Load() || !s.sweeping.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.sweeping.Store(false)
		started := time.Now()
		stats, err := s.engine.SweepObjects(ctx, nil)
		switch {
		case errors.Is(err, backup.ErrSweepBusy):
			return // a backup is running; the next tick tries again
		case err != nil:
			if ctx.Err() == nil {
				s.log.Warn("sweep the repository", "error", err)
			}
			s.sweepPending.Store(false)
			return
		}
		s.sweepPending.Store(stats.More)
		if stats.Objects > 0 {
			_ = s.recordTimed(ctx, Actor{User: schedulerActor}, audit.ActionRetentionPrune, "repository", "", nil, audit.ResultOK,
				map[string]any{"objects_freed": stats.Objects, "bytes_freed": stats.Bytes}, time.Since(started))
		}
	}()
}
