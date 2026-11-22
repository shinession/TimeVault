package ntfsindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// These tests read a real NTFS volume, which needs administrator rights; without them they are skipped.
func skipUnlessElevated(t *testing.T) {
	t.Helper()
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("reading a volume needs administrator rights")
	}
}

func writeFile(t *testing.T, path string, size int, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshot is what a walk of the folder finds, by path relative to it in lower case.
type snapshotEntry struct {
	dir   bool
	size  int64
	mtime int64
}

func walkSnapshot(t *testing.T, root string) map[string]snapshotEntry {
	t.Helper()
	out := map[string]snapshotEntry{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel := strings.ToLower(strings.TrimPrefix(p, root+`\`))
		e := snapshotEntry{dir: d.IsDir(), mtime: filetime(info.ModTime())}
		if !d.IsDir() {
			e.size = info.Size()
		}
		out[rel] = e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// indexSnapshot is what the index holds below the registered folder, in the same form.
func indexSnapshot(ix *index, rootSlot int32, root string) map[string]snapshotEntry {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := map[string]snapshotEntry{}
	stack := []int32{}
	for c := ix.nodes[rootSlot].firstChild; c != noNode; c = ix.nodes[c].next {
		stack = append(stack, c)
	}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &ix.nodes[s]
		e := snapshotEntry{dir: n.isDir(), mtime: n.mtime}
		if !n.isDir() {
			e.size = n.size
		}
		out[strings.ToLower(strings.TrimPrefix(ix.path(s), root+`\`))] = e
		for c := n.firstChild; c != noNode; c = ix.nodes[c].next {
			stack = append(stack, c)
		}
	}
	return out
}

func sameSnapshots(t *testing.T, what string, got, want map[string]snapshotEntry) {
	t.Helper()
	for k, w := range want {
		g, ok := got[k]
		switch {
		case !ok:
			t.Errorf("%s: the index lacks %q", what, k)
		case g.dir != w.dir:
			t.Errorf("%s: %q is a folder: %v, want %v", what, k, g.dir, w.dir)
		default:
			if !w.dir && g.size != w.size {
				t.Errorf("%s: %q has size %d, want %d", what, k, g.size, w.size)
			}
			if !w.dir && g.mtime != w.mtime {
				t.Errorf("%s: %q has time %d, want %d", what, k, g.mtime, w.mtime)
			}
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: the index has %q, which is not there", what, k)
		}
	}
}

func TestWindowsVolumeMatchesAWalkAndFollowsTheJournal(t *testing.T) {
	skipUnlessElevated(t)
	root := t.TempDir()
	drive := filepath.VolumeName(root)
	fixed := time.Date(2024, 3, 5, 6, 7, 8, 0, time.UTC)

	// Small files live in the file record, big ones in clusters; both have to show their length.
	writeFile(t, filepath.Join(root, "a.txt"), 10, fixed)
	writeFile(t, filepath.Join(root, "docs", "b.txt"), 2000, fixed.Add(time.Hour))
	writeFile(t, filepath.Join(root, "docs", "Report 2024.docx"), 70_000, fixed.Add(2*time.Hour))
	writeFile(t, filepath.Join(root, "docs", "deep", "er", "c.txt"), 0, fixed.Add(3*time.Hour))
	writeFile(t, filepath.Join(root, "Ünï cödé 文档 😀.txt"), 5, fixed)
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	vol, err := openVolume(drive)
	if err != nil {
		t.Fatal(err)
	}
	defer vol.Close()
	r, ok := newRootInfo(root)
	if !ok {
		t.Fatalf("bad root %q", root)
	}
	started := time.Now()
	ix, err := build(vol, []*rootInfo{r}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("built the index of %s: %d entries, %v (the two listings read all of %s)", root, ix.stats(), time.Since(started).Round(time.Millisecond), drive)
	if r.slot == noNode {
		t.Fatal("the registered folder was not found in the listing")
	}
	sameSnapshots(t, "after the build", indexSnapshot(ix, r.slot, root), walkSnapshot(t, root))

	// The journal: create, write, rename, delete.
	settle := func(check func() bool, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if err := ix.sync(vol); err != nil {
				t.Fatal(err)
			}
			ix.refresh(vol, 1000, time.Now().Add(10*time.Second))
			if check() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not show in the index", what)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	matches := func(text string) int { return ix.search(r.slot, []string{text}, 0, 10).Total }
	beforeChange := time.Now()

	writeFile(t, filepath.Join(root, "docs", "new", "n1.txt"), 33, time.Time{})
	settle(func() bool {
		p := ix.search(r.slot, []string{"n1"}, 0, 5)
		return p.Total == 1 && p.Items[0].Size == 33
	}, "a new file with its size")

	f, err := os.OpenFile(filepath.Join(root, "a.txt"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(strings.Repeat("y", 90))
	f.Close()
	settle(func() bool { return ix.search(r.slot, []string{"a.txt"}, 0, 5).Items[0].Size == 100 }, "the size of a file that grew")

	if err := os.Rename(filepath.Join(root, "docs", "deep"), filepath.Join(root, "deeper")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "docs", "b.txt"), filepath.Join(root, "b-renamed.txt")); err != nil {
		t.Fatal(err)
	}
	settle(func() bool { return matches("c.txt") == 1 && matches("b-renamed") == 1 && matches("b.txt") == 0 }, "renames")
	if got := ix.search(r.slot, []string{"c.txt"}, 0, 5).Items[0].Path; !strings.EqualFold(got, filepath.Join(root, "deeper", "er", "c.txt")) {
		t.Errorf("the path of a file in a renamed folder = %q", got)
	}

	if err := os.RemoveAll(filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	settle(func() bool { return matches("report") == 0 && matches("n1") == 0 }, "a deleted folder")
	if ix.rebuild {
		t.Error("none of this needs a rebuild")
	}
	sameSnapshots(t, "after the changes", indexSnapshot(ix, r.slot, root), walkSnapshot(t, root))

	// The probe: what changed since a time before all of it, and what has not changed since now.
	if !ix.probe(r.slot, 0, beforeChange.Add(-time.Second)).Changed {
		t.Error("a probe from before the changes must say changed")
	}
	if ix.probe(r.slot, 0, beforeChange).Changed != true {
		t.Error("the changes happened after beforeChange")
	}
	time.Sleep(1500 * time.Millisecond)
	quiet := time.Now()
	time.Sleep(200 * time.Millisecond)
	if err := ix.sync(vol); err != nil {
		t.Fatal(err)
	}
	if res := ix.probe(r.slot, 0, quiet); res.Changed {
		t.Errorf("nothing was changed since %v: %+v", quiet, res)
	}
	writeFile(t, filepath.Join(root, "late.txt"), 1, time.Time{})
	settle(func() bool { return ix.probe(r.slot, 0, quiet).Changed }, "a change after the quiet time")
}
