package backup

import (
	"context"
	"sync"
)

// gate keeps the deletion of repository objects apart from backups.
//
// A backup that meets a file whose content is already in the repository does not write it again, and the
// content only becomes referenced when the run writes its versions into the catalog. Deleting object files
// while a run is in progress could therefore take away content the run relies on. Runs share the gate; a
// sweep has it alone.
//
// A sweep never waits: it gets the gate or is refused, so a backup is only ever held up by a sweep that is
// running, never by one that is waiting for its turn. The zero value is ready to use.
type gate struct {
	mu   sync.Mutex
	runs int
	// sweeping is closed when the sweep in progress ends; it is nil while there is none.
	sweeping chan struct{}
}

// enter takes the shared side. It waits while a sweep is in progress, calling waiting (when it is not nil)
// once before it does, and gives up when ctx ends.
func (g *gate) enter(ctx context.Context, waiting func()) error {
	told := false
	for {
		g.mu.Lock()
		sweep := g.sweeping
		if sweep == nil {
			g.runs++
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		if !told && waiting != nil {
			told = true
			waiting()
		}
		select {
		case <-sweep:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// leave gives back the shared side.
func (g *gate) leave() {
	g.mu.Lock()
	g.runs--
	g.mu.Unlock()
}

// tryExclusive takes the exclusive side, unless a run or another sweep has the gate.
func (g *gate) tryExclusive() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.runs > 0 || g.sweeping != nil {
		return false
	}
	g.sweeping = make(chan struct{})
	return true
}

// release gives back the exclusive side and lets the waiting runs go on.
func (g *gate) release() {
	g.mu.Lock()
	close(g.sweeping)
	g.sweeping = nil
	g.mu.Unlock()
}
