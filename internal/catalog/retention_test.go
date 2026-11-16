package catalog_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/catalog"
)

// What an edit of a history says about a path: the text is the content of a file, unless it is one of these.
const (
	gone    = "\x00gone"
	skipped = "\x00skipped"
)

// history writes recovery points straight into the catalog, without a folder on disk, so that a test can
// give every file exactly the versions it needs.
type history struct {
	t   *testing.T
	cat *catalog.Store
	src catalog.Source
}

func newHistory(t *testing.T, cat *catalog.Store, name string) *history {
	t.Helper()
	return &history{t: t, cat: cat, src: mustCreate(t, cat, name, nil)}
}

func digest(content string) []byte {
	sum := sha256.Sum256([]byte(content))
	return sum[:]
}

type edit struct {
	name  string
	isDir bool
	value string
}

// build begins a point and applies the edits to it, which maps a path to its new state: the content of a
// file, gone or skipped. A path ending in a slash is a folder; the folders of the root can hold files.
// What the edits do not mention stays as it was.
func (h *history) build(lock *catalog.SourceLock, edits map[string]string) catalog.Point {
	h.t.Helper()
	ctx := context.Background()
	p, prev, err := h.cat.BeginPoint(ctx, lock)
	if err != nil {
		h.t.Fatal(err)
	}
	root, err := h.cat.RootNode(ctx, h.src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	paths := make([]string, 0, len(edits))
	for path := range edits {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var top []edit
	inner := map[string][]edit{}
	for _, path := range paths {
		dir, name, nested := strings.Cut(path, "/")
		switch {
		case !nested:
			top = append(top, edit{name: dir, value: edits[path]})
		case name == "":
			top = append(top, edit{name: dir, isDir: true, value: edits[path]})
		default:
			inner[dir] = append(inner[dir], edit{name: name, value: edits[path]})
		}
	}
	ids := h.apply(ctx, p, prev, root, top)
	for dir, in := range inner {
		parent, ok := ids[dir]
		if !ok {
			existing, err := h.cat.ChildrenAt(ctx, root, prev)
			if err != nil || !existing[dir].IsDir {
				h.t.Fatalf("folder %q does not exist: %v", dir, err)
			}
			parent = existing[dir].NodeID
		}
		h.apply(ctx, p, prev, parent, in)
	}
	return p
}

func (h *history) apply(ctx context.Context, p catalog.Point, prev int, parent int64, edits []edit) map[string]int64 {
	h.t.Helper()
	existing, err := h.cat.ChildrenAt(ctx, parent, prev)
	if err != nil {
		h.t.Fatal(err)
	}
	var changes []catalog.Change
	var objects []catalog.ObjectRef
	for _, e := range edits {
		cur, has := existing[e.name]
		c := catalog.Change{Name: e.name, State: catalog.VersionOK, MTimeNS: int64(p.Seq) * int64(time.Second)}
		if has {
			c.NodeID, c.VersionID, c.WasDir = cur.NodeID, cur.VersionID, cur.IsDir
		}
		switch {
		case e.value == gone:
			if !has {
				continue
			}
			c.Kind = catalog.ChangeDelete
		case e.isDir:
			if has && cur.IsDir {
				continue
			}
			c.IsDir = true
		case e.value == skipped:
			c.State, c.SkipReason = catalog.VersionSkipped, "locked by another process"
		default:
			c.Size, c.SHA256 = int64(len(e.value)), digest(e.value)
			objects = append(objects, catalog.ObjectRef{SHA256: c.SHA256, Size: c.Size})
		}
		if c.Kind == 0 {
			c.Kind = catalog.ChangeAdd
			if has {
				c.Kind = catalog.ChangeModify
			}
		}
		changes = append(changes, c)
	}
	if len(changes) == 0 {
		return nil
	}
	res, err := h.cat.ApplyDir(ctx, p, prev, parent, changes, objects)
	if err != nil {
		h.t.Fatal(err)
	}
	return res.NodeIDs
}

// point records and commits a recovery point.
func (h *history) point(edits map[string]string) catalog.Point {
	h.t.Helper()
	ctx := context.Background()
	lock, err := h.cat.LockSource(ctx, h.src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer lock.Unlock()
	p := h.build(lock, edits)
	sum, err := h.cat.ComputeManifest(ctx, h.src.ID, p.Seq)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.cat.CommitPoint(ctx, lock, p, catalog.Stats{}, sum); err != nil {
		h.t.Fatal(err)
	}
	committed, err := h.cat.GetPoint(ctx, p.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return committed
}

// failedAttempt records a backup that failed before it wrote anything.
func (h *history) failedAttempt() {
	h.t.Helper()
	ctx := context.Background()
	lock, err := h.cat.LockSource(ctx, h.src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer lock.Unlock()
	p, _, err := h.cat.BeginPoint(ctx, lock)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.cat.FailPoint(ctx, lock, p, catalog.StateFailed, "disk full"); err != nil {
		h.t.Fatal(err)
	}
}

// crashedBuild leaves what a process that died in the middle of a backup leaves behind.
func (h *history) crashedBuild(edits map[string]string) {
	h.t.Helper()
	lock, err := h.cat.LockSource(context.Background(), h.src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer lock.Unlock()
	h.build(lock, edits)
}

func (h *history) prune(opts catalog.PruneOptions) catalog.PruneResult {
	h.t.Helper()
	lock, err := h.cat.LockSource(context.Background(), h.src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer lock.Unlock()
	res, err := h.cat.PruneVersions(context.Background(), lock, opts)
	if err != nil {
		h.t.Fatalf("PruneVersions(%+v): %v", opts, err)
	}
	return res
}

// versions lists the versions of a path, newest first, as "first-last" with an empty last for the one
// that is still current and an "s" in front of a version that is only a record of a file that was not captured.
func (h *history) versions(rel string) []string {
	h.t.Helper()
	ctx := context.Background()
	node, err := h.cat.ResolveNode(ctx, h.src.ID, rel)
	if err != nil {
		h.t.Fatalf("resolve %q: %v", rel, err)
	}
	latest, err := h.cat.LatestCommitted(ctx, h.src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	list, err := h.cat.Versions(ctx, node, latest.Seq)
	if err != nil {
		h.t.Fatal(err)
	}
	out := make([]string, len(list))
	for i, v := range list {
		last, mark := "", ""
		if v.LastPoint != nil {
			last = strconv.Itoa(*v.LastPoint)
		}
		if v.State == catalog.VersionSkipped {
			mark = "s"
		}
		out[i] = fmt.Sprintf("%s%d-%s", mark, v.FirstPoint, last)
	}
	return out
}

func (h *history) points() []catalog.Point {
	h.t.Helper()
	pts, err := h.cat.ListPoints(context.Background(), h.src.ID, 1000, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return pts
}

func (h *history) partialPoints() []int {
	var seqs []int
	for _, p := range h.points() {
		if p.Partial {
			seqs = append(seqs, p.Seq)
		}
	}
	slices.Sort(seqs)
	return seqs
}

// assertManifests checks the promise a partial flag makes: a point that is not partial still shows exactly
// what it showed when it was committed, and one that is partial does not.
func (h *history) assertManifests() {
	h.t.Helper()
	for _, p := range h.points() {
		sum, err := h.cat.ComputeManifest(context.Background(), h.src.ID, p.Seq)
		if err != nil {
			h.t.Fatal(err)
		}
		if same := bytes.Equal(sum, p.ManifestSHA256); same == p.Partial {
			h.t.Errorf("point #%d: partial = %v but its manifest %s the one recorded at commit", p.Seq, p.Partial, map[bool]string{true: "equals", false: "differs from"}[same])
		}
	}
}

func versionRows(t *testing.T, pool *pgxpool.Pool, sourceID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM node_versions WHERE source_id = $1`, sourceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sameStrings(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func TestPruneVersionsKeepsTheNewestVersionsOfEachFile(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)
	h := newHistory(t, cat, "files")
	h.point(map[string]string{"a.txt": "a1", "b.txt": "b1", "c.txt": "c1", "d.txt": skipped, "e.txt": "e1", "old.txt": "o1", "sub/": "", "sub/f.txt": "f1"})
	h.point(map[string]string{"a.txt": "a2", "b.txt": "b2", "old.txt": "o2"})
	h.point(map[string]string{"a.txt": "a3", "b.txt": "b3", "c.txt": skipped, "d.txt": "d2", "old.txt": "o3"})
	h.point(map[string]string{"a.txt": "a4", "b.txt": gone, "old.txt": gone})
	h.point(map[string]string{"a.txt": "a5", "c.txt": "c2", "d.txt": "d3", "sub/f.txt": "f2"})
	h.point(map[string]string{"a.txt": "a6", "b.txt": "b4", "sub/f.txt": "f3"})
	h.point(map[string]string{"a.txt": "a7", "c.txt": "c3", "sub/f.txt": "f4"})
	h.point(map[string]string{"a.txt": "a8", "sub/f.txt": "f5"})

	sameStrings(t, "a.txt before", h.versions("a.txt"), "8-", "7-7", "6-6", "5-5", "4-4", "3-3", "2-2", "1-1")
	sameStrings(t, "b.txt before", h.versions("b.txt"), "6-", "3-3", "2-2", "1-1")
	sameStrings(t, "c.txt before", h.versions("c.txt"), "7-", "5-6", "s3-4", "1-2")
	rowsBefore := versionRows(t, pool, h.src.ID)

	// The preview promises exactly what the prune then does.
	preview, err := cat.PreviewPrune(ctx, h.src.ID, 2)
	if err != nil || preview.Files != 5 || preview.Versions != 14 {
		t.Fatalf("PreviewPrune = %+v, %v; want 5 files and 14 versions", preview, err)
	}
	if none, err := cat.PreviewPrune(ctx, h.src.ID, 100); err != nil || none != (catalog.PrunePreview{}) {
		t.Fatalf("PreviewPrune(100) = %+v, %v; no file has that many versions", none, err)
	}
	if _, err := cat.PreviewPrune(ctx, h.src.ID, 0); err == nil {
		t.Fatal("PreviewPrune accepted a limit of 0")
	}
	if got := versionRows(t, pool, h.src.ID); got != rowsBefore {
		t.Fatalf("PreviewPrune deleted versions: %d rows, were %d", got, rowsBefore)
	}

	res := h.prune(catalog.PruneOptions{Max: 2})
	if res.Files != preview.Files || res.Versions != preview.Versions || res.PointsMarked != 6 {
		t.Fatalf("PruneVersions = %+v; the preview said %+v and points 1 to 6 lost versions", res, preview)
	}
	if got := versionRows(t, pool, h.src.ID); got != rowsBefore-14 {
		t.Fatalf("%d version rows left, want %d", got, rowsBefore-14)
	}

	// Each file keeps its newest versions, and the current one is always among them.
	sameStrings(t, "a.txt", h.versions("a.txt"), "8-", "7-7")
	sameStrings(t, "b.txt", h.versions("b.txt"), "6-", "3-3")
	// The skipped record older than the oldest kept version goes with the versions before it.
	sameStrings(t, "c.txt", h.versions("c.txt"), "7-", "5-6")
	// Two good versions are within the limit: the older record of a file that was not captured stays.
	sameStrings(t, "d.txt", h.versions("d.txt"), "5-", "3-4", "s1-2")
	sameStrings(t, "e.txt", h.versions("e.txt"), "1-")
	// A deleted file is limited like any other.
	sameStrings(t, "old.txt", h.versions("old.txt"), "3-3", "2-2")
	sameStrings(t, "sub/f.txt", h.versions("sub/f.txt"), "8-", "7-7")
	if sub, err := cat.ResolveAt(ctx, h.src.ID, 8, "sub"); err != nil || !sub.IsDir {
		t.Fatalf("the folder is gone: %+v, %v", sub, err)
	}

	if got := h.partialPoints(); !slices.Equal(got, []int{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("partial points = %v, want 1 to 6: the versions deleted belonged to them, and to no later point", got)
	}
	h.assertManifests()

	// Nothing is left to do, and the preview agrees.
	if again := h.prune(catalog.PruneOptions{Max: 2}); again != (catalog.PruneResult{}) {
		t.Fatalf("a second prune = %+v, want nothing", again)
	}
	if left, _ := cat.PreviewPrune(ctx, h.src.ID, 2); left != (catalog.PrunePreview{}) {
		t.Fatalf("PreviewPrune after the prune = %+v", left)
	}

	// Lowering the limit prunes further. A file never loses its last version, and the newest point stays whole.
	res = h.prune(catalog.PruneOptions{Max: 1})
	if res.Files != 6 || res.PointsMarked != 1 {
		t.Fatalf("PruneVersions(1) = %+v; want 6 files and point 7 flagged", res)
	}
	sameStrings(t, "a.txt", h.versions("a.txt"), "8-")
	sameStrings(t, "d.txt", h.versions("d.txt"), "5-")
	sameStrings(t, "e.txt", h.versions("e.txt"), "1-")
	sameStrings(t, "old.txt", h.versions("old.txt"), "3-3")
	if got := h.partialPoints(); !slices.Equal(got, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Fatalf("partial points = %v, want 1 to 7", got)
	}
	h.assertManifests()
}

func TestPruneVersionsRejectsWhatItCannotDo(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	h := newHistory(t, cat, "files")
	h.point(map[string]string{"a.txt": "a1"})
	h.point(map[string]string{"a.txt": "a2"})
	lock, err := cat.LockSource(ctx, h.src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	for _, max := range []int{0, -3} {
		if _, err := cat.PruneVersions(ctx, lock, catalog.PruneOptions{Max: max}); err == nil {
			t.Errorf("PruneVersions accepted a limit of %d", max)
		}
	}
	if _, err := cat.PruneVersions(ctx, nil, catalog.PruneOptions{Max: 1}); err == nil {
		t.Error("PruneVersions ran without the lock of the source")
	}
	if got := h.versions("a.txt"); len(got) != 2 {
		t.Fatalf("a refused prune changed the history: %q", got)
	}

	// A canceled prune stops before it deletes anything.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if res, err := cat.PruneVersions(canceled, lock, catalog.PruneOptions{Max: 1}); !errors.Is(err, context.Canceled) || res != (catalog.PruneResult{}) {
		t.Fatalf("PruneVersions with a canceled context = %+v, %v", res, err)
	}
	if got := h.versions("a.txt"); len(got) != 2 {
		t.Fatalf("a canceled prune changed the history: %q", got)
	}
}

// A backup that has just committed point SinceSeq can only have pushed over the limit the files it wrote a
// version for, so only those are looked at; a full prune finds the rest.
func TestPruneVersionsSinceSeqLooksOnlyAtRecentlyChangedFiles(t *testing.T) {
	cat, pool := newStore(t)
	h := newHistory(t, cat, "since")
	for p := 1; p <= 6; p++ {
		edits := map[string]string{"hot.txt": fmt.Sprintf("hot %d", p)}
		if p <= 4 {
			edits["cold.txt"] = fmt.Sprintf("cold %d", p)
		}
		h.point(edits)
	}
	other := newHistory(t, cat, "other")
	for p := 1; p <= 6; p++ {
		other.point(map[string]string{"hot.txt": fmt.Sprintf("hot %d", p)})
	}
	otherRows := versionRows(t, pool, other.src.ID)

	res := h.prune(catalog.PruneOptions{Max: 2, SinceSeq: 6, BatchFiles: 1})
	if res.Files != 1 || res.Versions != 4 || res.PointsMarked != 4 {
		t.Fatalf("prune since point 6 = %+v; want only hot.txt, 4 versions, points 1 to 4", res)
	}
	sameStrings(t, "hot.txt", h.versions("hot.txt"), "6-", "5-5")
	sameStrings(t, "cold.txt", h.versions("cold.txt"), "4-", "3-3", "2-2", "1-1")

	// Asking again for the same files finds nothing, and a point after the last change finds no file.
	if res := h.prune(catalog.PruneOptions{Max: 2, SinceSeq: 6}); res != (catalog.PruneResult{}) {
		t.Fatalf("the same prune again = %+v", res)
	}
	if res := h.prune(catalog.PruneOptions{Max: 2, SinceSeq: 7}); res != (catalog.PruneResult{}) {
		t.Fatalf("prune since a point that does not exist = %+v", res)
	}

	// The full prune takes the old file too. The points it belonged to were flagged already.
	res = h.prune(catalog.PruneOptions{Max: 2})
	if res.Files != 1 || res.Versions != 2 || res.PointsMarked != 0 {
		t.Fatalf("full prune = %+v; want cold.txt, 2 versions, no point newly flagged", res)
	}
	sameStrings(t, "cold.txt", h.versions("cold.txt"), "4-", "3-3")
	h.assertManifests()

	// Another source is never touched, whatever its files look like.
	if got := versionRows(t, pool, other.src.ID); got != otherRows {
		t.Fatalf("the other source has %d version rows, had %d", got, otherRows)
	}
	sameStrings(t, "other hot.txt", other.versions("hot.txt"), "6-", "5-5", "4-4", "3-3", "2-2", "1-1")
	if got := other.partialPoints(); len(got) != 0 {
		t.Fatalf("points of the other source were flagged: %v", got)
	}
	other.assertManifests()
}

// Only committed history counts. A failed attempt is no recovery point, and what a build that died left
// behind belongs to nobody yet.
func TestPruneVersionsOnlyCountsCommittedHistory(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	h := newHistory(t, cat, "committed")
	h.point(map[string]string{"x.txt": "x1", "z.txt": "z1"})
	h.point(map[string]string{"x.txt": "x2"})
	h.point(map[string]string{"x.txt": "x3"})
	h.failedAttempt() // takes sequence number 4
	h.point(map[string]string{"y.txt": "y1"})
	h.point(map[string]string{"z.txt": "z2"})
	h.point(map[string]string{"z.txt": "z3"})
	// A build that died is half way through changing x.txt.
	h.crashedBuild(map[string]string{"x.txt": "x4"})

	sameStrings(t, "z.txt before", h.versions("z.txt"), "7-", "6-6", "1-5")

	res := h.prune(catalog.PruneOptions{Max: 2})
	// x.txt has two committed versions to keep besides the one the dead build closed; z.txt loses the version
	// that was valid across the failed attempt.
	if res.Files != 2 || res.Versions != 2 {
		t.Fatalf("PruneVersions = %+v; want x.txt and z.txt to lose one version each", res)
	}
	if got := h.partialPoints(); !slices.Equal(got, []int{1, 2, 3, 5}) {
		t.Fatalf("partial points = %v; the failed attempt is no point and the later ones lost nothing", got)
	}
	attempts, err := cat.ListAttempts(ctx, h.src.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range attempts {
		if a.State != catalog.StateCommitted && a.Partial {
			t.Errorf("point #%d is %s and was flagged partial", a.Seq, a.State)
		}
	}

	// What the dead build wrote is still referenced, so it is not swept before recovery has cleaned up after it.
	unreferenced := func() [][]byte {
		t.Helper()
		refs, err := cat.UnreferencedObjects(ctx, nil, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out [][]byte
		for _, r := range refs {
			out = append(out, r.SHA256)
		}
		return out
	}
	want := [][]byte{digest("x1"), digest("z1")}
	slices.SortFunc(want, bytes.Compare)
	if got := unreferenced(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unreferenced objects = %x, want the pruned x1 and z1 only: x4 belongs to the build that died", got)
	}

	// Recovery removes what the dead build left. The history is what a prune of committed history alone gives.
	if n, err := cat.RecoverStalePoints(ctx); err != nil || n != 1 {
		t.Fatalf("RecoverStalePoints = %d, %v", n, err)
	}
	want = append(want, digest("x4"))
	slices.SortFunc(want, bytes.Compare)
	if got := unreferenced(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unreferenced objects after recovery = %x, want x4 too", got)
	}
	sameStrings(t, "x.txt", h.versions("x.txt"), "3-", "2-2")
	sameStrings(t, "z.txt", h.versions("z.txt"), "7-", "6-6")
	h.assertManifests()
}

// pruneRow is a version row as the reference implementation sees it.
type pruneRow struct {
	id, node int64
	first    int
	last     *int
	isDir    bool
	state    string
}

func loadRows(t *testing.T, pool *pgxpool.Pool, sourceID int64) []pruneRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id, node_id, first_point, last_point, is_dir, state FROM node_versions WHERE source_id = $1 ORDER BY id`, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []pruneRow
	for rows.Next() {
		var r pruneRow
		if err := rows.Scan(&r.id, &r.node, &r.first, &r.last, &r.isDir, &r.state); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// referencePrune is the rule written the plain way: per file, rank the good versions newest first, and
// delete every version of the file that is older than the one at rank keep, if there are more than keep.
func referencePrune(rows []pruneRow, keep, latest int) (deleted map[int64]bool, files map[int64]bool) {
	deleted, files = map[int64]bool{}, map[int64]bool{}
	byNode := map[int64][]pruneRow{}
	for _, r := range rows {
		byNode[r.node] = append(byNode[r.node], r)
	}
	for node, versions := range byNode {
		var good []pruneRow
		for _, v := range versions {
			if !v.isDir && v.state == catalog.VersionOK && v.first <= latest {
				good = append(good, v)
			}
		}
		if len(good) <= keep {
			continue
		}
		slices.SortFunc(good, func(a, b pruneRow) int { return cmp.Or(cmp.Compare(b.first, a.first), cmp.Compare(b.id, a.id)) })
		edge := good[keep-1]
		for _, v := range versions {
			older := v.first < edge.first || (v.first == edge.first && v.id < edge.id)
			if !v.isDir && older && v.last != nil && *v.last < latest {
				deleted[v.id], files[node] = true, true
			}
		}
	}
	return deleted, files
}

func TestPruneVersionsAgreesWithAReferenceImplementation(t *testing.T) {
	for _, keep := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("keep %d", keep), func(t *testing.T) {
			ctx := context.Background()
			cat, pool := newStore(t)
			h := newHistory(t, cat, "random")
			rng := rand.New(rand.NewSource(20260116))
			names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "sub/x", "sub/y", "sub/z"}
			for p := 1; p <= 14; p++ {
				edits := map[string]string{}
				if p == 1 {
					edits["sub/"] = ""
				}
				for _, name := range names {
					switch r := rng.Intn(10); {
					case r < 4:
						edits[name] = fmt.Sprintf("%s at point %d", name, p)
					case r == 4:
						edits[name] = gone
					case r == 5, r == 6:
						edits[name] = skipped
					}
				}
				h.point(edits)
			}

			before := loadRows(t, pool, h.src.ID)
			latest := len(h.points())
			wantGone, wantFiles := referencePrune(before, keep, latest)
			if len(wantGone) == 0 {
				t.Fatal("the generated history gives the prune nothing to do")
			}
			var wantPartial []int
			for _, p := range h.points() {
				for _, v := range before {
					if wantGone[v.id] && v.first <= p.Seq && p.Seq <= *v.last {
						wantPartial = append(wantPartial, p.Seq)
						break
					}
				}
			}
			slices.Sort(wantPartial)

			preview, err := cat.PreviewPrune(ctx, h.src.ID, keep)
			if err != nil || preview.Versions != int64(len(wantGone)) || preview.Files != int64(len(wantFiles)) {
				t.Fatalf("PreviewPrune = %+v, %v; the reference deletes %d versions of %d files", preview, err, len(wantGone), len(wantFiles))
			}
			res := h.prune(catalog.PruneOptions{Max: keep, BatchFiles: 3})
			if res.Versions != preview.Versions || res.Files != preview.Files || res.PointsMarked != len(wantPartial) {
				t.Fatalf("PruneVersions = %+v; want %+v and %d points", res, preview, len(wantPartial))
			}

			var wantLeft []int64
			for _, v := range before {
				if !wantGone[v.id] {
					wantLeft = append(wantLeft, v.id)
				}
			}
			var left []int64
			for _, v := range loadRows(t, pool, h.src.ID) {
				left = append(left, v.id)
			}
			if !reflect.DeepEqual(left, wantLeft) {
				t.Fatalf("%d version rows are left, the reference keeps %d, and they are not the same rows", len(left), len(wantLeft))
			}
			if got := h.partialPoints(); !slices.Equal(got, wantPartial) {
				t.Fatalf("partial points = %v, want %v", got, wantPartial)
			}
			h.assertManifests()
			if again := h.prune(catalog.PruneOptions{Max: keep}); again != (catalog.PruneResult{}) {
				t.Fatalf("a second prune = %+v", again)
			}
		})
	}
}

// An object is shared by every version, of any source, that has the same content, and it is only
// unreferenced when the last of them is gone.
func TestObjectsStayReferencedUntilEveryVersionDropsThem(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	a := newHistory(t, cat, "alpha")
	b := newHistory(t, cat, "beta")
	a.point(map[string]string{"f.txt": "shared bytes", "g.txt": "only alpha"})
	a.point(map[string]string{"f.txt": "alpha f2", "g.txt": "alpha g2"})
	b.point(map[string]string{"f.txt": "shared bytes"})
	b.point(map[string]string{"f.txt": "beta f2"})

	unreferenced := func() []catalog.ObjectRef {
		t.Helper()
		refs, err := cat.UnreferencedObjects(ctx, nil, 100)
		if err != nil {
			t.Fatal(err)
		}
		return refs
	}
	if refs := unreferenced(); len(refs) != 0 {
		t.Fatalf("%d objects are unreferenced while every version is there", len(refs))
	}

	a.prune(catalog.PruneOptions{Max: 1})
	refs := unreferenced()
	if len(refs) != 1 || !bytes.Equal(refs[0].SHA256, digest("only alpha")) || refs[0].Size != int64(len("only alpha")) {
		t.Fatalf("after pruning alpha: %+v; only the content nothing else has is unreferenced, the shared one is still needed by beta", refs)
	}
	if ok, err := cat.ForgetObject(ctx, digest("shared bytes")); err != nil || ok {
		t.Fatalf("ForgetObject of an object beta still needs = %v, %v", ok, err)
	}

	b.prune(catalog.PruneOptions{Max: 1})
	refs = unreferenced()
	if len(refs) != 2 {
		t.Fatalf("after pruning beta: %d unreferenced objects, want the shared one and alpha's", len(refs))
	}
	want := [][]byte{digest("only alpha"), digest("shared bytes")}
	slices.SortFunc(want, bytes.Compare)
	for i, r := range refs {
		if !bytes.Equal(r.SHA256, want[i]) {
			t.Fatalf("unreferenced object %d = %x, want %x (ordered by hash)", i, r.SHA256, want[i])
		}
	}
}

func TestUnreferencedObjectsPagesByHash(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)
	h := newHistory(t, cat, "paging")
	h.point(map[string]string{"kept.txt": "referenced"})
	var all [][]byte
	for i := 0; i < 7; i++ {
		sum := digest(fmt.Sprintf("orphan %d", i))
		if _, err := pool.Exec(ctx, `INSERT INTO objects (sha256, size) VALUES ($1, $2)`, sum, 10+i); err != nil {
			t.Fatal(err)
		}
		all = append(all, sum)
	}
	slices.SortFunc(all, bytes.Compare)

	var paged [][]byte
	var after []byte
	for {
		page, err := cat.UnreferencedObjects(ctx, after, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 3 {
			t.Fatalf("a page of %d objects, the limit is 3", len(page))
		}
		for _, o := range page {
			if after != nil && bytes.Compare(o.SHA256, after) <= 0 {
				t.Fatalf("object %x is not after %x", o.SHA256, after)
			}
			paged = append(paged, o.SHA256)
			after = o.SHA256
		}
	}
	if !reflect.DeepEqual(paged, all) {
		t.Fatalf("paging gave %d objects, want the %d unreferenced ones in order", len(paged), len(all))
	}
	// The limit is a ceiling: a zero limit falls back to a default instead of returning nothing.
	if page, err := cat.UnreferencedObjects(ctx, all[2], 0); err != nil || len(page) != 4 || !bytes.Equal(page[0].SHA256, all[3]) {
		t.Fatalf("after the third object: %d objects, %v; want the 4 that follow it", len(page), err)
	}

	// ForgetObject only deletes what is unreferenced, and only once.
	if ok, err := cat.ForgetObject(ctx, digest("referenced")); err != nil || ok {
		t.Fatalf("ForgetObject of a referenced object = %v, %v", ok, err)
	}
	if ok, err := cat.ForgetObject(ctx, all[0]); err != nil || !ok {
		t.Fatalf("ForgetObject of an unreferenced object = %v, %v", ok, err)
	}
	if ok, err := cat.ForgetObject(ctx, all[0]); err != nil || ok {
		t.Fatalf("ForgetObject of an object that is already gone = %v, %v", ok, err)
	}
	if ok, err := cat.ForgetObject(ctx, digest("never stored")); err != nil || ok {
		t.Fatalf("ForgetObject of an unknown object = %v, %v", ok, err)
	}
	var kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM objects WHERE sha256 = $1`, digest("referenced")).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the referenced object's record: %d, %v", kept, err)
	}
}

// A version that is being written has not committed yet, so the check for references cannot see it. The
// foreign key still refuses to let its object go.
func TestForgetObjectLosesToAVersionThatIsBeingWritten(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cat, pool := newStore(t)
	h := newHistory(t, cat, "racing")
	h.point(map[string]string{"f.txt": "one"})
	node, err := cat.ResolveNode(ctx, h.src.ID, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	orphan := digest("about to be used")
	if _, err := pool.Exec(ctx, `INSERT INTO objects (sha256, size) VALUES ($1, 4)`, orphan); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO node_versions (node_id, source_id, first_point, is_dir, state, size, sha256) VALUES ($1, $2, 1000, false, 'ok', 4, $3)`,
		node, h.src.ID, orphan); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		ok  bool
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		ok, err := cat.ForgetObject(ctx, orphan)
		done <- outcome{ok, err}
	}()
	// The delete waits for the transaction that holds the object, so it is still running here.
	for blocked := false; !blocked; {
		select {
		case o := <-done:
			t.Fatalf("ForgetObject finished while the version was uncommitted: %+v", o)
		case <-ctx.Done():
			t.Fatal("ForgetObject never waited for the open transaction")
		case <-time.After(20 * time.Millisecond):
		}
		if err := pool.QueryRow(ctx,
			`SELECT count(*) > 0 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE 'DELETE FROM objects%'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if o := <-done; o.err != nil || o.ok {
		t.Fatalf("ForgetObject = %+v; want (false, nil) because the version refers to the object now", o)
	}
	var records int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM objects WHERE sha256 = $1`, orphan).Scan(&records); err != nil || records != 1 {
		t.Fatalf("the object's record: %d, %v", records, err)
	}
}
