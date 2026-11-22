package ntfsindex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config tells the Manager what to hold and how to reach the volumes.
type Config struct {
	// Roots lists the folders to hold, such as the monitored folders. It is asked again every Interval, so a folder that is
	// added or removed is noticed. Folders that are not on a drive letter (a share) cannot be indexed and are left out.
	Roots func(ctx context.Context) ([]string, error)
	// Open opens the volume of a drive letter ("D:"); nil means the volumes of this computer.
	Open func(drive string) (volume, error)
	Log  *slog.Logger
	// Interval is how often the folders are asked for and the journals are read; 10 seconds when zero.
	Interval time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// defaultRetryAfter is how long a volume that could not be indexed is left alone before another try, and
// defaultRebuildEvery the shortest time between two builds of the index of a volume that are caused by what happened to it.
const (
	defaultRetryAfter   = 30 * time.Second
	defaultRebuildEvery = 60 * time.Second
)

// Manager holds the indexes of the volumes that have folders to hold, builds them in the background and keeps them
// up to date. It is safe for concurrent use.
type Manager struct {
	cfg  Config
	mu   sync.Mutex
	vols map[string]*volState
	kick chan struct{}
	// retryAfter and rebuildEvery are the two times above; the tests make them short.
	retryAfter, rebuildEvery time.Duration
}

type volState struct {
	drive string
	vol   volume
	idx   *index
	// built is the folders the index holds, want the folders it should hold; the same text in lower case, sorted.
	built, want []string
	// building is true while a goroutine builds the index; err says why the last build failed.
	building bool
	err      error
	// nextTry is when a failed volume is tried again, rebuildAt when the index may be built again for what happened to it.
	nextTry, rebuildAt time.Time
	loggedErr          string
	lastSync           time.Time
	// rebuildWanted says that the index has to be built again, for the reason in why.
	rebuildWanted bool
	why           string
}

// New returns a Manager. Run starts its work.
func New(cfg Config) *Manager {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Open == nil {
		cfg.Open = openVolume
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Manager{
		cfg: cfg, vols: map[string]*volState{}, kick: make(chan struct{}, 1),
		retryAfter: defaultRetryAfter, rebuildEvery: defaultRebuildEvery,
	}
}

// Run looks after the indexes until ctx ends: it asks for the folders, builds what is missing and reads the journals.
func (m *Manager) Run(ctx context.Context) {
	defer m.closeAll()
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		m.reconcile(ctx)
		m.syncAll()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.kick:
		}
	}
}

func (m *Manager) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Manager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, vs := range m.vols {
		if vs.vol != nil {
			_ = vs.vol.Close()
			vs.vol = nil
		}
	}
}

// desiredRoots groups the folders by drive: the paths in their clean form, sorted, without duplicates.
func desiredRoots(paths []string) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, p := range paths {
		r, ok := newRootInfo(p)
		if !ok || seen[strings.ToLower(r.path)] {
			continue
		}
		seen[strings.ToLower(r.path)] = true
		out[r.volume] = append(out[r.volume], r.path)
	}
	for d := range out {
		sort.Slice(out[d], func(i, j int) bool { return strings.ToLower(out[d][i]) < strings.ToLower(out[d][j]) })
	}
	return out
}

func lowerAll(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.ToLower(p)
	}
	return out
}

// reconcile makes the volumes follow the folders that are asked for: a volume with new folders is built again, a volume
// that has none is let go.
func (m *Manager) reconcile(ctx context.Context) {
	paths, err := m.cfg.Roots(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.cfg.Log.Warn("ntfs index: could not read the folders to index", "error", err)
		}
		return
	}
	desired := desiredRoots(paths)
	now := m.cfg.Now()

	m.mu.Lock()
	defer m.mu.Unlock()
	for drive, roots := range desired {
		vs := m.vols[drive]
		if vs == nil {
			vs = &volState{drive: drive}
			m.vols[drive] = vs
		}
		want := lowerAll(roots)
		changed := !slices.Equal(vs.want, want)
		vs.want = want
		switch {
		case vs.building:
		case vs.idx == nil && vs.err != nil && !changed && now.Before(vs.nextTry):
		case vs.idx == nil, changed:
			m.startBuild(vs, roots, "the folders to hold changed")
		case vs.rebuildWanted && !now.Before(vs.rebuildAt):
			m.startBuild(vs, roots, vs.why)
		}
	}
	for drive, vs := range m.vols {
		if _, wanted := desired[drive]; !wanted && !vs.building {
			if vs.vol != nil {
				_ = vs.vol.Close()
			}
			delete(m.vols, drive)
		}
	}
}

// startBuild builds the index of a volume in a goroutine. The caller holds m.mu.
func (m *Manager) startBuild(vs *volState, roots []string, why string) {
	vs.building, vs.rebuildWanted = true, false
	vs.want = lowerAll(roots)
	go m.buildVolume(vs, slices.Clone(roots), why)
}

func (m *Manager) buildVolume(vs *volState, paths []string, why string) {
	started := time.Now()
	m.mu.Lock()
	vol := vs.vol
	m.mu.Unlock()
	var err error
	if vol == nil {
		vol, err = m.cfg.Open(vs.drive)
	}
	var idx *index
	if err == nil {
		roots := make([]*rootInfo, 0, len(paths))
		for _, p := range paths {
			if r, ok := newRootInfo(p); ok {
				roots = append(roots, r)
			}
		}
		idx, err = build(vol, roots, m.cfg.Now)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	vs.building = false
	if err != nil {
		vs.err = err
		vs.nextTry = m.cfg.Now().Add(m.retryAfter)
		vs.rebuildAt = vs.nextTry
		if vol != nil && vs.vol == nil && vs.idx == nil {
			_ = vol.Close()
		} else if vol != nil {
			vs.vol = vol
		}
		if text := err.Error(); text != vs.loggedErr {
			vs.loggedErr = text
			m.cfg.Log.Warn("ntfs index: the volume could not be indexed", "volume", vs.drive, "error", err)
		}
		return
	}
	vs.vol, vs.idx, vs.err, vs.loggedErr = vol, idx, nil, ""
	vs.built = lowerAll(paths)
	vs.rebuildAt = m.cfg.Now().Add(m.rebuildEvery)
	vs.lastSync = time.Time{}
	m.cfg.Log.Info("ntfs index: the volume is indexed", "volume", vs.drive, "folders", len(paths), "entries", idx.stats(),
		"reason", why, "seconds", fmt.Sprintf("%.1f", time.Since(started).Seconds()))
}

// syncAll reads the journal of every volume that has an index, and refreshes the sizes of the files that changed.
func (m *Manager) syncAll() {
	m.mu.Lock()
	var vols []*volState
	for _, vs := range m.vols {
		if vs.idx != nil && !vs.building {
			vols = append(vols, vs)
		}
	}
	m.mu.Unlock()
	for _, vs := range vols {
		m.syncVolume(vs, 0)
		m.mu.Lock()
		idx, vol := vs.idx, vs.vol
		m.mu.Unlock()
		if idx != nil && vol != nil {
			idx.refresh(vol, 5000, time.Now().Add(2*time.Second))
		}
	}
}

// syncVolume brings the index of a volume up to the end of its journal, unless that was done less than maxAge ago. It
// returns false when the index cannot be used: it is being replaced, or the journal was lost.
func (m *Manager) syncVolume(vs *volState, maxAge time.Duration) bool {
	m.mu.Lock()
	idx, vol, last := vs.idx, vs.vol, vs.lastSync
	m.mu.Unlock()
	if idx == nil || vol == nil {
		return false
	}
	if maxAge > 0 && time.Since(last) < maxAge {
		return true
	}
	err := idx.sync(vol)
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case err == nil:
		vs.lastSync = time.Now()
		if idx.rebuild && !vs.rebuildWanted {
			vs.rebuildWanted, vs.why = true, "a folder was moved into the indexed folders"
			m.wake()
		}
		return true
	case errors.Is(err, ErrJournalLost):
		vs.rebuildWanted, vs.why = true, "the change journal was replaced or overwritten"
		vs.rebuildAt = time.Time{}
		vs.idx = nil
		m.wake()
	default:
		m.cfg.Log.Warn("ntfs index: could not read the change journal", "volume", vs.drive, "error", err)
	}
	return false
}

// find resolves a folder to its index and its node. ErrNotIndexed when the folder is not below a folder that is held,
// ErrNotReady when it is (or will be) but the index is not there.
func (m *Manager) find(path string, maxAge time.Duration) (*index, int32, int, error) {
	drive, names, ok := splitPath(path)
	if !ok {
		return nil, noNode, -1, ErrNotIndexed
	}
	folded := foldAll(names)
	m.mu.Lock()
	vs := m.vols[drive]
	m.mu.Unlock()
	if vs == nil {
		m.wake() // a folder that was added a moment ago
		return nil, noNode, -1, ErrNotReady
	}
	if !m.syncVolume(vs, maxAge) {
		return nil, noNode, -1, ErrNotReady
	}
	m.mu.Lock()
	idx, want := vs.idx, vs.want
	m.mu.Unlock()
	if idx == nil {
		return nil, noNode, -1, ErrNotReady
	}
	idx.mu.RLock()
	slot, root, found := idx.resolve(drive, folded)
	idx.mu.RUnlock()
	if found {
		return idx, slot, root, nil
	}
	// Not found: a folder that is wanted but that this index was built without is a matter of time, anything else is
	// a folder the index knows nothing about.
	full := strings.ToLower(drive + `\` + strings.Join(names, `\`))
	for _, w := range want {
		if full == w || strings.HasPrefix(full, strings.TrimRight(w, `\`)+`\`) {
			idx.mu.RLock()
			held := false
			for _, r := range idx.roots {
				held = held || strings.EqualFold(r.path, w)
			}
			idx.mu.RUnlock()
			if !held {
				return nil, noNode, -1, ErrNotReady
			}
		}
	}
	return nil, noNode, -1, ErrNotIndexed
}

// Search finds the files and folders below path whose names contain every term, folders first and then by name.
func (m *Manager) Search(path string, terms []string, offset, limit int) (Page, error) {
	idx, slot, _, err := m.find(path, 2*time.Second)
	if err != nil {
		return Page{}, err
	}
	low := make([]string, len(terms))
	for i, t := range terms {
		low[i] = strings.ToLower(t)
	}
	return idx.search(slot, low, offset, limit), nil
}

// Subfolders lists the folders directly inside path with the size of everything below each of them; more says there
// are further ones than limit (0: all).
func (m *Manager) Subfolders(path string, limit int) ([]Folder, bool, error) {
	idx, slot, _, err := m.find(path, 2*time.Second)
	if err != nil {
		return nil, false, err
	}
	folders, more := idx.subfolders(slot, limit)
	return folders, more, nil
}

// Probe says how many files and folders are below path, and whether anything changed below it since the given time. A
// zero time asks for the count only. It reads the journal first, so what happened a moment ago is counted.
func (m *Manager) Probe(path string, since time.Time) (ProbeResult, error) {
	idx, slot, root, err := m.find(path, 0)
	if err != nil {
		return ProbeResult{}, err
	}
	return idx.probe(slot, root, since), nil
}

// State is how well the index is doing.
type State string

const (
	// StateIdle means there is nothing to index.
	StateIdle State = "idle"
	// StateOK means every volume is indexed.
	StateOK State = "ok"
	// StateDegraded means some volume is being built or could not be indexed.
	StateDegraded State = "degraded"
)

// Health describes the index for the status page. Key names a message of the interface, Args go into it, and Detail is
// the same in English.
type Health struct {
	State  State
	Key    string
	Args   []any
	Detail string
}

// Health reports whether the volumes are indexed, being built, or could not be indexed, and why.
func (m *Manager) Health() Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.vols) == 0 {
		return Health{State: StateIdle, Key: "index_idle", Detail: "there are no folders to index"}
	}
	drives := make([]string, 0, len(m.vols))
	for d := range m.vols {
		drives = append(drives, d)
	}
	sort.Strings(drives)
	var entries int
	var building []string
	for _, d := range drives {
		vs := m.vols[d]
		if vs.idx != nil {
			entries += vs.idx.stats()
		}
		switch {
		case vs.err != nil && vs.idx == nil && !vs.building:
			switch {
			case errors.Is(vs.err, ErrAccessDenied):
				return Health{State: StateDegraded, Key: "index_no_admin", Detail: "the service needs administrator rights to read the volumes, so it cannot index them"}
			case errors.Is(vs.err, ErrJournalOff):
				return Health{State: StateDegraded, Key: "index_journal_off", Args: []any{d}, Detail: fmt.Sprintf("%s has no change journal: enable it with: fsutil usn createjournal m=33554432 a=8388608 %s", d, d)}
			case errors.Is(vs.err, ErrUnsupported):
				return Health{State: StateDegraded, Key: "index_unsupported", Args: []any{d}, Detail: fmt.Sprintf("%s cannot be indexed (it is not an NTFS volume of this computer)", d)}
			}
			return Health{State: StateDegraded, Key: "index_failed", Args: []any{d}, Detail: fmt.Sprintf("%s could not be indexed (see the log of the service)", d)}
		case vs.idx == nil:
			building = append(building, d)
		}
	}
	if len(building) > 0 {
		return Health{State: StateDegraded, Key: "index_building", Args: []any{strings.Join(building, ", ")}, Detail: "the index of " + strings.Join(building, ", ") + " is being built"}
	}
	return Health{State: StateOK, Key: "index_ok", Args: []any{entries, strings.Join(drives, ", ")},
		Detail: fmt.Sprintf("%d entries on %s", entries, strings.Join(drives, ", "))}
}
