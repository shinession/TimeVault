package service

import (
	"context"
	"time"

	"timevault/internal/jobs"
)

const (
	// schedulerActor is recorded as the requester of backups the scheduler starts.
	schedulerActor       = "scheduler"
	defaultSchedulerTick = 15 * time.Second
	schedulerWarnEvery   = time.Minute
	markRunTimeout       = 10 * time.Second
)

// StartScheduledBackup queues an automatic backup of a source. Like StartBackup it
// returns the active job instead (created is then false) when one is already queued
// or running. The request is not audited; the outcome of the run is, except that a
// run which finds nothing changed leaves neither a job nor an audit record.
func (s *Service) StartScheduledBackup(ctx context.Context, sourceID int64) (job jobs.Job, created bool, err error) {
	src, err := s.Source(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if !src.Enabled {
		return jobs.Job{}, false, ErrSourceDisabled
	}
	return s.jobs.Enqueue(ctx, jobs.NewJob{
		Kind: jobs.KindBackup, SourceID: &sourceID,
		Params: backupParams{SourceID: sourceID, Scheduled: true}, RequestedBy: schedulerActor,
	})
}

// RunScheduler queues a backup for every source whose schedule says one is due. It
// looks at once and then every tick, and returns when ctx ends. Every backup run,
// whatever its outcome, restarts the interval of its source, so a source that keeps
// failing is retried once per interval. Start it once, after the job runner.
func (s *Service) RunScheduler(ctx context.Context) {
	tick := s.schedulerTick
	if tick <= 0 {
		tick = defaultSchedulerTick
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	lastWarn := map[int64]time.Time{}
	for {
		s.queueDue(ctx, lastWarn)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// queueDue starts the scheduled backups that are due. Problems are logged, at most
// once per source and minute, and never stop the scheduler.
func (s *Service) queueDue(ctx context.Context, lastWarn map[int64]time.Time) {
	warn := func(sourceID int64, msg string, err error) {
		if ctx.Err() != nil || time.Since(lastWarn[sourceID]) < schedulerWarnEvery {
			return
		}
		lastWarn[sourceID] = time.Now()
		s.log.Warn(msg, "source", sourceID, "error", err)
	}
	due, err := s.cat.DueSources(ctx)
	if err != nil {
		warn(0, "look for scheduled backups", err)
		return
	}
	for _, id := range due {
		// A source stays due until its run ends, so a long backup would otherwise make
		// every tick try a job that the database refuses.
		if s.backupActive(ctx, id) {
			continue
		}
		if _, _, err := s.StartScheduledBackup(ctx, id); err != nil {
			warn(id, "start a scheduled backup", err)
		}
	}
}

// backupActive reports whether a backup of the source is queued or running.
func (s *Service) backupActive(ctx context.Context, sourceID int64) bool {
	newest, err := jobs.List(ctx, s.pool, jobs.Filter{SourceID: &sourceID, Kind: jobs.KindBackup, Limit: 1})
	return err == nil && len(newest) == 1 && newest[0].Active()
}

// markRun restarts the schedule interval of a source whose backup run just ended.
// The job's context may already be canceled, and the interval must restart anyway.
func (s *Service) markRun(ctx context.Context, sourceID int64) {
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markRunTimeout)
	defer cancel()
	if err := s.cat.MarkRun(markCtx, sourceID); err != nil {
		s.log.Warn("record the end of a backup run", "source", sourceID, "error", err)
	}
}
