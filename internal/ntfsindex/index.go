package ntfsindex

import (
	"cmp"
	"container/heap"
	"slices"
	"strings"
	"sync"
	"time"
)

const noNode int32 = -1

// node is one file or folder of the index. The children of a folder are a doubly linked list, so that a folder can be
// searched, listed and removed without a slice per folder.
type node struct {
	frn                    uint64
	parent                 int32
	firstChild, next, prev int32
	// rootID is the number of the registered folder that this node is, or -1.
	rootID int32
	attrs  uint32
	// size is the length of the data of a file, -1 until it has been read.
	size  int64
	mtime int64
	// name is the name as the file system writes it, lower the same in lower case for matching. When the name is in
	// lower case already, both are the same string.
	name, lower string
	// subSize and subItems are what is below a folder, and the folder itself in subItems; they are valid while
	// index.aggDirty is false.
	subSize, subItems int64
}

func (n *node) isDir() bool { return n.attrs&attrDirectory != 0 }

// rootInfo is a folder that the index was asked to hold.
type rootInfo struct {
	// path is how the folder was registered, without a trailing backslash; volume is its drive ("D:") and key the
	// names of the folders below the drive in lower case.
	path   string
	volume string
	key    []string
	// slot is the node of the folder, noNode when it is not there.
	slot int32
	// lastChange is the time (a FILETIME) of the newest change that the journal reported below the folder.
	lastChange int64
}

// splitPath splits a path of a drive letter, such as D:\Data\Team. It refuses what is not one: a path of a share, a
// path relative to the current folder of a drive, and paths with empty, "." or ".." folders.
func splitPath(p string) (drive string, names []string, ok bool) {
	p = strings.ReplaceAll(p, "/", `\`)
	if len(p) < 2 || !(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z') || p[1] != ':' || (len(p) > 2 && p[2] != '\\') {
		return "", nil, false
	}
	drive = strings.ToUpper(p[:2])
	rest := strings.Trim(p[2:], `\`)
	if rest == "" {
		return drive, nil, true
	}
	for _, s := range strings.Split(rest, `\`) {
		if s == "" || s == "." || s == ".." {
			return "", nil, false
		}
		names = append(names, s)
	}
	return drive, names, true
}

func foldAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ToLower(n)
	}
	return out
}

func newRootInfo(path string) (*rootInfo, bool) {
	drive, names, ok := splitPath(path)
	if !ok {
		return nil, false
	}
	clean := drive + `\` + strings.Join(names, `\`)
	if len(names) == 0 {
		clean = drive + `\`
	}
	return &rootInfo{path: clean, volume: drive, key: foldAll(names), slot: noNode}, true
}

// index is the index of the folders of one volume.
type index struct {
	mu       sync.RWMutex
	nodes    []node
	free     []int32
	byFRN    map[uint64]int32
	roots    []*rootInfo
	aggDirty bool

	journalID uint64
	nextUSN   int64
	// liveSince is the time (a FILETIME) since which every change has been seen: the index was taken from the volume
	// and the journal was read from then on. A question about what changed before that time cannot be answered.
	liveSince int64
	// dirty holds the files whose size and time have to be read again.
	dirty map[int32]struct{}
	// rebuild says that the index cannot go on from the journal alone and has to be built again.
	rebuild bool
	// syncMu serialises the reading of the journal.
	syncMu sync.Mutex
}

func newIndex(roots []*rootInfo) *index {
	return &index{byFRN: map[uint64]int32{}, roots: roots, dirty: map[int32]struct{}{}, aggDirty: true}
}

func (ix *index) alloc() int32 {
	if n := len(ix.free); n > 0 {
		s := ix.free[n-1]
		ix.free = ix.free[:n-1]
		return s
	}
	ix.nodes = append(ix.nodes, node{})
	return int32(len(ix.nodes) - 1)
}

func (ix *index) release(s int32) {
	n := &ix.nodes[s]
	if n.rootID >= 0 {
		ix.roots[n.rootID].slot = noNode
	}
	delete(ix.byFRN, n.frn)
	delete(ix.dirty, s)
	*n = node{parent: noNode, firstChild: noNode, next: noNode, prev: noNode, rootID: -1}
	ix.free = append(ix.free, s)
}

func (ix *index) init(s int32, frn uint64, name string, attrs uint32, size, mtime int64) {
	// ToLower returns the name itself when it has no capital, so such a name is held once.
	ix.nodes[s] = node{frn: frn, parent: noNode, firstChild: noNode, next: noNode, prev: noNode, rootID: -1, attrs: attrs, size: size, mtime: mtime, name: name, lower: strings.ToLower(name)}
	ix.byFRN[frn] = s
}

func (ix *index) link(s, parent int32) {
	n := &ix.nodes[s]
	n.parent, n.prev, n.next = parent, noNode, ix.nodes[parent].firstChild
	if n.next != noNode {
		ix.nodes[n.next].prev = s
	}
	ix.nodes[parent].firstChild = s
}

func (ix *index) unlink(s int32) {
	n := &ix.nodes[s]
	switch {
	case n.prev != noNode:
		ix.nodes[n.prev].next = n.next
	case n.parent != noNode:
		ix.nodes[n.parent].firstChild = n.next
	}
	if n.next != noNode {
		ix.nodes[n.next].prev = n.prev
	}
	n.prev, n.next, n.parent = noNode, noNode, noNode
}

// below reports whether s is inside the folder of top (or is it).
func (ix *index) below(s, top int32) bool {
	for ; s != noNode; s = ix.nodes[s].parent {
		if s == top {
			return true
		}
	}
	return false
}

// remove takes a node and everything below it out of the index.
func (ix *index) remove(s int32) {
	ix.unlink(s)
	stack := []int32{s}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for c := ix.nodes[cur].firstChild; c != noNode; c = ix.nodes[c].next {
			stack = append(stack, c)
		}
		ix.release(cur)
	}
	ix.aggDirty = true
}

// touch records a change at time ft (a FILETIME) for every registered folder that contains the node.
func (ix *index) touch(s int32, ft int64) {
	for ; s != noNode; s = ix.nodes[s].parent {
		if id := ix.nodes[s].rootID; id >= 0 && ft > ix.roots[id].lastChange {
			ix.roots[id].lastChange = ft
		}
	}
}

// path is the full path of a node: the path of the registered folder it is below, and the names from there on.
func (ix *index) path(s int32) string {
	var names []string
	for ; ix.nodes[s].parent != noNode; s = ix.nodes[s].parent {
		names = append(names, ix.nodes[s].name)
	}
	top := &ix.nodes[s]
	base := top.name
	if top.rootID >= 0 {
		base = ix.roots[top.rootID].path
	}
	if len(names) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(base, `\`))
	for i := len(names) - 1; i >= 0; i-- {
		b.WriteByte('\\')
		b.WriteString(names[i])
	}
	return b.String()
}

// child finds the child of a folder by its name in lower case.
func (ix *index) child(s int32, lower string) int32 {
	for c := ix.nodes[s].firstChild; c != noNode; c = ix.nodes[c].next {
		if ix.nodes[c].lower == lower {
			return c
		}
	}
	return noNode
}

// deepestRoot is the registered folder that is the longest start of names (in lower case) and is in the index.
func (ix *index) deepestRoot(drive string, names []string) int {
	best := -1
	for i, r := range ix.roots {
		if r.slot == noNode || r.volume != drive || len(r.key) > len(names) || !slices.Equal(r.key, names[:len(r.key)]) {
			continue
		}
		if best < 0 || len(r.key) > len(ix.roots[best].key) {
			best = i
		}
	}
	return best
}

// resolve finds the node of a folder, and the registered folder that contains it.
func (ix *index) resolve(drive string, names []string) (slot int32, root int, ok bool) {
	root = ix.deepestRoot(drive, names)
	if root < 0 {
		return noNode, -1, false
	}
	slot = ix.roots[root].slot
	for _, name := range names[len(ix.roots[root].key):] {
		if slot = ix.child(slot, name); slot == noNode {
			return noNode, -1, false
		}
	}
	return slot, root, true
}

// --- Building ---------------------------------------------------------------------------------------

// build reads a volume and returns the index of the folders in roots. The change journal is read from the moment the
// listing starts, so a change that happens meanwhile is applied afterwards (every change is applied in a way that
// gives the same result when it was already in the listing).
func build(vol volume, roots []*rootInfo, now func() time.Time) (*index, error) {
	js, err := vol.Journal()
	if err != nil {
		return nil, err
	}
	liveSince := filetime(now())

	rootKeys := map[uint64]int32{}
	for i, r := range roots {
		r.slot, r.lastChange = noNode, 0
		if ref, err := vol.FileID(r.path); err == nil {
			rootKeys[frnKey(ref)] = int32(i)
		}
	}

	// First pass: the folders, to find out which of them are inside the registered ones.
	dirs := make(map[uint64]uint64)
	if err := vol.Dirs(func(frn, parent uint64, _ uint32) { dirs[frnKey(frn)] = frnKey(parent) }); err != nil {
		return nil, err
	}
	memo := make(map[uint64]bool)

	// Second pass: what is inside, with sizes and times. A name is only read for a file that is inside.
	ix := newIndex(roots)
	ix.journalID, ix.nextUSN, ix.liveSince = js.ID, js.Next, liveSince
	var parents []uint64
	want := func(frn, parent uint64, _ uint32) bool {
		key := frnKey(frn)
		if _, dup := ix.byFRN[key]; dup {
			return false // another name of a file that is in already: only the first is held
		}
		if _, isRoot := rootKeys[key]; isRoot {
			return true
		}
		return inScope(frnKey(parent), dirs, rootKeys, memo)
	}
	if err := vol.Entries(want, func(e fsEntry) {
		key := frnKey(e.FRN)
		if _, dup := ix.byFRN[key]; dup {
			return
		}
		s := ix.alloc()
		ix.init(s, key, e.name(), e.Attrs, e.Size, e.MTime)
		parents = append(parents, frnKey(e.Parent))
		if rid, isRoot := rootKeys[key]; isRoot {
			ix.nodes[s].rootID = rid
			roots[rid].slot = s
		}
	}); err != nil {
		return nil, err
	}
	for s := range ix.nodes {
		if p, ok := ix.byFRN[parents[s]]; ok && p != int32(s) {
			ix.link(int32(s), p)
		}
	}
	return ix, nil
}

// inScope reports whether the folder with the number k is a registered folder or inside one, following the folders up
// to the first one that is known to be, or to be not. memo remembers the answer for every folder it passed.
func inScope(k uint64, dirs map[uint64]uint64, roots map[uint64]int32, memo map[uint64]bool) bool {
	var chain []uint64
	res := false
	for {
		if v, ok := memo[k]; ok {
			res = v
			break
		}
		if _, isRoot := roots[k]; isRoot {
			res = true
			chain = append(chain, k)
			break
		}
		p, ok := dirs[k]
		if !ok {
			break
		}
		chain = append(chain, k)
		if p == k {
			break
		}
		k = p
	}
	for _, c := range chain {
		memo[c] = res
	}
	return res
}

// --- Keeping up with the journal -------------------------------------------------------------------

// apply takes one record of the journal into the index. It is safe to apply a record twice, or a record of a change that
// the listing already showed.
func (ix *index) apply(rec usnRecord) {
	if !rec.changed() {
		return
	}
	key := frnKey(rec.FRN)
	slot, known := ix.byFRN[key]
	switch {
	case rec.Reason&reasonFileDelete != 0:
		if known {
			ix.touch(slot, rec.Time)
			ix.remove(slot)
		} else if ps, ok := ix.byFRN[frnKey(rec.Parent)]; ok {
			ix.touch(ps, rec.Time)
		}
		return
	case rec.Reason&(reasonRenameOldName|reasonRenameNewName|reasonFileCreate) == reasonRenameOldName:
		// The old name of a rename: the new name follows in its own record, which moves the file.
		if known {
			ix.touch(slot, rec.Time)
		}
		return
	}
	parent, parentKnown := ix.byFRN[frnKey(rec.Parent)]
	isDir := rec.Attrs&attrDirectory != 0
	switch {
	case known && parentKnown:
		n := &ix.nodes[slot]
		if n.parent != parent || n.name != rec.Name {
			ix.touch(slot, rec.Time)
			if n.parent != parent {
				if ix.below(parent, slot) {
					ix.rebuild = true
					return
				}
				ix.unlink(slot)
				ix.link(slot, parent)
			}
			if n.name != rec.Name {
				n.name = rec.Name
				n.lower = strings.ToLower(rec.Name)
			}
			ix.aggDirty = true
		}
		n.attrs = rec.Attrs
		ix.touch(slot, rec.Time)
		if !isDir {
			ix.dirty[slot] = struct{}{}
		}
	case known && ix.nodes[slot].parent == noNode:
		// A registered folder whose own folder is not held: what happens to it is a change, not a move out.
		ix.touch(slot, rec.Time)
	case known:
		// Moved out of the folders that are held.
		ix.touch(slot, rec.Time)
		ix.remove(slot)
	case parentKnown:
		s := ix.alloc()
		ix.init(s, key, rec.Name, rec.Attrs, -1, 0)
		ix.link(s, parent)
		ix.aggDirty = true
		ix.touch(s, rec.Time)
		if isDir && rec.Reason&reasonFileCreate == 0 {
			// A folder that comes from outside brings files that nobody listed.
			ix.rebuild = true
		}
		if !isDir {
			ix.dirty[s] = struct{}{}
		}
	}
}

// sync reads the journal up to its end and applies it. It returns ErrJournalLost when the index has to be built again.
func (ix *index) sync(vol volume) error {
	ix.syncMu.Lock()
	defer ix.syncMu.Unlock()
	for {
		var recs []usnRecord
		from := ix.nextUSN
		next, err := vol.ReadJournal(ix.journalID, from, func(r usnRecord) { recs = append(recs, r) })
		if err != nil {
			return err
		}
		ix.mu.Lock()
		for _, r := range recs {
			ix.apply(r)
		}
		ix.nextUSN = next
		ix.mu.Unlock()
		if len(recs) == 0 || next <= from {
			return nil
		}
	}
}

// refresh reads the size and the time again of the files that changed: as many as the budget allows, then the rest
// waits for the next call.
func (ix *index) refresh(vol volume, budget int, deadline time.Time) {
	type job struct {
		slot int32
		frn  uint64
		path string
	}
	type result struct {
		job
		size, mtime int64
		attrs       uint32
	}
	for budget > 0 && time.Now().Before(deadline) {
		ix.mu.Lock()
		var jobs []job
		for s := range ix.dirty {
			if len(jobs) >= 256 || len(jobs) >= budget {
				break
			}
			jobs = append(jobs, job{s, ix.nodes[s].frn, ix.path(s)})
		}
		for _, j := range jobs {
			delete(ix.dirty, j.slot)
		}
		ix.mu.Unlock()
		if len(jobs) == 0 {
			return
		}
		budget -= len(jobs)
		var results []result
		for _, j := range jobs {
			if size, mtime, attrs, err := vol.Stat(j.path); err == nil {
				results = append(results, result{j, size, mtime, attrs})
			}
		}
		ix.mu.Lock()
		for _, r := range results {
			// The slot may have been given to another file meanwhile.
			if int(r.slot) < len(ix.nodes) && ix.nodes[r.slot].frn == r.frn {
				n := &ix.nodes[r.slot]
				n.size, n.mtime, n.attrs = r.size, r.mtime, r.attrs
				ix.aggDirty = true
			}
		}
		ix.mu.Unlock()
	}
}

// --- Questions -------------------------------------------------------------------------------------

// ensureAgg makes the sizes and counts below the folders current. It leaves the read lock held, as it found it.
func (ix *index) ensureAgg() {
	if !ix.aggDirty {
		return
	}
	ix.mu.RUnlock()
	ix.mu.Lock()
	if ix.aggDirty {
		ix.computeAgg()
		ix.aggDirty = false
	}
	ix.mu.Unlock()
	ix.mu.RLock()
}

// computeAgg works out subSize and subItems of every node. The caller holds the write lock.
func (ix *index) computeAgg() {
	order := make([]int32, 0, len(ix.nodes))
	var stack []int32
	for s := range ix.nodes {
		n := &ix.nodes[s]
		if n.frn == 0 && n.parent == noNode && n.name == "" {
			continue // a free slot
		}
		if n.parent != noNode {
			continue
		}
		stack = append(stack[:0], int32(s))
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			order = append(order, cur)
			for c := ix.nodes[cur].firstChild; c != noNode; c = ix.nodes[c].next {
				stack = append(stack, c)
			}
		}
	}
	for _, s := range order {
		n := &ix.nodes[s]
		n.subItems = 1
		n.subSize = 0
		if !n.isDir() && n.size > 0 {
			n.subSize = n.size
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		n := &ix.nodes[order[i]]
		if p := n.parent; p != noNode {
			ix.nodes[p].subItems += n.subItems
			ix.nodes[p].subSize += n.subSize
		}
	}
}

// Entry is one file or folder found by a search.
type Entry struct {
	Name, Path string
	IsDir      bool
	// Size is the length of a file, -1 for a folder and for a file whose length is not known yet.
	Size int64
	// MTime is the time of the last write as a FILETIME; 0 when it is not known.
	MTime int64
}

// Page is one page of the results of a search.
type Page struct {
	Items []Entry
	// Total is the number of matches, not only those of the page.
	Total int
}

func containsAll(lower string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(lower, t) {
			return false
		}
	}
	return true
}

// search finds the files and folders below a node whose names contain every term (in lower case). Folders come first,
// then by name.
func (ix *index) search(slot int32, terms []string, offset, limit int) Page {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var matches []int32
	stack := []int32{}
	for c := ix.nodes[slot].firstChild; c != noNode; c = ix.nodes[c].next {
		stack = append(stack, c)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &ix.nodes[cur]
		if containsAll(n.lower, terms) {
			matches = append(matches, cur)
		}
		for c := n.firstChild; c != noNode; c = ix.nodes[c].next {
			stack = append(stack, c)
		}
	}
	page := Page{Total: len(matches)}
	less := func(a, b int32) int {
		na, nb := &ix.nodes[a], &ix.nodes[b]
		if da, db := na.isDir(), nb.isDir(); da != db {
			if da {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(na.lower, nb.lower); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(ix.path(a)), strings.ToLower(ix.path(b)))
	}
	if offset >= len(matches) {
		return page
	}
	want := min(offset+limit, len(matches))
	var ordered []int32
	if want*4 < len(matches) && len(matches) > 50_000 {
		ordered = smallest(matches, want, less)
	} else {
		slices.SortFunc(matches, less)
		ordered = matches[:want]
	}
	for _, s := range ordered[offset:want] {
		n := &ix.nodes[s]
		e := Entry{Name: n.name, Path: ix.path(s), IsDir: n.isDir(), Size: n.size, MTime: n.mtime}
		if e.IsDir {
			e.Size = -1
		}
		page.Items = append(page.Items, e)
	}
	return page
}

// smallest returns the k smallest of items in order, without sorting all of them.
func smallest(items []int32, k int, less func(a, b int32) int) []int32 {
	h := &maxHeap{less: less}
	for _, it := range items {
		switch {
		case len(h.items) < k:
			heap.Push(h, it)
		case less(it, h.items[0]) < 0:
			h.items[0] = it
			heap.Fix(h, 0)
		}
	}
	out := h.items
	slices.SortFunc(out, less)
	return out
}

type maxHeap struct {
	items []int32
	less  func(a, b int32) int
}

func (h *maxHeap) Len() int           { return len(h.items) }
func (h *maxHeap) Less(i, j int) bool { return h.less(h.items[i], h.items[j]) > 0 }
func (h *maxHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *maxHeap) Push(x any)         { h.items = append(h.items, x.(int32)) }
func (h *maxHeap) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items = h.items[:n-1]
	return x
}

// Folder is a folder directly inside another, with everything below it added up.
type Folder struct {
	Name string
	// Size is the sum of the lengths of the files below the folder.
	Size int64
	// MTime is the time of the last write of the folder itself as a FILETIME; 0 when it is not known.
	MTime int64
}

// subfolders lists the folders directly inside a node by name, at most limit of them (0: all); more says that there are
// further ones.
func (ix *index) subfolders(slot int32, limit int) (folders []Folder, more bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	ix.ensureAgg()
	for c := ix.nodes[slot].firstChild; c != noNode; c = ix.nodes[c].next {
		if n := &ix.nodes[c]; n.isDir() {
			folders = append(folders, Folder{Name: n.name, Size: n.subSize, MTime: n.mtime})
		}
	}
	slices.SortFunc(folders, func(a, b Folder) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	if limit > 0 && len(folders) > limit {
		return folders[:limit], true
	}
	return folders, false
}

// ProbeResult is what the index knows about a folder tree.
type ProbeResult struct {
	// Items is the number of files and folders below the folder, and the folder itself.
	Items int64
	// Changed is true when something below the folder was created, modified, renamed, moved or deleted since the time
	// asked about, or when the index cannot tell because it has not been watching since then.
	Changed bool
}

// probe summarises the tree below a node. Changes are counted for the registered folder that contains the node, which
// may be larger than the node: it says "changed" more often than needed, never less. A zero since asks for the count only.
func (ix *index) probe(slot int32, root int, since time.Time) ProbeResult {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	ix.ensureAgg()
	res := ProbeResult{Items: ix.nodes[slot].subItems}
	if since.IsZero() {
		return res
	}
	ft := filetime(since)
	res.Changed = ft < ix.liveSince || ix.roots[root].lastChange >= ft
	return res
}

// stats says how much the index holds.
func (ix *index) stats() (items int) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.nodes) - len(ix.free)
}
