package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/catalog"
	"timevault/internal/db/dbtest"
	"timevault/internal/pathutil"
	"timevault/internal/repository"
)

type recorder struct {
	mu     sync.Mutex
	events []string
	phases []string
}

func (r *recorder) Phase(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases = append(r.phases, name)
}
func (r *recorder) Progress(Progress) {}
func (r *recorder) Event(level, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, level+": "+msg)
}
func (r *recorder) has(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

type testEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	cat    *catalog.Store
	repo   *repository.Store
	eng    *Engine
	srcDir string
	src    catalog.Source
	clock  int
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	pool := dbtest.New(t)
	repo, err := repository.Open(filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewStore(pool)
	eng := New(Config{Catalog: cat, Repository: repo, HashWorkers: 4, Protected: []string{repo.Root()}})
	srcDir := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := &testEnv{t: t, pool: pool, cat: cat, repo: repo, eng: eng, srcDir: srcDir}
	req, err := eng.PrepareSource(context.Background(), SourceRequest{Name: "test source", RootPath: srcDir, Excludes: []string{"*.tmp", "skipme/"}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if env.src, err = cat.CreateSource(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	return env
}

// write creates or replaces a file with a distinct, increasing modification time
// so that change detection never depends on the clock resolution.
func (e *testEnv) write(rel, content string) {
	e.t.Helper()
	p := filepath.Join(e.srcDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.clock++
	mt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(e.clock) * time.Minute)
	if err := os.Chtimes(p, mt, mt); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) mkdir(rel string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Join(e.srcDir, filepath.FromSlash(rel)), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) remove(rel string) {
	e.t.Helper()
	if err := os.RemoveAll(filepath.Join(e.srcDir, filepath.FromSlash(rel))); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) backup(opts ...RunOptions) catalog.Point {
	e.t.Helper()
	var o RunOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	p, err := e.eng.Run(context.Background(), e.src.ID, o, &recorder{})
	if err != nil {
		e.t.Fatalf("backup failed: %v", err)
	}
	return p
}

// names lists the names directly below rel at a point, in catalog order.
func (e *testEnv) names(seq int, rel string) []string {
	e.t.Helper()
	entry, err := e.cat.ResolveAt(context.Background(), e.src.ID, seq, rel)
	if err != nil {
		e.t.Fatalf("resolve %q at %d: %v", rel, seq, err)
	}
	var out []string
	var after *catalog.Cursor
	for {
		page, err := e.cat.ListChildren(context.Background(), catalog.ListParams{Parent: entry.NodeID, Seq: seq, After: after, Limit: 7})
		if err != nil {
			e.t.Fatal(err)
		}
		for _, en := range page.Entries {
			n := en.Name
			if en.IsDir {
				n += "/"
			}
			out = append(out, n)
		}
		if page.Next == nil {
			return out
		}
		after = page.Next
	}
}

func (e *testEnv) entry(seq int, rel string) catalog.Entry {
	e.t.Helper()
	en, err := e.cat.ResolveAt(context.Background(), e.src.ID, seq, rel)
	if err != nil {
		e.t.Fatalf("resolve %q at %d: %v", rel, seq, err)
	}
	return en
}

func (e *testEnv) content(en catalog.Entry) string {
	e.t.Helper()
	h, err := repository.HashFromBytes(en.SHA256)
	if err != nil {
		e.t.Fatalf("entry %s has no hash: %v", en.Name, err)
	}
	f, err := e.repo.Open(h)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func (e *testEnv) assertManifest(p catalog.Point) {
	e.t.Helper()
	sum, err := e.cat.ComputeManifest(context.Background(), p.SourceID, p.Seq)
	if err != nil {
		e.t.Fatal(err)
	}
	if string(sum) != string(p.ManifestSHA256) {
		e.t.Fatalf("point #%d: manifest recomputed from the catalog differs from the one recorded at commit", p.Seq)
	}
}

func (e *testEnv) versionCount() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM node_versions`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func seedTree(e *testEnv) {
	e.write("a.txt", "alpha")
	e.write("docs/b.txt", "bravo")
	e.write("docs/deep/c.txt", "charlie")
	e.write("dup1.bin", "same bytes")
	e.write("dup2.bin", "same bytes")
	e.mkdir("empty")
	e.write("ignored.tmp", "excluded by pattern")
	e.write("skipme/x.txt", "excluded by path pattern")
}

func TestFirstBackupIsAFullCommittedPoint(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	rec := &recorder{}
	p, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if p.Seq != 1 || p.Kind != "full" || p.State != catalog.StateCommitted || p.Consistency != catalog.ConsistencyLiveScan {
		t.Fatalf("point = %+v", p)
	}
	if !p.Restorable() || p.Verification != catalog.VerificationValidated {
		t.Fatalf("point should be restorable and validated: %+v", p)
	}
	if p.FileCount != 5 || p.DirCount != 3 || p.AddedCount != 8 || p.ModifiedCount != 0 || p.DeletedCount != 0 {
		t.Fatalf("stats = %+v", p)
	}
	if p.NewObjectCount != 4 { // alpha, bravo, charlie, "same bytes" once
		t.Fatalf("new objects = %d, want 4 (duplicates must be stored once)", p.NewObjectCount)
	}
	wantBytes := int64(len("alpha") + len("bravo") + len("charlie") + 2*len("same bytes"))
	if p.TotalBytes != wantBytes {
		t.Fatalf("total bytes = %d, want %d", p.TotalBytes, wantBytes)
	}
	eq(t, "root", e.names(1, ""), []string{"a.txt", "docs/", "dup1.bin", "dup2.bin", "empty/"})
	eq(t, "docs", e.names(1, "docs"), []string{"b.txt", "deep/"})
	if got := e.content(e.entry(1, "docs/deep/c.txt")); got != "charlie" {
		t.Fatalf("stored content = %q", got)
	}
	e.assertManifest(p)
	if want := []string{PhasePreparing, PhaseScanning, PhaseValidating, PhaseCommitting, PhaseCommitted}; fmt.Sprint(rec.phases) != fmt.Sprint(want) {
		t.Fatalf("phases = %v, want %v", rec.phases, want)
	}
}

func TestBackupWithoutChangesWritesNothingNew(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	first := e.backup()
	versions := e.versionCount()
	second := e.backup()
	if second.Seq != 2 || second.Kind != "incremental" {
		t.Fatalf("second point = %+v", second)
	}
	if second.AddedCount+second.ModifiedCount+second.DeletedCount != 0 || second.NewObjectCount != 0 {
		t.Fatalf("an unchanged tree must record no changes: %+v", second)
	}
	if got := e.versionCount(); got != versions {
		t.Fatalf("version rows grew from %d to %d without any change", versions, got)
	}
	if string(first.ManifestSHA256) != string(second.ManifestSHA256) {
		t.Fatal("identical trees must have identical manifests")
	}
	e.assertManifest(second)
}

func TestIncrementalBackupKeepsHistoryAndTracksChanges(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	first := e.backup()

	e.write("a.txt", "alpha v2")
	e.remove("docs/b.txt")
	e.write("new.txt", "brand new")
	e.remove("docs/deep")
	e.write("docs/deeper/c.txt", "charlie") // "rename": identical content under a new folder name
	second := e.backup()

	if second.ModifiedCount != 1 {
		t.Fatalf("modified = %d, want 1 (a.txt)", second.ModifiedCount)
	}
	if second.AddedCount != 3 {
		t.Fatalf("added = %d, want 3 (new.txt, docs/deeper, docs/deeper/c.txt)", second.AddedCount)
	}
	if second.DeletedCount != 3 {
		t.Fatalf("deleted = %d, want 3 (docs/b.txt, docs/deep, docs/deep/c.txt)", second.DeletedCount)
	}
	if second.NewObjectCount != 2 { // "alpha v2" and "brand new"; "charlie" already stored
		t.Fatalf("new objects = %d, want 2", second.NewObjectCount)
	}

	eq(t, "root @1", e.names(1, ""), []string{"a.txt", "docs/", "dup1.bin", "dup2.bin", "empty/"})
	eq(t, "root @2", e.names(2, ""), []string{"a.txt", "docs/", "dup1.bin", "dup2.bin", "empty/", "new.txt"})
	eq(t, "docs @1", e.names(1, "docs"), []string{"b.txt", "deep/"})
	eq(t, "docs @2", e.names(2, "docs"), []string{"deeper/"})
	if got := e.content(e.entry(1, "a.txt")); got != "alpha" {
		t.Fatalf("a.txt @1 = %q", got)
	}
	if got := e.content(e.entry(2, "a.txt")); got != "alpha v2" {
		t.Fatalf("a.txt @2 = %q", got)
	}
	if got := e.content(e.entry(1, "docs/b.txt")); got != "bravo" {
		t.Fatalf("deleted file must stay restorable at the old point, got %q", got)
	}
	if _, err := e.cat.ResolveAt(context.Background(), e.src.ID, 2, "docs/b.txt"); err == nil {
		t.Fatal("docs/b.txt must not exist at point 2")
	}
	if _, err := e.cat.ResolveAt(context.Background(), e.src.ID, 1, "new.txt"); err == nil {
		t.Fatal("new.txt must not exist at point 1")
	}
	// Names resolve case-insensitively, as on Windows.
	if got := e.entry(1, "DOCS/B.TXT"); got.Name != "b.txt" {
		t.Fatalf("case-insensitive resolve returned %q", got.Name)
	}

	e.assertManifest(first)
	e.assertManifest(second)
	// The first point is unchanged by the later backup.
	again, err := e.cat.GetPoint(context.Background(), first.ID)
	if err != nil || string(again.ManifestSHA256) != string(first.ManifestSHA256) {
		t.Fatalf("point 1 changed after point 2: %v", err)
	}
}

func TestVersionsAndStatusAgainstLatest(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "one")
	e.write("gone.txt", "soon deleted")
	e.write("same.txt", "constant")
	e.backup()
	e.write("a.txt", "two")
	e.remove("gone.txt")
	e.backup()
	e.write("a.txt", "three")
	third := e.backup()

	node, err := e.cat.ResolveNode(context.Background(), e.src.ID, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := e.cat.Versions(context.Background(), node, third.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("a.txt has %d versions, want 3", len(versions))
	}
	for i, wantFirst := range []int{3, 2, 1} {
		if versions[i].FirstPoint != wantFirst {
			t.Fatalf("version %d starts at point %d, want %d", i, versions[i].FirstPoint, wantFirst)
		}
	}
	if !versions[0].StillCurrent || versions[1].StillCurrent || versions[2].StillCurrent {
		t.Fatalf("only the newest version may be current: %+v", versions)
	}
	if versions[2].LastAt == nil {
		t.Fatal("a closed version must report when it ended")
	}

	// Browsing point 1 shows how each item relates to the present.
	root := e.entry(1, "")
	page, err := e.cat.ListChildren(context.Background(), catalog.ListParams{Parent: root.NodeID, Seq: 1, LatestSeq: third.Seq, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]catalog.NowStatus{}
	for _, en := range page.Entries {
		got[en.Name] = en.Now
	}
	want := map[string]catalog.NowStatus{"a.txt": catalog.NowModified, "gone.txt": catalog.NowDeleted, "same.txt": catalog.NowSame}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("status vs latest = %v, want %v", got, want)
	}
}

func TestFolderReplacedByFileAndBack(t *testing.T) {
	e := newEnv(t)
	e.write("thing/inside.txt", "inside")
	e.backup()

	e.remove("thing")
	e.write("thing", "now a file")
	second := e.backup()
	if second.ModifiedCount != 1 || second.DeletedCount != 1 {
		t.Fatalf("folder->file stats: %+v", second)
	}
	if en := e.entry(2, "thing"); en.IsDir || e.content(en) != "now a file" {
		t.Fatalf("thing at point 2 = %+v", en)
	}
	if en := e.entry(1, "thing"); !en.IsDir {
		t.Fatal("thing at point 1 must still be a folder")
	}
	eq(t, "thing @1", e.names(1, "thing"), []string{"inside.txt"})

	e.remove("thing")
	e.write("thing/other.txt", "other")
	e.backup()
	eq(t, "thing @3", e.names(3, "thing"), []string{"other.txt"})
	eq(t, "thing @1", e.names(1, "thing"), []string{"inside.txt"})
}

func TestDeletedFolderIsRestorableFromOlderPoint(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	e.backup()
	e.remove("docs")
	second := e.backup()
	if second.DeletedCount != 4 { // docs, b.txt, deep, c.txt
		t.Fatalf("deleted = %d, want 4", second.DeletedCount)
	}
	eq(t, "root @2", e.names(2, ""), []string{"a.txt", "dup1.bin", "dup2.bin", "empty/"})
	if got := e.content(e.entry(1, "docs/deep/c.txt")); got != "charlie" {
		t.Fatalf("content of a deleted tree = %q", got)
	}
	// Bringing the folder back creates a fresh history for the same path.
	e.write("docs/b.txt", "bravo returns")
	e.backup()
	eq(t, "docs @3", e.names(3, "docs"), []string{"b.txt"})
	if _, err := e.cat.ResolveAt(context.Background(), e.src.ID, 2, "docs"); err == nil {
		t.Fatal("docs must not exist at point 2")
	}
}

func TestRehashFindsNothingNewInUnchangedFiles(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	e.backup()
	versions := e.versionCount()
	p := e.backup(RunOptions{Rehash: true})
	if p.NewObjectCount != 0 || p.ModifiedCount != 0 || e.versionCount() != versions {
		t.Fatalf("rehash of unchanged data recorded changes: %+v", p)
	}
}

func TestExcludePatternsAreHonoured(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	e.backup()
	eq(t, "root @1 has no excluded items", sorted(e.names(1, "")), []string{"a.txt", "docs/", "dup1.bin", "dup2.bin", "empty/"})
}

func TestChangedFileIsRetriedUntilItIsStable(t *testing.T) {
	e := newEnv(t)
	e.write("busy.txt", "start")
	changes := 0
	e.eng.hook = func(event, rel string) {
		if event == "opened" && rel == "busy.txt" && changes == 0 {
			changes++
			p := filepath.Join(e.srcDir, "busy.txt")
			_ = os.WriteFile(p, []byte("start plus more bytes"), 0o644)
			future := time.Now().Add(time.Hour)
			_ = os.Chtimes(p, future, future)
		}
	}
	p := e.backup()
	if changes != 1 {
		t.Fatalf("hook fired %d times", changes)
	}
	if got := e.content(e.entry(p.Seq, "busy.txt")); got != "start plus more bytes" {
		t.Fatalf("stored %q; a file that changed while being read must be re-read", got)
	}
	if p.SkippedCount != 0 {
		t.Fatalf("skipped = %d", p.SkippedCount)
	}
	e.assertManifest(p)
}

func TestFileThatKeepsChangingIsSkippedNotStoredTorn(t *testing.T) {
	e := newEnv(t)
	e.write("busy.txt", "start")
	e.write("calm.txt", "calm")
	round := 0
	e.eng.hook = func(event, rel string) {
		if event == "opened" && rel == "busy.txt" {
			round++
			p := filepath.Join(e.srcDir, "busy.txt")
			_ = os.WriteFile(p, []byte(strings.Repeat("x", round*10)), 0o644)
			future := time.Now().Add(time.Duration(round) * time.Hour)
			_ = os.Chtimes(p, future, future)
		}
	}
	rec := &recorder{}
	p, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if p.SkippedCount != 1 || !rec.has("changed while it was being backed up") {
		t.Fatalf("skipped = %d events = %v", p.SkippedCount, rec.events)
	}
	busy := e.entry(p.Seq, "busy.txt")
	if busy.State != catalog.VersionSkipped || busy.SHA256 != nil || !strings.Contains(busy.SkipReason, "changed") {
		t.Fatalf("busy.txt entry = %+v", busy)
	}
	if got := e.content(e.entry(p.Seq, "calm.txt")); got != "calm" {
		t.Fatalf("other files must still be captured, got %q", got)
	}
	e.assertManifest(p)

	// Once the file is quiet again the next backup captures it.
	e.eng.hook = nil
	next := e.backup()
	if en := e.entry(next.Seq, "busy.txt"); en.State != catalog.VersionOK || en.SHA256 == nil {
		t.Fatalf("busy.txt after it settled = %+v", en)
	}
}

func TestUnreadableFileKeepsItsPreviousVersion(t *testing.T) {
	e := newEnv(t)
	e.write("report.txt", "version one")
	e.backup()
	e.write("report.txt", "version two, but unreadable now")
	e.eng.hook = func(event, rel string) {
		if event == "opening" && rel == "report.txt" {
			// Replace the file with a folder just before it is opened, so reading it fails.
			p := filepath.Join(e.srcDir, "report.txt")
			_ = os.Remove(p)
			_ = os.Mkdir(p, 0o755)
		}
	}
	rec := &recorder{}
	p, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if p.SkippedCount != 1 {
		t.Fatalf("skipped = %d, events %v", p.SkippedCount, rec.events)
	}
	if !rec.has("the previous version is kept") {
		t.Fatalf("events = %v", rec.events)
	}
	if got := e.content(e.entry(p.Seq, "report.txt")); got != "version one" {
		t.Fatalf("report.txt at the new point = %q, want the previous version", got)
	}
	e.assertManifest(p)
}

func TestUnlistableFolderIsNotTreatedAsDeleted(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	first := e.backup()
	e.eng.failReadDir = func(rel string) error {
		if rel == "docs" {
			return os.ErrPermission
		}
		return nil
	}
	second := e.backup()
	if second.DeletedCount != 0 || second.SkippedCount != 1 {
		t.Fatalf("an unreadable folder must be kept, not deleted: %+v", second)
	}
	eq(t, "docs @2", e.names(2, "docs"), []string{"b.txt", "deep/"})
	if string(first.ManifestSHA256) != string(second.ManifestSHA256) {
		t.Fatal("the carried-over state must produce the same manifest")
	}
	e.assertManifest(second)
}

func TestCancelledBackupLeavesNoTraceAndRetrySucceeds(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	first := e.backup()

	e.write("a.txt", "alpha changed")
	e.remove("docs/b.txt")
	e.write("added.txt", "added later")
	e.write("docs/deep/c.txt", "charlie changed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The root folder is already recorded when the deeper file is opened, so the
	// cancellation leaves real partial work behind that has to be removed.
	e.eng.hook = func(event, rel string) {
		if event == "opened" && rel == "docs/deep/c.txt" {
			cancel()
		}
	}
	if _, err := e.eng.Run(ctx, e.src.ID, RunOptions{}, nil); err == nil {
		t.Fatal("cancelled backup must fail")
	}
	e.eng.hook = nil

	attempts, err := e.cat.ListAttempts(context.Background(), e.src.ID, 10)
	if err != nil || len(attempts) != 2 || attempts[0].State != catalog.StateAborted {
		t.Fatalf("attempts = %+v, %v", attempts, err)
	}
	latest, err := e.cat.LatestCommitted(context.Background(), e.src.ID)
	if err != nil || latest.ID != first.ID {
		t.Fatalf("latest committed = %+v, %v; the cancelled point must stay invisible", latest, err)
	}
	var leftover int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM node_versions WHERE first_point > 1`).Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("%d version rows of the cancelled point remain (%v)", leftover, err)
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM node_versions WHERE last_point = 1`).Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("%d versions are still closed by the cancelled point (%v)", leftover, err)
	}
	e.assertManifest(first)

	retry := e.backup()
	if retry.Seq != 3 || retry.ModifiedCount != 2 || retry.AddedCount != 1 || retry.DeletedCount != 1 {
		t.Fatalf("retry = %+v", retry)
	}
	eq(t, "root after retry", e.names(retry.Seq, ""), []string{"a.txt", "added.txt", "docs/", "dup1.bin", "dup2.bin", "empty/"})
	e.assertManifest(retry)
}

func TestInterruptedBuildIsRecoveredAtStartup(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "alpha")
	first := e.backup()

	// Simulate a crash: a point is begun and partly written, then the process "dies".
	ctx := context.Background()
	lock, err := e.cat.LockSource(ctx, e.src.ID)
	if err != nil {
		t.Fatal(err)
	}
	point, prev, err := e.cat.BeginPoint(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := e.cat.RootNode(ctx, e.src.ID)
	before := e.entry(first.Seq, "a.txt")
	sha := make([]byte, 32)
	sha[0] = 7
	_, err = e.cat.ApplyDir(ctx, point, prev, root, []catalog.Change{
		{Kind: catalog.ChangeModify, Name: "a.txt", State: catalog.VersionOK, Size: 3, SHA256: sha, NodeID: before.NodeID, VersionID: before.VersionID},
		{Kind: catalog.ChangeAdd, Name: "ghost.txt", State: catalog.VersionOK, Size: 3, SHA256: sha},
	}, []catalog.ObjectRef{{SHA256: sha, Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	lock.Unlock() // the dead process' connection would have released it

	n, err := e.cat.RecoverStalePoints(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RecoverStalePoints = %d, %v", n, err)
	}
	if got := e.content(e.entry(first.Seq, "a.txt")); got != "alpha" {
		t.Fatalf("a.txt = %q after recovery", got)
	}
	eq(t, "root", e.names(first.Seq, ""), []string{"a.txt"})
	next := e.backup()
	if next.AddedCount+next.ModifiedCount+next.DeletedCount != 0 {
		t.Fatalf("recovery left the history inconsistent: %+v", next)
	}
	e.assertManifest(next)
}

func TestOnlyOneBackupPerSourceRuns(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "alpha")
	lock, err := e.cat.LockSource(context.Background(), e.src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, nil); err != catalog.ErrBusy {
		t.Fatalf("Run while locked = %v, want ErrBusy", err)
	}
	lock.Unlock()
	e.backup()
}

func TestVerifyDetectsMissingAndCorruptObjects(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	p := e.backup()
	ctx := context.Background()

	res, err := e.eng.Verify(ctx, p.ID, true, nil)
	if err != nil || !res.OK() || res.Objects != 4 {
		t.Fatalf("deep verify = %+v, %v", res, err)
	}
	if got, _ := e.cat.GetPoint(ctx, p.ID); got.Verification != catalog.VerificationVerified || got.VerifiedAt == nil {
		t.Fatalf("point after a deep verify = %+v", got)
	}

	alpha := e.entry(p.Seq, "a.txt")
	alphaHash, _ := repository.HashFromBytes(alpha.SHA256)
	_ = os.Chmod(e.repo.ObjectPath(alphaHash), 0o666)
	if err := os.WriteFile(e.repo.ObjectPath(alphaHash), []byte("ALPHA"), 0o666); err != nil { // same size, different bytes
		t.Fatal(err)
	}
	shallow, err := e.eng.Verify(ctx, p.ID, false, nil)
	if err != nil || !shallow.OK() {
		t.Fatalf("a shallow check cannot see same-size corruption: %+v, %v", shallow, err)
	}
	deep, err := e.eng.Verify(ctx, p.ID, true, nil)
	if err != nil || deep.OK() || deep.Corrupt != 1 {
		t.Fatalf("deep verify must find the corrupt object: %+v, %v", deep, err)
	}
	got, _ := e.cat.GetPoint(ctx, p.ID)
	if got.Verification != catalog.VerificationFailed || got.Restorable() {
		t.Fatalf("a point with a corrupt object must not be restorable: %+v", got)
	}

	bravo := e.entry(p.Seq, "docs/b.txt")
	bravoHash, _ := repository.HashFromBytes(bravo.SHA256)
	_ = os.Chmod(e.repo.ObjectPath(bravoHash), 0o666)
	if err := os.Remove(e.repo.ObjectPath(bravoHash)); err != nil {
		t.Fatal(err)
	}
	missing, err := e.eng.Verify(ctx, p.ID, false, nil)
	if err != nil || missing.OK() || missing.Missing != 1 || len(missing.Problems) == 0 {
		t.Fatalf("shallow verify must find the missing object: %+v, %v", missing, err)
	}
}

func TestVerifyDetectsTamperedCatalog(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	p := e.backup()
	if _, err := e.pool.Exec(context.Background(), `UPDATE node_versions SET size = size + 1 WHERE size = 5 AND NOT is_dir`); err != nil {
		t.Fatal(err)
	}
	res, err := e.eng.Verify(context.Background(), p.ID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ManifestOK || res.OK() {
		t.Fatalf("a modified catalog must be detected: %+v", res)
	}
}

func TestPrepareSourceRejectsUnsafeFolders(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	other := t.TempDir()
	file := filepath.Join(other, "file.txt")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	nested := filepath.Join(e.srcDir, "nested")
	_ = os.MkdirAll(nested, 0o755)
	cases := map[string]SourceRequest{
		"empty name":           {Name: " ", RootPath: other},
		"relative path":        {Name: "n", RootPath: `data\relative`},
		"missing folder":       {Name: "n", RootPath: filepath.Join(other, "missing")},
		"a file":               {Name: "n", RootPath: file},
		"unc path":             {Name: "n", RootPath: `\\server\share\data`},
		"the repository":       {Name: "n", RootPath: e.repo.Root()},
		"inside the repo":      {Name: "n", RootPath: filepath.Join(e.repo.Root(), "objects")},
		"inside other source":  {Name: "n", RootPath: nested},
		"contains other":       {Name: "n", RootPath: filepath.Dir(e.srcDir)},
		"bad exclude pattern":  {Name: "n", RootPath: other, Excludes: []string{"[bad"}},
		"control char in name": {Name: "bad\x07name", RootPath: other},
	}
	for name, req := range cases {
		if _, err := e.eng.PrepareSource(ctx, req, "tester"); err == nil {
			t.Errorf("%s: PrepareSource accepted %+v", name, req)
		}
	}
	good, err := e.eng.PrepareSource(ctx, SourceRequest{Name: " fine ", RootPath: other + `\`, Excludes: []string{" *.log ", ""}}, "tester")
	if err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
	if good.Name != "fine" || strings.HasSuffix(good.RootPath, `\`) || len(good.Excludes) != 1 || good.Excludes[0] != "*.log" {
		t.Fatalf("normalized source = %+v", good)
	}
	if _, err := e.cat.CreateSource(ctx, catalog.NewSource{Name: "test source", RootPath: other, CreatedBy: "t"}); err == nil {
		t.Fatal("a duplicate source name must be rejected")
	}
}

func TestSourceScheduleMustBeWithinBounds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fresh := t.TempDir()
	for _, minutes := range []int{-1, 0, MaxScheduleMinutes + 1} {
		if _, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "n", RootPath: fresh, ScheduleMinutes: &minutes}, "tester"); !errors.Is(err, ErrInvalidSource) {
			t.Errorf("PrepareSource accepted a schedule of %d minutes: %v", minutes, err)
		}
		if _, err := e.eng.PrepareSourceUpdate(ctx, e.src, SourceRequest{Name: "n", ScheduleMinutes: &minutes}); !errors.Is(err, ErrInvalidSource) {
			t.Errorf("PrepareSourceUpdate accepted a schedule of %d minutes: %v", minutes, err)
		}
	}
	for _, minutes := range []int{MinScheduleMinutes, 60, MaxScheduleMinutes} {
		got, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "n", RootPath: fresh, ScheduleMinutes: &minutes}, "tester")
		if err != nil || got.ScheduleMinutes == nil || *got.ScheduleMinutes != minutes {
			t.Errorf("PrepareSource with a schedule of %d minutes = %+v, %v", minutes, got, err)
		}
		got, err = e.eng.PrepareSourceUpdate(ctx, e.src, SourceRequest{Name: "n", ScheduleMinutes: &minutes})
		if err != nil || got.ScheduleMinutes == nil || *got.ScheduleMinutes != minutes {
			t.Errorf("PrepareSourceUpdate with a schedule of %d minutes = %+v, %v", minutes, got, err)
		}
	}
	manual, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "n", RootPath: fresh}, "tester")
	if err != nil || manual.ScheduleMinutes != nil {
		t.Fatalf("a source without a schedule is manual only: %+v, %v", manual, err)
	}
}

func TestPrepareSourceUpdate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cur := e.src
	update := func(req SourceRequest) (catalog.NewSource, error) { return e.eng.PrepareSourceUpdate(ctx, cur, req) }

	otherDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(otherDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherReq, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "other", RootPath: otherDir}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.cat.CreateSource(ctx, otherReq); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(e.srcDir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	rejected := map[string]SourceRequest{
		"missing folder":        {Name: "n", RootPath: filepath.Join(t.TempDir(), "missing")},
		"relative path":         {Name: "n", RootPath: `data\relative`},
		"unc path":              {Name: "n", RootPath: `\\server\share\data`},
		"the repository":        {Name: "n", RootPath: e.repo.Root()},
		"another source":        {Name: "n", RootPath: otherDir},
		"inside another source": {Name: "n", RootPath: filepath.Join(otherDir, "sub")},
		"empty name":            {Name: " "},
		"bad exclude pattern":   {Name: "n", Excludes: []string{"[bad"}},
	}
	for name, req := range rejected {
		if _, err := update(req); !errors.Is(err, ErrInvalidSource) {
			t.Errorf("%s: PrepareSourceUpdate accepted %+v (%v)", name, req, err)
		}
	}

	// The source being edited never counts as an overlap, so its folder can move up or down.
	for name, root := range map[string]string{"a subfolder of itself": nested, "the parent of itself": filepath.Dir(e.srcDir), "an unrelated folder": t.TempDir()} {
		got, err := update(SourceRequest{Name: " renamed ", RootPath: root + string(filepath.Separator)})
		if err != nil || got.RootPath != filepath.Clean(root) || got.Name != "renamed" {
			t.Errorf("%s: PrepareSourceUpdate = %+v, %v", name, got, err)
		}
	}

	// A folder that is not changed is not even looked at, so a source whose drive is offline can
	// still be renamed and rescheduled.
	if err := os.RemoveAll(e.srcDir); err != nil {
		t.Fatal(err)
	}
	hourly := 60
	for name, root := range map[string]string{"the same folder": cur.RootPath + string(filepath.Separator), "no folder given": ""} {
		got, err := update(SourceRequest{Name: "offline", RootPath: root, Excludes: []string{"*.log"}, ScheduleMinutes: &hourly})
		if err != nil || got.RootPath != cur.RootPath || got.Name != "offline" || got.ScheduleMinutes == nil || *got.ScheduleMinutes != 60 ||
			len(got.Excludes) != 1 || got.CreatedBy != cur.CreatedBy {
			t.Errorf("%s: PrepareSourceUpdate = %+v, %v", name, got, err)
		}
	}
}

func TestListChildrenPagesAreStableAndComplete(t *testing.T) {
	e := newEnv(t)
	var want []string
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("file-%02d.txt", i)
		e.write(name, "content "+name)
		want = append(want, name)
	}
	e.write("UPPER.txt", "upper") // sorts case-insensitively among the others
	want = append(want, "UPPER.txt")
	sort.Slice(want, func(i, j int) bool { return strings.ToLower(want[i]) < strings.ToLower(want[j]) })
	e.backup()
	got := e.names(1, "") // pages of 7
	eq(t, "paged names", got, want)
}

// cancelOnPhase cancels the run when it reports a given phase.
type cancelOnPhase struct {
	recorder
	phase  string
	cancel context.CancelFunc
}

func (c *cancelOnPhase) Phase(name string) {
	c.recorder.Phase(name)
	if name == c.phase {
		c.cancel()
	}
}

func TestCancellationAfterTheCommitDoesNotFailTheBackup(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "alpha")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rep := &cancelOnPhase{phase: PhaseCommitted, cancel: cancel}
	p, err := e.eng.Run(ctx, e.src.ID, RunOptions{}, rep)
	if err != nil {
		t.Fatalf("a cancellation after the commit turned a committed point into an error: %v", err)
	}
	latest, err := e.cat.LatestCommitted(context.Background(), e.src.ID)
	if err != nil || latest.ID != p.ID {
		t.Fatalf("latest committed = %+v, %v", latest, err)
	}
	e.assertManifest(p)
}

func TestCancellationBeforeTheCommitLeavesNothingVisible(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "alpha")
	first := e.backup()
	e.write("a.txt", "alpha two")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rep := &cancelOnPhase{phase: PhaseCommitting, cancel: cancel}
	if _, err := e.eng.Run(ctx, e.src.ID, RunOptions{}, rep); err == nil {
		t.Fatal("a backup cancelled before its commit must fail")
	}
	latest, err := e.cat.LatestCommitted(context.Background(), e.src.ID)
	if err != nil || latest.ID != first.ID {
		t.Fatalf("latest committed = %+v, %v; the cancelled point must stay invisible", latest, err)
	}
	if got := e.content(e.entry(first.Seq, "a.txt")); got != "alpha" {
		t.Fatalf("a.txt = %q", got)
	}
}

func TestPathsDeeperThanBrowsingAllowsAreRecordedAsSkipped(t *testing.T) {
	e := newEnv(t)
	e.write(strings.Repeat("d/", pathutil.MaxRelDepth+1)+"f.txt", "too deep to browse")
	e.write("ok.txt", "fine")
	rec := &recorder{}
	p, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, rec)
	if err != nil {
		t.Fatalf("a very deep tree must not fail the backup: %v", err)
	}
	if p.SkippedCount != 1 || !rec.has("deeper than") {
		t.Fatalf("skipped = %d, events = %v", p.SkippedCount, rec.events)
	}
	atLimit := strings.TrimSuffix(strings.Repeat("d/", pathutil.MaxRelDepth), "/")
	if en := e.entry(p.Seq, atLimit); !en.IsDir {
		t.Fatalf("the folder at the limit must still be browsable: %+v", en)
	}
	below, err := e.cat.ListChildren(context.Background(), catalog.ListParams{Parent: e.entry(p.Seq, atLimit).NodeID, Seq: p.Seq, Limit: 10})
	if err != nil || len(below.Entries) != 1 || below.Entries[0].State != catalog.VersionSkipped || !strings.Contains(below.Entries[0].SkipReason, "deeper than") {
		t.Fatalf("below the limit = %+v, %v", below.Entries, err)
	}
	e.assertManifest(p)
}

func canSymlink(t *testing.T, dir string) {
	t.Helper()
	probe := filepath.Join(dir, "symlink-probe")
	if err := os.Symlink("probe-target", probe); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	_ = os.Remove(probe)
}

// A file that is replaced by a link to another file after the folder was listed
// must not be recorded with the other file's content.
func TestFileSwappedForASymlinkIsNotRecordedAsItsTarget(t *testing.T) {
	e := newEnv(t)
	e.write("victim.txt", "the real content")
	e.write("other.txt", "someone else's content")
	canSymlink(t, e.srcDir)
	victim := filepath.Join(e.srcDir, "victim.txt")

	var once sync.Once
	e.eng.hook = func(event, rel string) {
		if event != "opening" || rel != "victim.txt" {
			return
		}
		once.Do(func() {
			if err := os.Remove(victim); err != nil {
				t.Error(err)
				return
			}
			if err := os.Symlink("other.txt", victim); err != nil {
				t.Error(err)
			}
		})
	}
	p, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, &recorder{})
	e.eng.hook = nil
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	en := e.entry(p.Seq, "victim.txt")
	if en.State != catalog.VersionSkipped || en.SHA256 != nil {
		t.Fatalf("victim.txt = %+v; another file's content must not be recorded under its name", en)
	}
	if got := e.content(e.entry(p.Seq, "other.txt")); got != "someone else's content" {
		t.Fatalf("other.txt = %q", got)
	}
	e.assertManifest(p)
}

// A folder that is replaced by a link after its parent was listed is not entered.
func TestFolderSwappedForASymlinkIsNotFollowed(t *testing.T) {
	e := newEnv(t)
	e.write("elsewhere/other.txt", "other")
	e.write("sub/inside.txt", "inside")
	canSymlink(t, e.srcDir)

	var once sync.Once
	e.eng.failReadDir = func(rel string) error {
		if rel != "sub" {
			return nil
		}
		once.Do(func() {
			if err := os.RemoveAll(filepath.Join(e.srcDir, "sub")); err != nil {
				t.Error(err)
				return
			}
			if err := os.Symlink("elsewhere", filepath.Join(e.srcDir, "sub")); err != nil {
				t.Error(err)
			}
		})
		return nil
	}
	rec := &recorder{}
	p, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, rec)
	e.eng.failReadDir = nil
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	if got := e.names(p.Seq, "sub"); len(got) != 0 {
		t.Fatalf("the folder behind a swapped-in link was followed: %v", got)
	}
	if !rec.has("no longer a plain folder") {
		t.Fatalf("events = %v", rec.events)
	}
	e.assertManifest(p)
}

// After a case-only rename and a failed build the catalog keeps an empty node for the
// new spelling; looking up the history by that spelling must still find the real one.
func TestHistoryLookupIgnoresNodesLeftByAFailedBuild(t *testing.T) {
	e := newEnv(t)
	e.write("Report.txt", "first")
	e.write("sub/x.txt", "x")
	first := e.backup()

	if err := os.Rename(filepath.Join(e.srcDir, "Report.txt"), filepath.Join(e.srcDir, "report.txt")); err != nil {
		t.Fatal(err)
	}
	e.write("sub/x.txt", "x changed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The root folder, with the new spelling, is recorded before sub/x.txt is opened.
	e.eng.hook = func(event, rel string) {
		if event == "opened" && rel == "sub/x.txt" {
			cancel()
		}
	}
	if _, err := e.eng.Run(ctx, e.src.ID, RunOptions{}, nil); err == nil {
		t.Fatal("cancelled backup must fail")
	}
	e.eng.hook = nil

	node, err := e.cat.ResolveNode(context.Background(), e.src.ID, "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := e.cat.Versions(context.Background(), node, first.Seq)
	if err != nil || len(versions) != 1 || versions[0].Name != "Report.txt" || !versions[0].StillCurrent {
		t.Fatalf("history by the new spelling = %+v, %v", versions, err)
	}
}

// While a build is running its closure of an old version is already in the catalog, but
// readers must keep seeing that version as current until the point is committed.
func TestVersionsHideTheClosureMadeByARunningBuild(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "one")
	e.write("sub/y.txt", "y")
	first := e.backup()
	e.write("a.txt", "two")
	e.write("sub/y.txt", "y changed")

	node, err := e.cat.ResolveNode(context.Background(), e.src.ID, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	// By the time sub/y.txt is opened, the root folder has already closed the old a.txt.
	e.eng.hook = func(event, rel string) {
		if event != "opened" || rel != "sub/y.txt" {
			return
		}
		versions, err := e.cat.Versions(context.Background(), node, first.Seq)
		if err != nil || len(versions) != 1 {
			t.Errorf("versions while building = %+v, %v", versions, err)
			return
		}
		if v := versions[0]; !v.StillCurrent || v.LastAt != nil || v.LastPoint != nil {
			t.Errorf("an uncommitted closure is visible: %+v", v)
		}
	}
	e.backup()
	e.eng.hook = nil
}

func (e *testEnv) attempts() []catalog.Point {
	e.t.Helper()
	pts, err := e.cat.ListAttempts(context.Background(), e.src.ID, 50)
	if err != nil {
		e.t.Fatal(err)
	}
	return pts
}

// backupSkippingUnchanged is what a scheduled run does.
func (e *testEnv) backupSkippingUnchanged(rep Reporter) (catalog.Point, error) {
	return e.eng.Run(context.Background(), e.src.ID, RunOptions{SkipUnchanged: true}, rep)
}

func TestSkipUnchangedRecordsNothingWhenNothingChanged(t *testing.T) {
	e := newEnv(t)
	seedTree(e)

	first, err := e.backupSkippingUnchanged(nil)
	if err != nil || first.Seq != 1 || first.Kind != "full" {
		t.Fatalf("the first backup of a source is always recorded: %+v, %v", first, err)
	}
	versions := e.versionCount()

	rec := &recorder{}
	if p, err := e.backupSkippingUnchanged(rec); !errors.Is(err, ErrUnchanged) || p.ID != 0 {
		t.Fatalf("an unchanged source = %+v, %v; want ErrUnchanged", p, err)
	}
	if !rec.has("nothing changed since recovery point #1") {
		t.Fatalf("events = %v", rec.events)
	}
	if got := e.attempts(); len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("attempts = %+v; an unchanged run must leave no point behind, not even a failed one", got)
	}
	if latest, err := e.cat.LatestCommitted(context.Background(), e.src.ID); err != nil || latest.ID != first.ID {
		t.Fatalf("latest committed = %+v, %v", latest, err)
	}
	if got := e.versionCount(); got != versions {
		t.Fatalf("version rows grew from %d to %d", versions, got)
	}
	e.assertManifest(first)

	// The sequence number of the dropped point is taken by the next real one.
	e.write("a.txt", "alpha v2")
	second, err := e.backupSkippingUnchanged(nil)
	if err != nil || second.Seq != 2 || second.ModifiedCount != 1 {
		t.Fatalf("the next change = %+v, %v; want point #2", second, err)
	}
	e.assertManifest(second)

	// Only automatic runs skip: a manual backup records a point even when nothing changed.
	if manual := e.backup(); manual.Seq != 3 || manual.AddedCount+manual.ModifiedCount+manual.DeletedCount != 0 {
		t.Fatalf("manual run of an unchanged source = %+v", manual)
	}
}

func TestSkipUnchangedStillRecordsEveryKindOfChange(t *testing.T) {
	cases := map[string]struct {
		change                   func(e *testEnv)
		added, modified, deleted int64
	}{
		"a new file":       {func(e *testEnv) { e.write("new.txt", "new") }, 1, 0, 0},
		"a changed file":   {func(e *testEnv) { e.write("a.txt", "alpha v2") }, 0, 1, 0},
		"a deleted file":   {func(e *testEnv) { e.remove("a.txt") }, 0, 0, 1},
		"a deleted folder": {func(e *testEnv) { e.remove("empty") }, 0, 0, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			seedTree(e)
			e.backup()
			c.change(e)
			p, err := e.backupSkippingUnchanged(nil)
			if err != nil || p.Seq != 2 || p.AddedCount != c.added || p.ModifiedCount != c.modified || p.DeletedCount != c.deleted {
				t.Fatalf("point = %+v, %v", p, err)
			}
			e.assertManifest(p)
		})
	}
}

// Items that could not be captured are not changes, but a different number of them is news
// worth a point: something broke, or an earlier problem went away.
func TestSkipUnchangedComparesTheNumberOfSkippedItems(t *testing.T) {
	e := newEnv(t)
	seedTree(e)
	e.backup()

	e.eng.failReadDir = func(rel string) error {
		if rel == "docs" {
			return os.ErrPermission
		}
		return nil
	}
	second, err := e.backupSkippingUnchanged(nil)
	if err != nil || second.Seq != 2 || second.SkippedCount != 1 || second.AddedCount+second.ModifiedCount+second.DeletedCount != 0 {
		t.Fatalf("a newly unreadable folder = %+v, %v; want a point that reports it", second, err)
	}
	if _, err := e.backupSkippingUnchanged(nil); !errors.Is(err, ErrUnchanged) {
		t.Fatalf("the same problem again = %v, want ErrUnchanged", err)
	}

	e.eng.failReadDir = nil
	third, err := e.backupSkippingUnchanged(nil)
	if err != nil || third.Seq != 3 || third.SkippedCount != 0 {
		t.Fatalf("the problem going away = %+v, %v; want a point that reports it", third, err)
	}
	if _, err := e.backupSkippingUnchanged(nil); !errors.Is(err, ErrUnchanged) {
		t.Fatalf("a healthy unchanged source = %v, want ErrUnchanged", err)
	}
	e.assertManifest(third)
}

// The common real case: a file was edited but is locked by the editor. The previous version is kept,
// so no path changed, yet the point must tell that the newest edit is not protected.
func TestSkipUnchangedStillReportsAFileThatCouldNotBeRead(t *testing.T) {
	e := newEnv(t)
	e.write("report.txt", "version one")
	e.backup()
	edit := func() { e.write("report.txt", "version two, but unreadable now") }
	// Replace the file with a folder just before it is opened, so reading it fails.
	e.eng.hook = func(event, rel string) {
		if event == "opening" && rel == "report.txt" {
			p := filepath.Join(e.srcDir, "report.txt")
			_ = os.Remove(p)
			_ = os.Mkdir(p, 0o755)
		}
	}
	edit()
	rec := &recorder{}
	p, err := e.backupSkippingUnchanged(rec)
	if err != nil || p.Seq != 2 || p.SkippedCount != 1 || p.AddedCount+p.ModifiedCount+p.DeletedCount != 0 || !rec.has("the previous version is kept") {
		t.Fatalf("point = %+v, %v, events %v; want a point that reports the file as not captured", p, err, rec.events)
	}
	if got := e.content(e.entry(p.Seq, "report.txt")); got != "version one" {
		t.Fatalf("report.txt at the new point = %q, want the previous version", got)
	}

	// Still locked at the next run: the same news is not recorded twice.
	e.remove("report.txt")
	edit()
	if _, err := e.backupSkippingUnchanged(nil); !errors.Is(err, ErrUnchanged) {
		t.Fatalf("the same unreadable file again = %v, want ErrUnchanged", err)
	}
}
