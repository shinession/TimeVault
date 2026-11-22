package ntfsindex

import (
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitPath(t *testing.T) {
	good := map[string]struct {
		drive string
		names []string
	}{
		`D:\`:           {"D:", nil},
		`d:`:            {"D:", nil},
		`D:\Data\Team`:  {"D:", []string{"Data", "Team"}},
		`d:/Data/Team/`: {"D:", []string{"Data", "Team"}},
		`E:\a b\ü\😀`:    {"E:", []string{"a b", "ü", "😀"}},
		`D:\Data\\`:     {"D:", []string{"Data"}},
	}
	for in, want := range good {
		drive, names, ok := splitPath(in)
		if !ok || drive != want.drive || !slices.Equal(names, want.names) {
			t.Errorf("splitPath(%q) = %q %v %v, want %q %v", in, drive, names, ok, want.drive, want.names)
		}
	}
	for _, bad := range []string{``, `D`, `D:data`, `\\server\share\x`, `\\?\D:\x`, `D:\a\..\b`, `D:\a\.\b`, `D:\a\\b`, `/x`, `1:\x`} {
		if _, _, ok := splitPath(bad); ok {
			t.Errorf("splitPath(%q) accepted", bad)
		}
	}
}

// find resolves a path to its node.
func find(t *testing.T, ix *index, path string) int32 {
	t.Helper()
	drive, names, ok := splitPath(path)
	if !ok {
		t.Fatalf("bad path %q", path)
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	slot, _, ok := ix.resolve(drive, foldAll(names))
	if !ok {
		return noNode
	}
	return slot
}

func searchPaths(t *testing.T, ix *index, root, text string, offset, limit int) ([]string, int) {
	t.Helper()
	slot := find(t, ix, root)
	if slot == noNode {
		t.Fatalf("%s is not in the index", root)
	}
	page := ix.search(slot, strings.Fields(strings.ToLower(text)), offset, limit)
	var out []string
	for _, e := range page.Items {
		out = append(out, e.Path)
	}
	return out, page.Total
}

func TestBuildHoldsOnlyTheRegisteredFolders(t *testing.T) {
	m := newModel()
	m.tree()
	ix, roots := m.built(`D:\data\team`)
	if got := ix.stats(); got != 10 {
		t.Errorf("the index holds %d entries, want the 10 below and including D:\\data\\team", got)
	}
	if roots[0].slot == noNode {
		t.Fatal("the registered folder has no node")
	}
	if find(t, ix, `D:\data\other`) != noNode || find(t, ix, `D:\data`) != noNode || find(t, ix, `D:\elsewhere`) != noNode {
		t.Error("a folder outside the registered one can be resolved")
	}
	if find(t, ix, `D:\DATA\Team\DOCS\deep`) == noNode {
		t.Error("names are matched without regard to case")
	}
	if m.lists != 2 {
		t.Errorf("the volume was listed %d times, want the two passes", m.lists)
	}
}

func TestSearchMatchesEveryTermInTheNameAndPages(t *testing.T) {
	m := newModel()
	m.tree()
	ix, _ := m.built(`D:\data\team`)
	root := `D:\data\team`

	got, total := searchPaths(t, ix, root, "needle", 0, 50)
	if total != 1 || !reflect.DeepEqual(got, []string{`D:\data\team\Needle Folder`}) {
		t.Errorf("needle = %v (%d)", got, total)
	}
	if got, _ := searchPaths(t, ix, root, "2024 REPORT", 0, 50); !reflect.DeepEqual(got, []string{`D:\data\team\docs\Report 2024.docx`}) {
		t.Errorf("two terms in any order and case = %v", got)
	}
	if got, total := searchPaths(t, ix, root, "nothing like this", 0, 50); len(got) != 0 || total != 0 {
		t.Errorf("no match = %v (%d)", got, total)
	}
	all := []string{`D:\data\team\a.txt`, `D:\data\team\docs\b.txt`, `D:\data\team\docs\deep\er\c.txt`, `D:\data\team\Needle Folder\inside.txt`}
	if got, total := searchPaths(t, ix, root, ".txt", 0, 50); total != 4 || !reflect.DeepEqual(got, []string{all[0], all[1], all[2], all[3]}) {
		t.Errorf(".txt = %v (%d), want the files by name", got, total)
	}
	if got, total := searchPaths(t, ix, root, ".txt", 1, 2); total != 4 || !reflect.DeepEqual(got, all[1:3]) {
		t.Errorf("page 2 = %v (%d)", got, total)
	}
	if got, total := searchPaths(t, ix, root, ".txt", 9, 2); total != 4 || len(got) != 0 {
		t.Errorf("a page past the end = %v (%d)", got, total)
	}
	// Folders come before files, then the names.
	got, _ = searchPaths(t, ix, root, "e", 0, 50)
	if len(got) < 3 || !strings.HasSuffix(got[0], `\deep`) || !strings.HasSuffix(got[1], `\er`) || !strings.HasSuffix(got[2], `Needle Folder`) {
		t.Errorf("folders first: %v", got)
	}
	// A search in a folder stays in it.
	if got, _ := searchPaths(t, ix, `D:\data\team\docs`, "txt", 0, 50); !reflect.DeepEqual(got, all[1:3]) {
		t.Errorf("search in docs = %v", got)
	}
	page := ix.search(find(t, ix, root), []string{"report"}, 0, 5)
	if e := page.Items[0]; e.IsDir || e.Size != 30 || e.Name != "Report 2024.docx" || e.MTime == 0 {
		t.Errorf("the entry = %+v", e)
	}
	if e := ix.search(find(t, ix, root), []string{"docs"}, 0, 5).Items[0]; !e.IsDir || e.Size != -1 {
		t.Errorf("the entry of a folder = %+v", e)
	}
}

func TestFoldersAddUpTheFilesBelowThem(t *testing.T) {
	m := newModel()
	m.tree()
	ix, _ := m.built(`D:\data\team`)
	team := find(t, ix, `D:\data\team`)
	folders, more := ix.subfolders(team, 0)
	if more || len(folders) != 2 {
		t.Fatalf("folders = %+v", folders)
	}
	want := []Folder{{Name: "docs", Size: 90}, {Name: "Needle Folder", Size: 5}}
	for i, w := range want {
		if folders[i].Name != w.Name || folders[i].Size != w.Size || folders[i].MTime == 0 {
			t.Errorf("folder %d = %+v, want %+v", i, folders[i], w)
		}
	}
	if f, more := ix.subfolders(team, 1); !more || len(f) != 1 {
		t.Errorf("limit 1 = %v, more %v", f, more)
	}
	docs, _ := ix.subfolders(find(t, ix, `D:\data\team\docs`), 0)
	if len(docs) != 1 || docs[0].Name != "deep" || docs[0].Size != 40 {
		t.Errorf("below docs = %+v", docs)
	}
}

func TestProbeCountsAndAnswersFromTheJournal(t *testing.T) {
	m := newModel()
	m.tree()
	team := `D:\data\team`
	ix, roots := m.built(team, `D:\data\team\docs`)
	slot := find(t, ix, team)
	built := m.now()

	res := ix.probe(slot, 0, time.Time{})
	if res.Items != 10 || res.Changed {
		t.Errorf("the count alone = %+v", res)
	}
	// What was before the index started to watch cannot be known.
	if res := ix.probe(slot, 0, built.Add(-time.Hour)); !res.Changed {
		t.Error("a time before the index started to watch must count as a change")
	}
	m.mu.Lock()
	m.tick()
	m.mu.Unlock()
	quiet := m.now()
	if res := ix.probe(slot, 0, quiet); res.Changed {
		t.Errorf("nothing happened since the index started: %+v", res)
	}

	step := func(change func()) time.Time {
		m.mu.Lock()
		m.tick()
		m.mu.Unlock()
		before := m.now()
		change()
		if err := ix.sync(m); err != nil {
			t.Fatal(err)
		}
		return before
	}
	// A change next to the folders is no change of them.
	before := step(func() { m.write(`D:\data\other\a.txt`, 1) })
	if ix.probe(slot, 0, before).Changed {
		t.Error("a change outside the registered folder counts")
	}
	// A change deep inside counts for the folders that contain it, not for the one that does not.
	before = step(func() { m.write(`D:\data\team\docs\deep\er\c.txt`, 41) })
	if !ix.probe(slot, 0, before).Changed || !ix.probe(find(t, ix, `D:\data\team\docs`), 1, before).Changed {
		t.Error("a change in docs is a change of team and of docs")
	}
	before = step(func() { m.write(`D:\data\team\a.txt`, 11) })
	if !ix.probe(slot, 0, before).Changed || ix.probe(find(t, ix, `D:\data\team\docs`), 1, before).Changed {
		t.Error("a change next to docs is a change of team but not of docs")
	}
	if roots[0].lastChange == 0 || roots[1].lastChange >= roots[0].lastChange {
		t.Errorf("last changes: team %d, docs %d", roots[0].lastChange, roots[1].lastChange)
	}
	// A folder inside a registered one has the changes of that one: it may say changed too often, never too seldom.
	if !ix.probe(find(t, ix, `D:\data\team\Needle Folder`), 0, before).Changed {
		t.Error("a folder inside team must report the changes of team")
	}
	// A deletion shows in the count and as a change.
	before = step(func() { m.remove(`D:\data\team\Needle Folder`) })
	if res := ix.probe(slot, 0, before); !res.Changed || res.Items != 8 {
		t.Errorf("after the deletion of a folder with a file: %+v, want 8 items", res)
	}
	// A time after every change is quiet again.
	step(func() {})
	if res := ix.probe(slot, 0, m.now()); res.Changed {
		t.Errorf("quiet again: %+v", res)
	}
}

func TestJournalChangesAreApplied(t *testing.T) {
	m := newModel()
	m.tree()
	ix, _ := m.built(`D:\data\team`)
	team := `D:\data\team`
	sync := func() {
		t.Helper()
		if err := ix.sync(m); err != nil {
			t.Fatal(err)
		}
		ix.refresh(m, 1000, time.Now().Add(time.Minute))
	}
	paths := func(text string) []string { p, _ := searchPaths(t, ix, team, text, 0, 50); return p }

	// A new file in a new folder is found, with its size once that has been read.
	m.create(`D:\data\team\new\n1.txt`, 12)
	sync()
	if got := paths("n1"); !reflect.DeepEqual(got, []string{`D:\data\team\new\n1.txt`}) {
		t.Errorf("a new file: %v", got)
	}
	if page := ix.search(find(t, ix, team), []string{"n1"}, 0, 5); page.Items[0].Size != 12 {
		t.Errorf("size of the new file = %d", page.Items[0].Size)
	}
	if ix.stats() != 12 {
		t.Errorf("entries = %d, want 12", ix.stats())
	}

	// A file that is written gets its new size.
	m.write(`D:\data\team\new\n1.txt`, 99)
	sync()
	if f, _ := ix.subfolders(find(t, ix, team), 0); f[len(f)-1].Name != "new" || f[len(f)-1].Size != 99 {
		t.Errorf("folder sizes after a write: %+v", f)
	}

	// A file moved to another folder inside; a folder renamed with what is in it.
	m.rename(`D:\data\team\a.txt`, `D:\data\team\docs\a2.txt`)
	m.rename(`D:\data\team\docs\deep`, `D:\data\team\deeper`)
	sync()
	if got := paths("a2"); !reflect.DeepEqual(got, []string{`D:\data\team\docs\a2.txt`}) {
		t.Errorf("a moved file: %v", got)
	}
	if got := paths("c.txt"); !reflect.DeepEqual(got, []string{`D:\data\team\deeper\er\c.txt`}) {
		t.Errorf("a renamed folder: %v", got)
	}
	if got := paths("a.txt"); len(got) != 0 {
		t.Errorf("the old name is still there: %v", got)
	}
	if ix.rebuild {
		t.Error("a rename inside the registered folder needs no rebuild")
	}

	// A file that leaves the registered folder is gone, and comes back.
	m.rename(`D:\data\team\docs\b.txt`, `D:\data\other\b.txt`)
	sync()
	if got := paths("b.txt"); len(got) != 0 {
		t.Errorf("a file moved out: %v", got)
	}
	m.rename(`D:\data\other\b.txt`, `D:\data\team\b-back.txt`)
	sync()
	if got := paths("b-back"); len(got) != 1 {
		t.Errorf("a file moved back in: %v", got)
	}

	// A folder with everything in it is deleted.
	m.remove(`D:\data\team\deeper`)
	sync()
	if got := paths("c.txt"); len(got) != 0 {
		t.Errorf("deleted with its folder: %v", got)
	}
	if find(t, ix, `D:\data\team\deeper`) != noNode || find(t, ix, `D:\data\team\deeper\er`) != noNode {
		t.Error("a deleted folder can still be resolved")
	}
	// The slots of what was deleted are used again.
	free := len(ix.free)
	m.create(`D:\data\team\again.txt`, 1)
	sync()
	if len(ix.free) >= free {
		t.Errorf("free slots %d -> %d: nothing was reused", free, len(ix.free))
	}

	// A folder that comes from outside brings files that nobody listed: the index has to be built again.
	m.rename(`D:\data\other`, `D:\data\team\other`)
	sync()
	if !ix.rebuild {
		t.Error("a folder moved in must ask for a rebuild")
	}
}

func TestTheRegisteredFolderIsNotRemovedByItsOwnChanges(t *testing.T) {
	m := newModel()
	m.tree()
	ix, roots := m.built(`D:\data\team`)
	m.mu.Lock()
	team := m.files[ix.nodes[roots[0].slot].frn]
	rec := usnRecord{FRN: ref(team.frn), Parent: ref(team.parent), USN: 5000, Time: m.tick(), Reason: 0x8000 /* basic info change */ | reasonClose, Attrs: attrDirectory, Name: "team"}
	m.mu.Unlock()
	ix.apply(rec)
	if roots[0].slot == noNode || ix.stats() != 10 {
		t.Fatalf("the registered folder was removed: slot %d, %d entries", roots[0].slot, ix.stats())
	}
	if roots[0].lastChange != rec.Time {
		t.Errorf("the change of the folder itself is not recorded: %d", roots[0].lastChange)
	}
}

func TestApplyingTheJournalAgainChangesNothing(t *testing.T) {
	m := newModel()
	m.tree()
	m.create(`D:\data\team\gone.txt`, 3)
	m.remove(`D:\data\team\gone.txt`)
	m.rename(`D:\data\team\a.txt`, `D:\data\team\moved.txt`)
	ix, _ := m.built(`D:\data\team`)
	before := ix.stats()
	// Every record since the beginning, as if the journal had been read from before the listing.
	for _, r := range m.recs {
		ix.apply(r)
	}
	if got := ix.stats(); got != before {
		t.Errorf("the entries went from %d to %d", before, got)
	}
	if got, _ := searchPaths(t, ix, `D:\data\team`, "gone", 0, 5); len(got) != 0 {
		t.Errorf("a deleted file came back: %v", got)
	}
	if got, _ := searchPaths(t, ix, `D:\data\team`, "moved", 0, 5); len(got) != 1 {
		t.Errorf("the renamed file = %v", got)
	}
	if ix.rebuild {
		t.Error("replaying what the listing showed must not ask for a rebuild")
	}
}

func TestSyncReportsALostJournal(t *testing.T) {
	m := newModel()
	m.tree()
	ix, _ := m.built(`D:\data\team`)
	m.write(`D:\data\team\a.txt`, 5)
	m.wrapJournal()
	if err := ix.sync(m); err != ErrJournalLost {
		t.Errorf("sync after the journal wrapped = %v, want ErrJournalLost", err)
	}
	m.mu.Lock()
	m.id++
	m.mu.Unlock()
	ix2, _ := m.built(`D:\data\team`)
	m.mu.Lock()
	m.id++
	m.mu.Unlock()
	if err := ix2.sync(m); err != ErrJournalLost {
		t.Errorf("sync after the journal was replaced = %v, want ErrJournalLost", err)
	}
}

func TestBuildFailsWhenTheVolumeCannotBeListed(t *testing.T) {
	m := newModel()
	m.tree()
	m.listErr = errBoom
	r, _ := newRootInfo(`D:\data\team`)
	if _, err := build(m, []*rootInfo{r}, m.now); err != errBoom {
		t.Errorf("build = %v, want the error of the listing", err)
	}
}

func TestAFolderThatIsNotThereIsRegisteredButEmpty(t *testing.T) {
	m := newModel()
	m.tree()
	ix, roots := m.built(`D:\data\team`, `D:\data\missing`)
	if roots[1].slot != noNode || find(t, ix, `D:\data\missing`) != noNode {
		t.Error("a folder that is not there has a node")
	}
	if ix.stats() != 10 {
		t.Errorf("entries = %d", ix.stats())
	}
}

func TestSmallestIsTheStartOfTheSortedList(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 5, 100, 5000} {
		items := make([]int32, n)
		for i := range items {
			items[i] = int32(rng.Intn(1000))
		}
		less := func(a, b int32) int { return int(a) - int(b) }
		for _, k := range []int{1, 3, n / 2, n} {
			if k < 1 || k > n {
				continue
			}
			sorted := slices.Clone(items)
			slices.SortFunc(sorted, less)
			got := smallest(slices.Clone(items), k, less)
			if !slices.Equal(got, sorted[:k]) {
				t.Errorf("n=%d k=%d: %v, want %v", n, k, got, sorted[:k])
			}
		}
	}
}

func TestSearchOfALargeTreeUsesThePartialSort(t *testing.T) {
	ix := newIndex(nil)
	root := ix.alloc()
	ix.init(root, 1, "root", attrDirectory, 0, 0)
	const files = 60_000
	for i := 0; i < files; i++ {
		s := ix.alloc()
		ix.init(s, uint64(10+i), "file-"+strings.Repeat("0", 5-len(itoa(i)))+itoa(i)+".dat", 0x20, int64(i), 1)
		ix.link(s, root)
	}
	page := ix.search(root, []string{"file-"}, 100, 3)
	if page.Total != files || len(page.Items) != 3 || page.Items[0].Name != "file-00100.dat" || page.Items[2].Name != "file-00102.dat" {
		t.Errorf("page = %d items, total %d, first %q", len(page.Items), page.Total, page.Items[0].Name)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
