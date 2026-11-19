package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/search"
	"timevault/internal/weblog"
)

const (
	// schedulerActor is recorded as the requester of backups the scheduler starts.
	schedulerActor       = "scheduler"
	defaultSchedulerTick = 15 * time.Second
	schedulerWarnEvery   = time.Minute
	markRunTimeout       = 10 * time.Second

	// Automatic backups are spread out: after one starts the next waits at least the gap, and after one ends
	// the next waits for a rest as long as the run took (within these bounds), so a big folder that was scanned
	// for minutes is followed by a longer pause and the server is not busy all the time.
	minGap  = 30 * time.Second
	maxGap  = 10 * time.Minute
	minRest = 30 * time.Second
	maxRest = 10 * time.Minute

	// probeMargin is how far before the last scan the probe looks for changes, so a clock that is a little off or an
	// index that is a little late does not hide one.
	probeMargin = 5 * time.Minute
	// probeTimeout bounds one question to the search engine. A probe never delays a backup for longer.
	probeTimeout = 3 * time.Second
	// probeBackoff is how long the search engine is left alone after it failed to answer, so a stopped engine costs
	// one timeout and not one for every due folder in every tick.
	probeBackoff = time.Minute
	// maxProbesPerTick bounds how many questions one tick asks the search engine.
	maxProbesPerTick = 25
)

// SchedulerOptions tunes how automatic backups are spread out.
type SchedulerOptions struct {
	// Parallel is how many automatic backups may be queued or running at the same time; below 1 means 1.
	Parallel int
	// GapAuto derives the time between two starts from the schedules, see autoGap. Otherwise Gap is that time.
	GapAuto bool
	Gap     time.Duration
	// RestAuto makes the pause after a run as long as the run took, see restAfter. Otherwise Rest is that pause.
	// A rest only applies while Parallel is 1.
	RestAuto bool
	Rest     time.Duration
	// Probe asks the search engine whether a folder changed before it is scanned, and skips the scan when it did
	// not.
	Probe bool
	// FullScanEvery is the longest a folder goes without a complete scan while Probe is on.
	FullScanEvery time.Duration
}

func (o SchedulerOptions) parallel() int { return max(o.Parallel, 1) }

// restFor is the pause after a run that took d, or 0 when no rest applies.
func (o SchedulerOptions) restFor(d time.Duration) time.Duration {
	switch {
	case o.parallel() != 1:
		return 0
	case o.RestAuto:
		return restAfter(d)
	}
	return o.Rest
}

// pace remembers when automatic backups started and ended. It is in memory: after a restart the first one may
// start at once.
type pace struct {
	mu           sync.Mutex
	lastStart    time.Time
	lastEnd      time.Time
	lastDuration time.Duration
}

func (p *pace) started(t time.Time) {
	p.mu.Lock()
	p.lastStart = t
	p.mu.Unlock()
}

func (p *pace) ended(t time.Time, took time.Duration) {
	p.mu.Lock()
	p.lastEnd, p.lastDuration = t, took
	p.mu.Unlock()
}

// earliest is the first moment another automatic backup may start: the gap after the previous start and the
// rest after the previous end, whichever is later.
func (p *pace) earliest(gap time.Duration, rest func(time.Duration) time.Duration) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.lastStart.Add(gap)
	if !p.lastEnd.IsZero() {
		if r := p.lastEnd.Add(rest(p.lastDuration)); r.After(t) {
			t = r
		}
	}
	return t
}

// restAfter is the pause that follows a run that took d.
func restAfter(d time.Duration) time.Duration { return min(max(d, minRest), maxRest) }

// autoGap is the time between two starts that keeps up with every schedule: a source with an interval of T minutes
// needs 1/T runs a minute, so 10 folders of 60 minutes each want one run every 6 minutes. It stays within
// minGap and maxGap.
func autoGap(intervals []int) time.Duration {
	rate := 0.0 // runs per minute
	for _, m := range intervals {
		if m > 0 {
			rate += 1 / float64(m)
		}
	}
	if rate == 0 {
		return maxGap
	}
	return min(max(time.Duration(float64(time.Minute)/rate), minGap), maxGap)
}

// rankDue orders the due sources by who should go first: those without any recovery point yet, then the most
// overdue in proportion to their own interval (so a short interval is not starved by a long one), then the one
// whose last run was the quickest, so small folders do not wait behind a big one.
func rankDue(due []catalog.Due, now time.Time) []catalog.Due {
	out := append([]catalog.Due(nil), due...)
	overdue := func(d catalog.Due) float64 {
		if d.LastRunAt == nil || d.ScheduleMinutes <= 0 {
			return 0
		}
		return float64(now.Sub(*d.LastRunAt)) / float64(time.Duration(d.ScheduleMinutes)*time.Minute)
	}
	cost := func(d catalog.Due) int64 {
		if d.LastRunMS == nil {
			return 0
		}
		return *d.LastRunMS
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.HasPoint != b.HasPoint {
			return !a.HasPoint
		}
		if oa, ob := overdue(a), overdue(b); oa != ob {
			return oa > ob
		}
		if ca, cb := cost(a), cost(b); ca != cb {
			return ca < cb
		}
		return a.ID < b.ID
	})
	return out
}

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

// RunScheduler starts the automatic backups that are due, one at a time and spread out (see SchedulerOptions). It
// looks at once and then every tick, and returns when ctx ends. Every backup run, whatever its outcome, restarts the
// interval of its source, so a source that keeps failing is retried once per interval. Start it once, after the job
// runner.
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
		s.sweepIfPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// queueDue goes through the due sources in order of priority. A source the search engine says has not changed is
// done at once, which costs next to nothing. The first one that has to be scanned is started if the pace allows,
// and then the tick ends. Problems are logged, at most once per source and minute, and never stop the scheduler.
func (s *Service) queueDue(ctx context.Context, lastWarn map[int64]time.Time) {
	warn := func(sourceID int64, msg string, err error) {
		if ctx.Err() != nil || time.Since(lastWarn[sourceID]) < schedulerWarnEvery {
			return
		}
		lastWarn[sourceID] = time.Now()
		s.log.Warn(msg, "source", sourceID, "error", err)
		s.webLog(weblog.Scheduler("scheduler.error", sourceID, slog.LevelWarn))
	}
	due, err := s.cat.DueNow(ctx)
	if err != nil {
		warn(0, "look for scheduled backups", err)
		return
	}
	if len(due) == 0 {
		return
	}
	now := time.Now()
	open, err := s.mayStartScan(ctx, now)
	if err != nil {
		warn(0, "look for running scheduled backups", err)
		return
	}
	asked := 0
	for _, d := range rankDue(due, now) {
		// A source stays due until its run ends, so a long backup would otherwise make
		// every tick try a job that the database refuses.
		if s.backupActive(ctx, d.ID) {
			continue
		}
		if asked < maxProbesPerTick {
			skip, probed := s.probeUnchanged(ctx, d, now)
			if probed {
				asked++
			}
			if skip {
				continue
			}
		}
		if !open {
			continue
		}
		_, created, err := s.StartScheduledBackup(ctx, d.ID)
		if err != nil {
			warn(d.ID, "start a scheduled backup", err)
			continue
		}
		if created {
			s.webLog(weblog.Scheduler("scheduler.queued", d.ID, slog.LevelInfo))
		}
		s.pace.started(time.Now())
		return
	}
}

// mayStartScan reports whether the pace lets an automatic backup start now: fewer than Parallel of them are queued
// or running, and the gap and the rest since the last one have passed.
func (s *Service) mayStartScan(ctx context.Context, now time.Time) (bool, error) {
	var inFlight int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE kind = 'backup' AND state IN ('queued', 'running') AND params->>'scheduled' = 'true'`).Scan(&inFlight); err != nil {
		return false, err
	}
	if inFlight >= s.sched.parallel() {
		return false, nil
	}
	gap := s.sched.Gap
	if s.sched.GapAuto {
		intervals, err := s.cat.ScheduleIntervals(ctx)
		if err != nil {
			return false, err
		}
		gap = autoGap(intervals)
	}
	return !now.Before(s.pace.earliest(gap, s.sched.restFor)), nil
}

// probeUnchanged asks the search engine whether a due source can be skipped, and when it can, restarts its interval
// without a scan. It is conservative: it only says yes when the engine knows the folder, answers in time, sees
// nothing created, modified, renamed, moved or deleted since the last complete scan, and that scan is recent and
// left nothing uncaptured. Anything else means the folder is scanned as usual. asked tells whether the engine was
// asked at all, for the bound on the questions of a tick.
func (s *Service) probeUnchanged(ctx context.Context, d catalog.Due, now time.Time) (skip, asked bool) {
	if !s.sched.Probe || !d.HasPoint || d.LatestSkipped > 0 || d.LastWalkAt == nil || d.LastWalkItems == nil {
		return false, false
	}
	if now.Sub(*d.LastWalkAt) >= s.sched.FullScanEvery {
		return false, false
	}
	prober, ok := s.search.(search.ChangeProber)
	if !ok || s.probeBackedOff(now) {
		return false, false
	}
	askCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	started := time.Now()
	res, err := prober.Probe(askCtx, d.RootPath, d.LastWalkAt.Add(-probeMargin))
	if err != nil {
		s.probeFailed(ctx, err, now)
		return false, true
	}
	if res.Changed || res.Items != *d.LastWalkItems {
		return false, true
	}
	s.recordRun(ctx, d.ID, catalog.RunEnd{Duration: time.Since(started)})
	s.log.Debug("scheduled scan skipped: the search engine sees no change", "source", d.ID, "items", res.Items)
	s.webLog(weblog.Scheduler("scheduler.skipped", d.ID, slog.LevelDebug))
	return true, true
}

// webLog sends a line to the web log, when there is one.
func (s *Service) webLog(e weblog.Entry) {
	if s.web != nil {
		s.web.Write(e)
	}
}

// probeBackedOff reports whether the search engine failed to answer lately and is left alone for now.
func (s *Service) probeBackedOff(now time.Time) bool { return now.UnixNano() < s.probeDownUntil.Load() }

// probeFailed leaves the search engine alone for a while after it failed to answer. A folder it does not know, a
// folder it refuses and a caller that gave up say nothing about an outage.
func (s *Service) probeFailed(ctx context.Context, err error, now time.Time) {
	if ctx.Err() == nil && !errors.Is(err, search.ErrNotIndexed) && !errors.Is(err, search.ErrBadQuery) && !errors.Is(err, search.ErrDisabled) {
		s.probeDownUntil.Store(now.Add(probeBackoff).UnixNano())
	}
}

// probeItems is how many entries the search engine holds below the folder of a source, taken right before a scan
// so that a later probe can tell a deletion. It is nil when probing is off or the engine cannot say.
func (s *Service) probeItems(ctx context.Context, rootPath string) *int64 {
	if !s.sched.Probe {
		return nil
	}
	prober, ok := s.search.(search.ChangeProber)
	now := time.Now()
	if !ok || s.probeBackedOff(now) {
		return nil
	}
	askCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	res, err := prober.Probe(askCtx, rootPath, time.Time{})
	if err != nil {
		s.probeFailed(ctx, err, now)
		if !errors.Is(err, search.ErrNotIndexed) && ctx.Err() == nil {
			s.log.Debug("count the entries of a folder in the search engine", "error", err)
		}
		return nil
	}
	return &res.Items
}

// backupActive reports whether a backup of the source is queued or running.
func (s *Service) backupActive(ctx context.Context, sourceID int64) bool {
	newest, err := jobs.List(ctx, s.pool, jobs.Filter{SourceID: &sourceID, Kind: jobs.KindBackup, Limit: 1})
	return err == nil && len(newest) == 1 && newest[0].Active()
}

// recordRun restarts the schedule interval of a source whose backup run just ended and keeps how it went. The job's
// context may already be canceled, and the interval must restart anyway.
func (s *Service) recordRun(ctx context.Context, sourceID int64, end catalog.RunEnd) {
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markRunTimeout)
	defer cancel()
	if err := s.cat.RecordRun(markCtx, sourceID, end); err != nil {
		s.log.Warn("record the end of a backup run", "source", sourceID, "error", err)
	}
}
