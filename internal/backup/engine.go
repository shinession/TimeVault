// Package backup builds recovery points: it scans a source, copies changed
// content into the repository and publishes the result as one committed point.
//
// The MVP reads the live file system ("live_scan"): files are not read from a
// VSS snapshot, so every recovery point is labelled accordingly and a file that
// changes while it is being read is detected and retried instead of stored torn.
package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"timevault/internal/catalog"
	"timevault/internal/pathutil"
	"timevault/internal/repository"
)

const (
	tmpMaxAge       = 24 * time.Hour
	failureTimeout  = 2 * time.Minute
	commitTimeout   = time.Minute
	maxFailureBytes = 500
)

// Config wires an Engine.
type Config struct {
	Catalog     *catalog.Store
	Repository  *repository.Store
	Logger      *slog.Logger
	HashWorkers int
	// Protected are folders that must never be backed up, normally the
	// repository and the restore root.
	Protected []string
}

// Engine runs backups and verifications.
type Engine struct {
	cat         *catalog.Store
	repo        *repository.Store
	log         *slog.Logger
	hashWorkers int
	protected   []string
	// gate keeps backups and the deletion of repository objects apart.
	gate gate
	// sweepBudget is how long one sweep may run, and sweepBatch how many objects it asks the catalog for at a
	// time. Tests make them small, and replace now, the clock the time limit is measured by.
	sweepBudget time.Duration
	sweepBatch  int
	now         func() time.Time
	// hook lets tests act at well-defined moments of a run.
	hook func(event, rel string)
	// failReadDir lets tests make listing a folder fail.
	failReadDir func(rel string) error
}

// New builds an Engine.
func New(cfg Config) *Engine {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	workers := cfg.HashWorkers
	if workers < 1 {
		workers = 4
	}
	return &Engine{cat: cfg.Catalog, repo: cfg.Repository, log: log, hashWorkers: workers, protected: cfg.Protected,
		sweepBudget: defaultSweepBudget, sweepBatch: defaultSweepBatch, now: time.Now}
}

// resolveProtected lists the protected folders as configured and as they
// really are on disk, so a junction cannot hide one from the scan.
func (e *Engine) resolveProtected() []string {
	out := make([]string, 0, 2*len(e.protected))
	for _, p := range e.protected {
		if p == "" {
			continue
		}
		out = append(out, p)
		if real := pathutil.RealOrClean(p); real != p {
			out = append(out, real)
		}
	}
	return out
}

// ErrUnchanged is returned by Run when RunOptions.SkipUnchanged is set and the
// source is identical to its newest recovery point. Nothing was recorded.
var ErrUnchanged = errors.New("backup: nothing changed since the last recovery point")

// RunOptions tunes one backup.
type RunOptions struct {
	// Rehash reads every file even when its size and modification time are unchanged.
	Rehash bool
	// SkipUnchanged records no recovery point when nothing changed since the
	// previous one, so automatic runs do not pile up identical points. Run then
	// returns ErrUnchanged. The first backup of a source is always recorded.
	SkipUnchanged bool
}

// Run takes one recovery point of a source and returns it once committed.
// On any failure the partial work is removed and the point is recorded as
// failed (or aborted when ctx was cancelled); it never becomes visible.
// A run that ends with ErrUnchanged leaves no trace at all, not even a failed point.
// While a sweep of the repository is in progress (see SweepObjects) the run waits for it to end.
func (e *Engine) Run(ctx context.Context, sourceID int64, opts RunOptions, rep Reporter) (catalog.Point, error) {
	if rep == nil {
		rep = NopReporter{}
	}
	// A sweep deletes object files, so no run may store content, or rely on content that is already stored,
	// while one is in progress. The run waits before it locks the source, so that nothing is held meanwhile.
	waiting := func() { rep.Event("info", "waiting for a sweep of the repository to finish") }
	if err := e.gate.enter(ctx, waiting); err != nil {
		return catalog.Point{}, fmt.Errorf("backup: waiting for the repository sweep to end: %w", err)
	}
	defer e.gate.leave()
	lock, err := e.cat.LockSource(ctx, sourceID)
	if err != nil {
		return catalog.Point{}, err
	}
	defer lock.Unlock()
	// The source is read only once its writer lock is held, so an edit made while this run waited for it or
	// just before is seen, and none can be stored while the run goes on (a change of folder needs the lock).
	src, err := e.cat.GetSource(ctx, sourceID)
	if err != nil {
		return catalog.Point{}, err
	}
	if !src.Enabled {
		return catalog.Point{}, errors.New("backup: the source is disabled")
	}

	rep.Phase(PhasePreparing)
	if err := e.checkSource(src); err != nil {
		return catalog.Point{}, err
	}
	if removed, err := e.repo.CleanTmp(tmpMaxAge); err != nil {
		e.log.Warn("clean repository temp files", "error", err)
	} else if removed > 0 {
		rep.Event("info", fmt.Sprintf("removed %d stale temporary object(s) from the repository", removed))
	}
	if h := e.repo.Health(); !h.OK {
		return catalog.Point{}, fmt.Errorf("backup: repository is not usable: %s", h.Detail)
	}

	point, prevSeq, err := e.cat.BeginPoint(ctx, lock)
	if err != nil {
		return catalog.Point{}, err
	}
	rep.Event("info", fmt.Sprintf("started %s recovery point #%d (live scan, no VSS snapshot)", point.Kind, point.Seq))

	if err := e.build(ctx, lock, src, point, prevSeq, opts, rep); err != nil {
		if errors.Is(err, ErrUnchanged) {
			return catalog.Point{}, ErrUnchanged
		}
		state, reason := catalog.StateFailed, err.Error()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			state, reason = catalog.StateAborted, "canceled"
		}
		if len(reason) > maxFailureBytes {
			reason = reason[:maxFailureBytes]
		}
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failureTimeout)
		defer cancel()
		if ferr := e.cat.FailPoint(failCtx, lock, point, state, reason); ferr != nil {
			e.log.Error("record failed recovery point", "point", point.ID, "error", ferr)
		}
		return catalog.Point{}, err
	}
	rep.Phase(PhaseCommitted)
	// The point is committed and visible. A cancellation that arrives now must not
	// turn that success into an error, so reading it back ignores the job's context.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	return e.cat.GetPoint(readCtx, point.ID)
}

func (e *Engine) build(ctx context.Context, lock *catalog.SourceLock, src catalog.Source, point catalog.Point, prevSeq int, opts RunOptions, rep Reporter) error {
	rootNode, err := e.cat.RootNode(ctx, src.ID)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(src.RootPath)
	if err != nil {
		return fmt.Errorf("backup: open source folder: %w", err)
	}
	defer root.Close()

	sc := &scan{
		e: e, src: src, point: point, prevSeq: prevSeq, rehash: opts.Rehash,
		root: root, excl: newExcluder(src.Excludes), rep: rep,
		manifest: catalog.NewManifest(), refs: map[repository.Hash]int64{},
		rootReal: pathutil.RealOrClean(src.RootPath), protected: e.resolveProtected(),
	}
	rep.Phase(PhaseScanning)
	if err := sc.dir(ctx, rootNode, ""); err != nil {
		return err
	}

	rep.Phase(PhaseValidating)
	for h, size := range sc.refs {
		got, err := e.repo.Stat(h)
		if err != nil {
			return fmt.Errorf("backup: object %s vanished before the point was committed: %w", h, err)
		}
		if got != size {
			return fmt.Errorf("backup: object %s has %d bytes, expected %d", h, got, size)
		}
	}
	// Make the new objects durable before the catalog starts to refer to them.
	if err := e.repo.Barrier(); err != nil {
		rep.Event("warn", fmt.Sprintf("the repository folders could not be flushed to disk (%v); if the computer loses power now the newest objects may be missing, so run an integrity check", err))
	}

	rep.Phase(PhaseCommitting)
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.SkipUnchanged && prevSeq > 0 {
		if err := e.discardIfUnchanged(ctx, lock, point, sc.stats, rep); err != nil {
			return err
		}
	}
	// The commit is one statement; once it is issued it is not abandoned, because a
	// cancellation in the middle would leave the outcome in doubt.
	commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancelCommit()
	if err := e.cat.CommitPoint(commitCtx, lock, point, sc.stats, sc.manifest.Sum()); err != nil {
		return err
	}
	final := sc.prog
	final.Phase = PhaseCommitted
	rep.Progress(final)
	rep.Event("info", fmt.Sprintf("committed recovery point #%d: %d files, %d folders, %d added, %d modified, %d deleted, %d not captured",
		point.Seq, sc.stats.FileCount, sc.stats.DirCount, sc.stats.AddedCount, sc.stats.ModifiedCount, sc.stats.DeletedCount, sc.stats.SkippedCount))
	return nil
}

// discardIfUnchanged drops the point under construction when the scan found
// nothing to record. It returns ErrUnchanged once the point is gone and nil when
// the point has to be committed.
//
// "Nothing" means no path was added, modified or deleted and the number of items
// that could not be captured is the same as in the previous point. A different
// number is worth a point of its own: items were newly skipped, or an earlier
// problem went away, and the history should say so.
func (e *Engine) discardIfUnchanged(ctx context.Context, lock *catalog.SourceLock, point catalog.Point, st catalog.Stats, rep Reporter) error {
	if st.AddedCount+st.ModifiedCount+st.DeletedCount != 0 {
		return nil
	}
	prev, err := e.cat.LatestCommitted(ctx, point.SourceID)
	if err != nil {
		return err
	}
	if st.SkippedCount != prev.SkippedCount {
		return nil
	}
	// Like the commit, the cleanup is not abandoned halfway by a cancellation.
	discardCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	if err := e.cat.DiscardPoint(discardCtx, lock, point); err != nil {
		return err
	}
	rep.Event("info", fmt.Sprintf("nothing changed since recovery point #%d; no new recovery point was recorded", prev.Seq))
	return ErrUnchanged
}

// checkSource makes sure the registered folder still exists and does not
// overlap the repository or the restore root.
func (e *Engine) checkSource(src catalog.Source) error {
	st, err := os.Stat(src.RootPath)
	if err != nil {
		return fmt.Errorf("backup: source folder is not accessible: %w", err)
	}
	if !st.IsDir() {
		return errors.New("backup: the source is not a folder")
	}
	real, err := pathutil.Real(src.RootPath)
	if err != nil {
		return fmt.Errorf("backup: resolve source folder: %w", err)
	}
	for _, p := range e.protected {
		if p == "" {
			continue
		}
		if pathutil.Overlaps(pathutil.RealOrClean(p), real) {
			return fmt.Errorf("backup: the source overlaps the protected folder %s", p)
		}
	}
	return nil
}
