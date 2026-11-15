package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"timevault/internal/catalog"
	"timevault/internal/pathutil"
	"timevault/internal/platform"
	"timevault/internal/repository"
)

const (
	tickInterval    = 250 * time.Millisecond
	maxSkipEvents   = 200
	unsupportedName = "unsupported name"
)

type itemKind int

const (
	kindFile itemKind = iota
	kindDir
	kindSpecial
)

// item is one directory entry as listed from disk.
type item struct {
	name    string
	key     string
	kind    itemKind
	size    int64
	mtimeNS int64
	attrs   uint32
	reason  string
}

// slot pairs a listed item with its previous recorded state and what will be recorded now.
type slot struct {
	item    item
	prev    catalog.Entry
	has     bool
	final   catalog.Entry
	change  *catalog.Change
	capture bool
	res     captured
}

// scan is the state of one backup run. dir runs on a single goroutine; only
// the content copies of a folder run in parallel.
type scan struct {
	e        *Engine
	src      catalog.Source
	point    catalog.Point
	prevSeq  int
	rehash   bool
	root     *os.Root
	excl     *excluder
	rep      Reporter
	manifest *catalog.Manifest
	stats    catalog.Stats
	refs     map[repository.Hash]int64
	prog     Progress

	// rootReal is the source folder with junctions resolved; protected are the
	// folders that must never be read, as configured and as they really are.
	rootReal  string
	protected []string

	lastTick   time.Time
	skipEvents int
}

func (sc *scan) isProtected(abs string) bool {
	for _, p := range sc.protected {
		if pathutil.Overlaps(p, abs) {
			return true
		}
	}
	return false
}

func (sc *scan) tick(path string) {
	if time.Since(sc.lastTick) < tickInterval {
		return
	}
	sc.lastTick = time.Now()
	p := sc.prog
	p.Phase = PhaseScanning
	p.CurrentPath = path
	sc.rep.Progress(p)
}

func (sc *scan) warn(format string, args ...any) {
	if sc.skipEvents < maxSkipEvents {
		sc.rep.Event("warn", fmt.Sprintf(format, args...))
	} else if sc.skipEvents == maxSkipEvents {
		sc.rep.Event("warn", "further skipped items are counted but not listed")
	}
	sc.skipEvents++
}

func (sc *scan) readDir(rel string) ([]item, error) {
	if sc.e.failReadDir != nil {
		if err := sc.e.failReadDir(rel); err != nil {
			return nil, err
		}
	}
	if rel != "" {
		// The parent listing saw a plain folder; a link swapped in since then must not be followed.
		st, err := sc.root.Lstat(osRel(rel))
		if err != nil {
			return nil, err
		}
		if !st.IsDir() || st.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil, errors.New("it is no longer a plain folder")
		}
	}
	d, err := sc.root.Open(osRel(rel))
	if err != nil {
		return nil, err
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return nil, err
	}
	// Browsing and restoring accept paths of at most MaxRelDepth folders, so nothing deeper is recorded.
	depth := 1
	if rel != "" {
		depth = strings.Count(rel, "/") + 2
	}
	tooDeep := depth > pathutil.MaxRelDepth
	items := make([]item, 0, len(entries))
	for _, de := range entries {
		name := de.Name()
		if err := pathutil.ValidateName(name); err != nil {
			sc.stats.SkippedCount++
			sc.warn("%s: %s (%v)", pathutil.JoinRel(rel, name), unsupportedName, err)
			continue
		}
		if sc.excl.match(rel, name) {
			continue
		}
		if de.IsDir() && sc.isProtected(filepath.Join(sc.rootReal, filepath.FromSlash(rel), name)) {
			sc.stats.SkippedCount++
			sc.warn("%s: skipped because it holds the repository or restore folder", pathutil.JoinRel(rel, name))
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue // vanished between listing and stat
		}
		it := item{name: name, key: pathutil.NameKey(name), attrs: platform.FileAttributes(info)}
		mode := info.Mode()
		switch {
		case mode&(os.ModeSymlink|os.ModeIrregular) != 0:
			it.kind, it.reason = kindSpecial, "reparse point (junction or symlink) is not followed"
		case tooDeep:
			it.kind, it.reason = kindSpecial, fmt.Sprintf("the path is deeper than %d folders", pathutil.MaxRelDepth)
		case mode.IsDir():
			it.kind = kindDir
		case mode.IsRegular():
			it.kind, it.size, it.mtimeNS = kindFile, info.Size(), info.ModTime().UnixNano()
		default:
			it.kind, it.reason = kindSpecial, "not a regular file"
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].key != items[j].key {
			return items[i].key < items[j].key
		}
		return items[i].name < items[j].name
	})
	return items, nil
}

func (sc *scan) newChange(s *slot, final catalog.Entry) *catalog.Change {
	c := &catalog.Change{
		Kind: catalog.ChangeAdd, Name: s.item.name,
		IsDir: final.IsDir, State: final.State, SkipReason: final.SkipReason,
		Size: final.Size, MTimeNS: final.MTimeNS, Attrs: final.Attrs, SHA256: final.SHA256,
	}
	if s.has {
		c.Kind, c.NodeID, c.VersionID, c.WasDir = catalog.ChangeModify, s.prev.NodeID, s.prev.VersionID, s.prev.IsDir
	}
	return c
}

// plan decides, without reading file content, whether an item is unchanged,
// changed (and can be recorded right away) or needs its content captured.
func (sc *scan) plan(s *slot) {
	it, prev := s.item, s.prev
	base := catalog.Entry{Name: it.name, Attrs: it.attrs, State: catalog.VersionOK}
	switch it.kind {
	case kindDir:
		base.IsDir = true
		if s.has && prev.IsDir {
			s.final = prev
			return
		}
		s.final = base
		s.change = sc.newChange(s, base)
	case kindSpecial:
		sc.stats.SkippedCount++
		sc.warn("%s: %s", it.name, it.reason)
		base.State, base.SkipReason = catalog.VersionSkipped, it.reason
		if s.has && !prev.IsDir && prev.State == catalog.VersionSkipped && prev.SkipReason == it.reason {
			s.final = prev
			return
		}
		s.final = base
		s.change = sc.newChange(s, base)
	case kindFile:
		base.Size, base.MTimeNS = it.size, it.mtimeNS
		if s.has && !prev.IsDir && prev.State == catalog.VersionOK && prev.Size == it.size && prev.MTimeNS == it.mtimeNS && !sc.rehash {
			if prev.Attrs == it.attrs {
				s.final = prev
				return
			}
			base.SHA256 = prev.SHA256
			s.final = base
			s.change = sc.newChange(s, base)
			return
		}
		s.capture = true
	}
}

// resolveCapture turns the outcome of a content copy into the recorded state.
func (sc *scan) resolveCapture(s *slot, rel string) {
	r := s.res
	if r.skip != "" {
		sc.stats.SkippedCount++
		prev := s.prev
		if s.has && !prev.IsDir && prev.State == catalog.VersionOK {
			sc.warn("%s: not captured (%s); the previous version is kept", rel, r.skip)
			s.final = prev
			return
		}
		sc.warn("%s: not captured (%s)", rel, r.skip)
		base := catalog.Entry{Name: s.item.name, Attrs: s.item.attrs, State: catalog.VersionSkipped, SkipReason: r.skip}
		if s.has && !prev.IsDir && prev.State == catalog.VersionSkipped && prev.SkipReason == r.skip {
			s.final = prev
			return
		}
		s.final = base
		s.change = sc.newChange(s, base)
		return
	}

	sc.prog.BytesHashed += r.size
	if r.created {
		sc.prog.NewObjects++
		sc.stats.NewObjectCount++
		sc.stats.NewObjectBytes += r.size
	}
	sha := r.hash[:]
	final := catalog.Entry{Name: s.item.name, State: catalog.VersionOK, Size: r.size, MTimeNS: r.mtimeNS, Attrs: r.attrs, SHA256: append([]byte(nil), sha...)}
	prev := s.prev
	if s.has && !prev.IsDir && prev.State == catalog.VersionOK && prev.Size == final.Size &&
		prev.MTimeNS == final.MTimeNS && prev.Attrs == final.Attrs && bytes.Equal(prev.SHA256, final.SHA256) {
		s.final = prev
		return
	}
	s.final = final
	s.change = sc.newChange(s, final)
	sc.refs[r.hash] = r.size
}

func (sc *scan) captureAll(ctx context.Context, rel string, slots []slot, idx []int) error {
	if len(idx) == 0 {
		return nil
	}
	workers := min(sc.e.hashWorkers, len(idx))
	queue := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				slots[i].res = sc.capture(ctx, pathutil.JoinRel(rel, slots[i].item.name))
			}
		}()
	}
feed:
	for _, i := range idx {
		select {
		case queue <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, i := range idx {
		if err := slots[i].res.err; err != nil {
			return err
		}
	}
	return nil
}

// dir records one folder and, depth first, everything below it.
func (sc *scan) dir(ctx context.Context, parent int64, rel string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sc.tick(rel)
	items, err := sc.readDir(rel)
	if err != nil {
		if rel == "" {
			return fmt.Errorf("read source folder: %w", err)
		}
		return sc.keepPrevious(ctx, parent, rel, err)
	}
	existing, err := sc.e.cat.ChildrenAt(ctx, parent, sc.prevSeq)
	if err != nil {
		return err
	}

	slots := make([]slot, len(items))
	seen := make(map[string]struct{}, len(items))
	var toCapture []int
	for i := range items {
		s := &slots[i]
		s.item = items[i]
		s.prev, s.has = existing[s.item.name]
		seen[s.item.name] = struct{}{}
		sc.plan(s)
		if s.capture {
			toCapture = append(toCapture, i)
		}
	}
	if err := sc.captureAll(ctx, rel, slots, toCapture); err != nil {
		return err
	}
	for _, i := range toCapture {
		sc.resolveCapture(&slots[i], pathutil.JoinRel(rel, slots[i].item.name))
	}

	var changes []catalog.Change
	var objects []catalog.ObjectRef
	for i := range slots {
		c := slots[i].change
		if c == nil {
			continue
		}
		changes = append(changes, *c)
		if len(c.SHA256) > 0 {
			objects = append(objects, catalog.ObjectRef{SHA256: c.SHA256, Size: c.Size})
		}
	}
	for name, prev := range existing {
		if _, ok := seen[name]; ok {
			continue
		}
		changes = append(changes, catalog.Change{Kind: catalog.ChangeDelete, Name: name, NodeID: prev.NodeID, VersionID: prev.VersionID, WasDir: prev.IsDir})
		sc.stats.DeletedCount++
		sc.prog.Deleted++
	}
	res, err := sc.e.cat.ApplyDir(ctx, sc.point, sc.prevSeq, parent, changes, objects)
	if err != nil {
		return err
	}
	sc.stats.DeletedCount += res.DescendantsClosed
	sc.prog.Deleted += res.DescendantsClosed

	for i := range slots {
		s := &slots[i]
		if id, ok := res.NodeIDs[s.item.name]; ok {
			s.final.NodeID = id
		}
		if s.change != nil {
			if s.change.Kind == catalog.ChangeAdd {
				sc.stats.AddedCount++
				sc.prog.Added++
			} else {
				sc.stats.ModifiedCount++
				sc.prog.Modified++
			}
		}
		relPath := pathutil.JoinRel(rel, s.item.name)
		sc.manifest.Add(relPath, catalog.ManifestOf(s.final))
		sc.count(s.final)
		if s.final.IsDir {
			if err := sc.dir(ctx, s.final.NodeID, relPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (sc *scan) count(e catalog.Entry) {
	if e.IsDir {
		sc.stats.DirCount++
		sc.prog.Dirs++
		return
	}
	sc.stats.FileCount++
	sc.prog.Files++
	if e.State == catalog.VersionOK {
		sc.stats.TotalBytes += e.Size
	}
}

// keepPrevious carries a folder that could not be listed over unchanged: the
// folder still exists, so treating its unseen contents as deleted would erase
// history that was merely unreadable this time.
func (sc *scan) keepPrevious(ctx context.Context, parent int64, rel string, cause error) error {
	sc.stats.SkippedCount++
	sc.warn("%s: cannot be read (%v); its previous state is kept", rel, skipReason("cannot list", cause))
	return sc.e.cat.WalkAt(ctx, parent, sc.prevSeq, rel, func(r string, e catalog.Entry) error {
		sc.manifest.Add(r, catalog.ManifestOf(e))
		sc.count(e)
		return nil
	})
}
