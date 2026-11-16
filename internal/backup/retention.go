package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"timevault/internal/catalog"
	"timevault/internal/repository"
)

const (
	defaultSweepBudget = 2 * time.Minute
	defaultSweepBatch  = 1000
	maxSweepEvents     = 20
)

// ErrSweepBusy is returned by SweepObjects when a backup run or another sweep is in progress.
var ErrSweepBusy = errors.New("backup: the repository cannot be swept while a backup or another sweep is running")

// SweepStats tells what a sweep did.
type SweepStats struct {
	// Objects and Bytes are what was removed: the number of objects, and their size as the catalog recorded it.
	Objects, Bytes int64
	// Failed is the number of objects whose file could not be deleted, because another process has it open,
	// for example. They stay in the catalog, and the next sweep tries them again.
	Failed int64
	// More is true when the time limit ended the sweep while unreferenced objects were still to be looked at.
	More bool
}

// Prune deletes the older versions of the files of a source, so that no file keeps more than opts.Max (see
// catalog.PruneVersions). It takes the writer lock of the source and returns catalog.ErrBusy while a backup
// of the source is running. Content that no version needs any more stays in the repository until
// SweepObjects removes it.
func (e *Engine) Prune(ctx context.Context, sourceID int64, opts catalog.PruneOptions, rep Reporter) (catalog.PruneResult, error) {
	if rep == nil {
		rep = NopReporter{}
	}
	lock, err := e.cat.LockSource(ctx, sourceID)
	if err != nil {
		return catalog.PruneResult{}, err
	}
	defer lock.Unlock()

	rep.Phase(PhasePruning)
	res, err := e.cat.PruneVersions(ctx, lock, opts)
	switch {
	case err != nil:
		if res.Versions > 0 {
			rep.Event("warn", fmt.Sprintf("pruning stopped early: %d old version(s) of %d file(s) were deleted before it did", res.Versions, res.Files))
		}
	case res.Versions == 0:
		rep.Event("info", fmt.Sprintf("no old versions had to be deleted (at most %d per file are kept)", opts.Max))
	default:
		rep.Event("info", fmt.Sprintf("deleted %d old version(s) of %d file(s); %d recovery point(s) were marked as partial",
			res.Versions, res.Files, res.PointsMarked))
	}
	return res, err
}

// SweepObjects deletes the content that no version of any source refers to any more from the repository: what
// pruning left behind, and what failed or canceled backups registered but never used.
//
// While a sweep runs, backups wait for it, so it must not run long, and it never waits itself: when a
// backup is running, or another sweep, it returns ErrSweepBusy. One call stops after about two minutes and
// says so in stats.More, and the next call carries on.
//
// Each object's file is deleted before its record, so an interrupted sweep leaves a record without a file,
// which the next sweep removes, and never a file the catalog does not know about.
func (e *Engine) SweepObjects(ctx context.Context, rep Reporter) (SweepStats, error) {
	var st SweepStats
	if rep == nil {
		rep = NopReporter{}
	}
	if !e.gate.tryExclusive() {
		return st, ErrSweepBusy
	}
	defer e.gate.release()

	rep.Phase(PhaseSweeping)
	err := e.sweep(ctx, rep, e.now().Add(e.sweepBudget), &st)
	switch {
	case err != nil:
		rep.Event("warn", fmt.Sprintf("sweeping stopped early after removing %d object(s) (%d bytes): %v", st.Objects, st.Bytes, err))
	case st.More:
		rep.Event("info", fmt.Sprintf("removed %d unreferenced object(s) (%d bytes); the time limit was reached and more are left for the next sweep", st.Objects, st.Bytes))
	default:
		rep.Event("info", fmt.Sprintf("removed %d unreferenced object(s) (%d bytes) from the repository", st.Objects, st.Bytes))
	}
	if st.Failed > 0 {
		rep.Event("warn", fmt.Sprintf("%d object(s) could not be deleted and are tried again by the next sweep", st.Failed))
	}
	e.log.Info("repository sweep", "objects", st.Objects, "bytes", st.Bytes, "failed", st.Failed, "more", st.More, "error", err)
	return st, err
}

func (e *Engine) sweep(ctx context.Context, rep Reporter, deadline time.Time, st *SweepStats) error {
	var after []byte
	problems := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		objects, err := e.cat.UnreferencedObjects(ctx, after, e.sweepBatch)
		if err != nil {
			return err
		}
		for _, o := range objects {
			if err := ctx.Err(); err != nil {
				return err
			}
			if e.now().After(deadline) {
				st.More = true
				return nil
			}
			after = o.SHA256

			h, err := repository.HashFromBytes(o.SHA256)
			if err == nil {
				if e.hook != nil {
					e.hook("sweeping", h.String())
				}
				err = e.repo.Delete(h)
			}
			if err != nil {
				st.Failed++
				e.log.Warn("delete repository object", "error", err)
				if problems++; problems <= maxSweepEvents {
					rep.Event("warn", err.Error())
				}
				continue
			}
			// The file is gone, so its record goes too, even when the sweep is being canceled.
			forgetCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
			forgotten, err := e.cat.ForgetObject(forgetCtx, o.SHA256)
			cancel()
			if err != nil {
				return err
			}
			if !forgotten {
				// Only a writer outside this process's gate can do this; the run that wrote the version has to be checked.
				e.log.Error("object was referenced again while it was being swept", "object", h.String())
				rep.Event("error", fmt.Sprintf("object %s was referenced by a new version while it was being deleted; run an integrity check", h))
				continue
			}
			st.Objects++
			st.Bytes += o.Size
		}
		if len(objects) < e.sweepBatch {
			return nil
		}
	}
}
