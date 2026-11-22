package ntfsindex

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// rootsList is the folders the manager under test is asked to hold; a test changes it while the manager runs.
type rootsList struct {
	mu sync.Mutex
	v  []string
}

func (r *rootsList) set(paths ...string) {
	r.mu.Lock()
	r.v = paths
	r.mu.Unlock()
}

func (r *rootsList) get(context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.v...), nil
}

// start runs a manager on the model until the test ends. Its times are short, and it uses the clock of the model, so that
// what the journal says and what the manager asks about are on the same time line.
func start(t *testing.T, m *model, roots *rootsList, open func(drive string) (volume, error)) *Manager {
	t.Helper()
	if open == nil {
		open = func(drive string) (volume, error) {
			if drive != "D:" {
				return nil, ErrUnsupported
			}
			return m, nil
		}
	}
	mgr := New(Config{Roots: roots.get, Open: open, Interval: 10 * time.Millisecond, Now: m.now})
	mgr.retryAfter, mgr.rebuildEvery = 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { mgr.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the manager did not stop")
		}
	})
	return mgr
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func ready(mgr *Manager) func() bool { return func() bool { return mgr.Health().State == StateOK } }

func TestManagerBuildsInTheBackgroundAndAnswers(t *testing.T) {
	m := newModel()
	m.tree()
	m.gate = make(chan struct{})
	roots := &rootsList{}
	roots.set(`D:\data\team`)
	mgr := start(t, m, roots, nil)

	// Until the first build is done nothing can be answered, and the status says so.
	eventually(t, "the build to start", func() bool { return mgr.Health().Key == "index_building" })
	if _, _, err := mgr.Subfolders(`D:\data\team`, 0); !errors.Is(err, ErrNotReady) {
		t.Errorf("a question while the index is built = %v, want ErrNotReady", err)
	}
	if h := mgr.Health(); h.State != StateDegraded || len(h.Args) != 1 || h.Args[0] != "D:" {
		t.Errorf("health while building = %+v", h)
	}
	close(m.gate)
	eventually(t, "the index", ready(mgr))
	if h := mgr.Health(); h.Key != "index_ok" || h.Args[0] != 10 || h.Args[1] != "D:" {
		t.Errorf("health = %+v", h)
	}

	page, err := mgr.Search(`D:\data\team`, []string{"NEEDLE"}, 0, 10)
	if err != nil || page.Total != 1 || page.Items[0].Path != `D:\data\team\Needle Folder` {
		t.Errorf("Search = %+v, %v", page, err)
	}
	folders, more, err := mgr.Subfolders(`D:\data\team`, 0)
	if err != nil || more || len(folders) != 2 || folders[0].Name != "docs" || folders[0].Size != 90 {
		t.Errorf("Subfolders = %+v %v %v", folders, more, err)
	}
	res, err := mgr.Probe(`D:\data\team`, time.Time{})
	if err != nil || res.Items != 10 {
		t.Errorf("Probe = %+v, %v", res, err)
	}

	// What is not held, and what cannot be held, is not indexed; that is not a failure of the index.
	for _, path := range []string{`D:\data\other`, `D:\elsewhere`, `D:\`, `D:\data\team\nope`, `\\server\share\x`, `relative\path`, ``} {
		if _, err := mgr.Probe(path, time.Time{}); !errors.Is(err, ErrNotIndexed) {
			t.Errorf("Probe(%q) = %v, want ErrNotIndexed", path, err)
		}
	}
}

func TestManagerFollowsTheFilesAndTheFoldersToHold(t *testing.T) {
	m := newModel()
	m.tree()
	roots := &rootsList{}
	roots.set(`D:\data\team`)
	mgr := start(t, m, roots, nil)
	eventually(t, "the index", ready(mgr))

	// A change to a file shows in a probe at once.
	started := m.now()
	m.mu.Lock()
	m.tick()
	m.mu.Unlock()
	m.write(`D:\data\team\a.txt`, 77)
	res, err := mgr.Probe(`D:\data\team`, started)
	if err != nil || !res.Changed {
		t.Errorf("Probe after a write = %+v, %v", res, err)
	}
	// And in a search once the journal was read again.
	m.create(`D:\data\team\fresh\n1.txt`, 3)
	eventually(t, "the new file in a search", func() bool {
		page, err := mgr.Search(`D:\data\team`, []string{"n1"}, 0, 5)
		return err == nil && page.Total == 1
	})

	// A folder that is added to the folders to hold is built in.
	if _, err := mgr.Probe(`D:\data\other`, time.Time{}); !errors.Is(err, ErrNotIndexed) {
		t.Errorf("before it is added: %v", err)
	}
	roots.set(`D:\data\team`, `D:\data\other`)
	eventually(t, "the folder that was added", func() bool {
		res, err := mgr.Probe(`D:\data\other`, time.Time{})
		return err == nil && res.Items == 2
	})
	if page, err := mgr.Search(`D:\data\team`, []string{"n1"}, 0, 5); err != nil || page.Total != 1 {
		t.Errorf("what was there before is still there: %+v, %v", page, err)
	}

	// A folder that is taken away is not indexed any more.
	roots.set(`D:\data\other`)
	eventually(t, "the folder that was taken away", func() bool {
		_, err := mgr.Probe(`D:\data\team`, time.Time{})
		return errors.Is(err, ErrNotIndexed)
	})
	if res, err := mgr.Probe(`D:\data\other`, time.Time{}); err != nil || res.Items != 2 {
		t.Errorf("the folder that stays: %+v, %v", res, err)
	}

	// No folders at all: the volume is let go.
	roots.set()
	eventually(t, "the volume to be let go", func() bool { return mgr.Health().State == StateIdle })
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed == 0 {
		t.Error("the volume was not closed")
	}
}

func TestManagerBuildsAgainWhenTheJournalIsLost(t *testing.T) {
	m := newModel()
	m.tree()
	roots := &rootsList{}
	roots.set(`D:\data\team`)
	mgr := start(t, m, roots, nil)
	eventually(t, "the index", ready(mgr))
	lists := func() int {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.lists
	}
	before := lists()

	m.write(`D:\data\team\a.txt`, 1)
	m.wrapJournal()
	m.create(`D:\data\team\after.txt`, 9)
	// The next question finds that the journal is gone, and the index is built again from the volume.
	if _, err := mgr.Probe(`D:\data\team`, time.Time{}); !errors.Is(err, ErrNotReady) {
		t.Errorf("Probe on a lost journal = %v, want ErrNotReady", err)
	}
	eventually(t, "the index to be built again", func() bool {
		page, err := mgr.Search(`D:\data\team`, []string{"after"}, 0, 5)
		return err == nil && page.Total == 1
	})
	if got := lists() - before; got != 2 {
		t.Errorf("the volume was listed %d times for the rebuild, want 2", got)
	}
	// Before the rebuild nobody watched; after it the probe starts again from the new index.
	m.mu.Lock()
	m.tick()
	m.mu.Unlock()
	if res, err := mgr.Probe(`D:\data\team`, m.now()); err != nil || res.Changed {
		t.Errorf("Probe after the rebuild = %+v, %v", res, err)
	}
}

func TestManagerBuildsAgainWhenAFolderIsMovedIn(t *testing.T) {
	m := newModel()
	m.tree()
	roots := &rootsList{}
	roots.set(`D:\data\team`)
	mgr := start(t, m, roots, nil)
	eventually(t, "the index", ready(mgr))

	m.rename(`D:\data\other`, `D:\data\team\other`)
	eventually(t, "the folder that was moved in to be indexed with its files", func() bool {
		page, err := mgr.Search(`D:\data\team\other`, []string{"a.txt"}, 0, 5)
		return err == nil && page.Total == 1 && page.Items[0].Path == `D:\data\team\other\a.txt`
	})
}

func TestManagerSaysWhyAVolumeIsNotIndexed(t *testing.T) {
	m := newModel()
	m.tree()
	roots := &rootsList{}
	roots.set(`D:\data\team`)
	var mu sync.Mutex
	openErr := ErrAccessDenied
	mgr := start(t, m, roots, func(string) (volume, error) {
		mu.Lock()
		defer mu.Unlock()
		if openErr != nil {
			return nil, openErr
		}
		return m, nil
	})

	setErr := func(err error) {
		mu.Lock()
		openErr = err
		mu.Unlock()
	}
	eventually(t, "the status to say why", func() bool { return mgr.Health().Key == "index_no_admin" })
	if h := mgr.Health(); h.State != StateDegraded || h.Detail == "" {
		t.Errorf("health = %+v", h)
	}
	if _, err := mgr.Probe(`D:\data\team`, time.Time{}); !errors.Is(err, ErrNotReady) {
		t.Errorf("a question without rights = %v, want ErrNotReady", err)
	}

	setErr(ErrUnsupported)
	eventually(t, "the status for a volume that is not NTFS", func() bool { return mgr.Health().Key == "index_unsupported" })
	if h := mgr.Health(); len(h.Args) != 1 || h.Args[0] != "D:" {
		t.Errorf("health = %+v", h)
	}

	// A volume without a journal.
	setErr(nil)
	m.mu.Lock()
	m.journalErr = ErrJournalOff
	m.mu.Unlock()
	eventually(t, "the status for a volume without a journal", func() bool { return mgr.Health().Key == "index_journal_off" })

	// Once the cause is gone the volume is indexed.
	m.mu.Lock()
	m.journalErr = nil
	m.mu.Unlock()
	eventually(t, "the index", ready(mgr))
	if res, err := mgr.Probe(`D:\data\team`, time.Time{}); err != nil || res.Items != 10 {
		t.Errorf("Probe = %+v, %v", res, err)
	}
}

func TestManagerLeavesOutWhatIsNotOnADriveLetter(t *testing.T) {
	m := newModel()
	m.tree()
	roots := &rootsList{}
	roots.set(`\\server\share\folder`, `relative`, `D:\data\team`, `D:\DATA\TEAM\`)
	mgr := start(t, m, roots, nil)
	eventually(t, "the index", ready(mgr))
	if h := mgr.Health(); h.Args[1] != "D:" {
		t.Errorf("health = %+v: only D: has folders to hold", h)
	}
	if res, err := mgr.Probe(`D:\data\team`, time.Time{}); err != nil || res.Items != 10 {
		t.Errorf("a folder written twice is held once: %+v, %v", res, err)
	}
}

func TestManagerClosesTheVolumesWhenItStops(t *testing.T) {
	m := newModel()
	m.tree()
	roots := &rootsList{}
	roots.set(`D:\data\team`)
	mgr := New(Config{Roots: roots.get, Open: func(string) (volume, error) { return m, nil }, Interval: 10 * time.Millisecond, Now: m.now})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { mgr.Run(ctx); close(done) }()
	eventually(t, "the index", ready(mgr))
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed != 1 {
		t.Errorf("the volume was closed %d times, want once", m.closed)
	}
}
