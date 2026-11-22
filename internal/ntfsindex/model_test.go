package ntfsindex

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// model is a volume in memory for the tests: a tree of files that can be changed, and the change journal that goes with
// every change, written the way NTFS writes it (a create, a delete, two records for a rename, the extension of a file).
type model struct {
	mu    sync.Mutex
	files map[uint64]*mfile
	next  uint64
	usn   int64
	first int64
	id    uint64
	recs  []usnRecord
	clock int64
	// perCall is how many records one ReadJournal call returns, so that the loop of sync is exercised.
	perCall int
	listErr error
	// lists counts the listings of the volume: Dirs and Entries.
	lists int
	// journalErr is what Journal answers when it is set; gate, when it is set, holds up Entries until it is closed;
	// closed counts the calls of Close.
	journalErr error
	gate       chan struct{}
	closed     int
}

type mfile struct {
	frn, parent uint64
	name        string
	attrs       uint32
	size, mtime int64
}

const modelRoot = 5

// ref gives a file number the sequence number that a file reference carries.
func ref(frn uint64) uint64 { return frn | 7<<48 }

func newModel() *model {
	m := &model{files: map[uint64]*mfile{}, next: 100, usn: 1000, first: 1000, id: 0xABCD, clock: filetime(time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)), perCall: 7}
	m.files[modelRoot] = &mfile{frn: modelRoot, parent: modelRoot, name: "", attrs: attrDirectory}
	return m
}

func (m *model) now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fromFiletime(m.clock)
}

// tick moves the clock of the model on by a second and returns the new time as a FILETIME.
func (m *model) tick() int64 {
	m.clock += 10_000_000
	return m.clock
}

func (m *model) lookup(path string) (*mfile, bool) {
	drive, names, ok := splitPath(path)
	if !ok || drive != "D:" {
		return nil, false
	}
	cur := m.files[modelRoot]
	for _, name := range names {
		var found *mfile
		for _, f := range m.files {
			if f.parent == cur.frn && f.frn != modelRoot && strings.EqualFold(f.name, name) {
				found = f
				break
			}
		}
		if found == nil {
			return nil, false
		}
		cur = found
	}
	return cur, true
}

func (m *model) record(f *mfile, reason uint32) {
	m.usn++
	m.recs = append(m.recs, usnRecord{FRN: ref(f.frn), Parent: ref(f.parent), USN: m.usn, Time: m.tick(), Reason: reason | reasonClose, Attrs: f.attrs, Name: f.name})
}

func splitLast(path string) (dir, name string) {
	i := strings.LastIndexByte(path, '\\')
	return path[:i], path[i+1:]
}

func (m *model) mustParent(path string) *mfile {
	dir, _ := splitLast(path)
	if len(dir) == 2 {
		dir += `\`
	}
	p, ok := m.lookup(dir)
	if !ok {
		panic("model: no folder " + dir)
	}
	return p
}

// mkdir creates a folder, and the folders above it that are missing.
func (m *model) mkdir(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirLocked(path)
}

func (m *model) mkdirLocked(path string) {
	if _, ok := m.lookup(path); ok {
		return
	}
	dir, name := splitLast(path)
	if len(dir) > 2 {
		m.mkdirLocked(dir)
	}
	p := m.mustParent(path)
	f := &mfile{frn: m.next, parent: p.frn, name: name, attrs: attrDirectory, mtime: m.clock}
	m.next++
	m.files[f.frn] = f
	m.record(f, reasonFileCreate)
}

// create makes a file of the given size, and the folders above it.
func (m *model) create(path string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir, name := splitLast(path)
	m.mkdirLocked(dir)
	p := m.mustParent(path)
	f := &mfile{frn: m.next, parent: p.frn, name: name, attrs: 0x20, size: size, mtime: m.clock}
	m.next++
	m.files[f.frn] = f
	m.record(f, reasonFileCreate)
}

// write gives a file a new size.
func (m *model) write(path string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.lookup(path)
	if !ok {
		panic("model: no file " + path)
	}
	f.size, f.mtime = size, m.clock
	m.record(f, 0x2) // USN_REASON_DATA_EXTEND
}

// remove deletes a file, or a folder with everything in it (the files first, as NTFS reports them).
func (m *model) remove(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.lookup(path)
	if !ok {
		panic("model: no file " + path)
	}
	m.removeLocked(f)
}

func (m *model) removeLocked(f *mfile) {
	var kids []*mfile
	for _, c := range m.files {
		if c.parent == f.frn && c.frn != modelRoot {
			kids = append(kids, c)
		}
	}
	sort.Slice(kids, func(i, j int) bool { return kids[i].frn < kids[j].frn })
	for _, c := range kids {
		m.removeLocked(c)
	}
	m.record(f, reasonFileDelete)
	delete(m.files, f.frn)
}

// rename moves and renames a file or folder: the old name is reported, then the new one.
func (m *model) rename(oldPath, newPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.lookup(oldPath)
	if !ok {
		panic("model: no file " + oldPath)
	}
	m.record(f, reasonRenameOldName)
	dir, name := splitLast(newPath)
	m.mkdirLocked(dir)
	f.parent, f.name = m.mustParent(newPath).frn, name
	m.record(f, reasonRenameNewName)
}

// wrapJournal makes the journal forget everything so far, like a journal that is full.
func (m *model) wrapJournal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.first = m.usn + 1
	m.recs = nil
}

// --- volume ----------------------------------------------------------------------------------------

func (m *model) Journal() (journalState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.journalErr != nil {
		return journalState{}, m.journalErr
	}
	return journalState{ID: m.id, First: m.first, Next: m.usn + 1}, nil
}

// snapshot lists the files of the model in the order of their numbers, as a listing of the volume would.
func (m *model) snapshot() ([]fsEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	m.lists++
	frns := make([]uint64, 0, len(m.files))
	for k := range m.files {
		frns = append(frns, k)
	}
	sort.Slice(frns, func(i, j int) bool { return frns[i] < frns[j] })
	entries := make([]fsEntry, 0, len(frns))
	for _, k := range frns {
		f := m.files[k]
		e := fsEntry{FRN: ref(f.frn), Parent: ref(f.parent), Name: f.name, Attrs: f.attrs, Size: f.size, MTime: f.mtime}
		if f.attrs&attrDirectory != 0 {
			e.Size = 0
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (m *model) Dirs(fn func(frn, parent uint64, attrs uint32)) error {
	entries, err := m.snapshot()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Attrs&attrDirectory != 0 && e.Attrs&attrReparsePoint == 0 {
			fn(e.FRN, e.Parent, e.Attrs)
		}
	}
	return nil
}

func (m *model) Entries(want func(frn, parent uint64, attrs uint32) bool, fn func(fsEntry)) error {
	m.mu.Lock()
	gate := m.gate
	m.mu.Unlock()
	if gate != nil {
		<-gate
	}
	entries, err := m.snapshot()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if want(e.FRN, e.Parent, e.Attrs) {
			fn(e)
		}
	}
	return nil
}

func (m *model) FileID(path string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.lookup(path)
	if !ok || f.attrs&attrDirectory == 0 {
		return 0, os.ErrNotExist
	}
	return ref(f.frn), nil
}

func (m *model) ReadJournal(id uint64, from int64, fn func(usnRecord)) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.id || from < m.first {
		return 0, ErrJournalLost
	}
	next, n := from, 0
	for _, r := range m.recs {
		if r.USN < from {
			continue
		}
		if n == m.perCall {
			break
		}
		fn(r)
		next, n = r.USN+1, n+1
	}
	return next, nil
}

func (m *model) Stat(path string) (int64, int64, uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.lookup(path)
	if !ok {
		return 0, 0, 0, os.ErrNotExist
	}
	return f.size, f.mtime, f.attrs, nil
}

func (m *model) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed++
	return nil
}

var _ volume = (*model)(nil)

// tree builds a small tree: a monitored folder with files and folders, and a folder next to it that is not monitored.
func (m *model) tree() {
	m.create(`D:\data\team\a.txt`, 10)
	m.create(`D:\data\team\docs\b.txt`, 20)
	m.create(`D:\data\team\docs\Report 2024.docx`, 30)
	m.create(`D:\data\team\docs\deep\er\c.txt`, 40)
	m.create(`D:\data\team\Needle Folder\inside.txt`, 5)
	m.create(`D:\data\other\a.txt`, 1000)
	m.create(`D:\elsewhere\needle.txt`, 7)
}

// built returns the index of the monitored folders, built from the model.
func (m *model) built(paths ...string) (*index, []*rootInfo) {
	roots := make([]*rootInfo, 0, len(paths))
	for _, p := range paths {
		r, ok := newRootInfo(p)
		if !ok {
			panic(fmt.Sprintf("bad root %q", p))
		}
		roots = append(roots, r)
	}
	ix, err := build(m, roots, m.now)
	if err != nil {
		panic(err)
	}
	return ix, roots
}

var errBoom = errors.New("boom")
