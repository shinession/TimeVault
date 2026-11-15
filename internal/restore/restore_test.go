package restore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/repository"
	"timevault/internal/restore"
	"timevault/internal/testenv"
)

var ctx = context.Background()

func newJob(t *testing.T, e *testenv.Env) int64 {
	t.Helper()
	var id int64
	err := e.Pool.QueryRow(ctx, `INSERT INTO jobs (kind, state, requested_by) VALUES ('restore', 'running', 'tester') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// run previews, confirms and executes a request the way the service does.
func run(t *testing.T, e *testenv.Env, req restore.Request) (restore.Plan, restore.Result, error) {
	t.Helper()
	plan, err := e.Restore.Preview(ctx, req)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	tok := e.Restore.IssueToken(plan, "alice")
	if _, err := e.Restore.Confirm(ctx, req, tok, "alice"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	res, err := e.Restore.Execute(ctx, newJob(t, e), req, nil)
	return plan, res, err
}

func seed(e *testenv.Env) {
	e.Write("a.txt", "alpha")
	e.Write("docs/b.txt", "bravo")
	e.Write("docs/deep/c.txt", "charlie")
	e.Write("empty-ish/keep.txt", "k")
}

func req(e *testenv.Env, point catalog.Point, dest string, paths ...string) restore.Request {
	return restore.Request{SourceID: e.Src.ID, PointID: point.ID, Paths: paths, DestName: dest, Policy: restore.PolicySkip}
}

func TestPreviewDoesNotWriteAnything(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	plan, err := e.Restore.Preview(ctx, req(e, p, "r1", "docs"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Totals.Dirs != 2 || plan.Totals.Files != 2 || plan.Totals.Create != 2 || plan.Totals.Blocked != 0 {
		t.Fatalf("totals = %+v", plan.Totals)
	}
	if plan.Totals.Bytes != int64(len("bravo")+len("charlie")) {
		t.Fatalf("bytes = %d", plan.Totals.Bytes)
	}
	if plan.Digest == "" || len(plan.Sample) != 4 {
		t.Fatalf("plan = %+v", plan)
	}
	if e.Restored("r1") {
		t.Fatal("a preview must not create the destination")
	}
	if plan.DestDir != filepath.Join(e.RestoreRoot, "r1") {
		t.Fatalf("DestDir = %q", plan.DestDir)
	}
}

func TestRestoreFolderAndFileFromOlderPointAfterDeletion(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	first := e.Backup()
	e.Remove("docs")
	e.Write("a.txt", "alpha v2")
	second := e.Backup()

	// The deleted folder comes back from the first point.
	plan, res, err := run(t, e, req(e, first, "recovered", "docs", "a.txt"))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if res.Created != 3 || res.Dirs != 2 || res.Failed != 0 || plan.Totals.Create != 3 {
		t.Fatalf("result = %+v plan = %+v", res, plan.Totals)
	}
	for rel, want := range map[string]string{
		"recovered/a.txt":           "alpha",
		"recovered/docs/b.txt":      "bravo",
		"recovered/docs/deep/c.txt": "charlie",
	} {
		if got := e.ReadRestored(rel); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	// Metadata: the modification time of the original is restored.
	st, err := os.Stat(filepath.Join(e.RestoreRoot, "recovered", "docs", "b.txt"))
	if err != nil || st.ModTime().Year() != 2026 || st.ModTime().Month() != time.January {
		t.Fatalf("restored modification time = %v (%v)", st.ModTime(), err)
	}
	// Nothing temporary is left behind.
	_ = filepath.WalkDir(filepath.Join(e.RestoreRoot, "recovered"), func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".tvpart") {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})

	// The newer point restores the newer content.
	if _, _, err := run(t, e, req(e, second, "latest", "a.txt")); err != nil {
		t.Fatal(err)
	}
	if got := e.ReadRestored("latest/a.txt"); got != "alpha v2" {
		t.Fatalf("latest/a.txt = %q", got)
	}
}

func TestPerItemResultsAreStored(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	plan, err := e.Restore.Preview(ctx, req(e, p, "r1", "docs"))
	if err != nil {
		t.Fatal(err)
	}
	jobID := newJob(t, e)
	if _, err := e.Restore.Execute(ctx, jobID, plan.Request, nil); err != nil {
		t.Fatal(err)
	}
	items, err := e.Restore.ListItems(ctx, jobID, "", 0, 100)
	if err != nil || len(items) != 4 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	for _, it := range items {
		if it.Status != "ok" {
			t.Errorf("item %+v should be ok", it)
		}
	}
	if failed, _ := e.Restore.ListItems(ctx, jobID, "failed", 0, 100); len(failed) != 0 {
		t.Fatalf("unexpected failures: %+v", failed)
	}
}

func TestRestoreWholeSourceUsesSourceNameFolder(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	if _, _, err := run(t, e, req(e, p, "all", "")); err != nil {
		t.Fatal(err)
	}
	if got := e.ReadRestored("all/Team Files/docs/deep/c.txt"); got != "charlie" {
		t.Fatalf("whole-source restore = %q", got)
	}
	if !e.Restored("all/Team Files/empty-ish/keep.txt") {
		t.Fatal("every folder must be restored")
	}
}

func TestConflictPolicies(t *testing.T) {
	e := testenv.New(t)
	e.Write("a.txt", "alpha")
	e.Write("b.txt", "bravo")
	p := e.Backup()
	base := req(e, p, "d", "a.txt", "b.txt")
	if _, _, err := run(t, e, base); err != nil {
		t.Fatal(err)
	}

	again, err := e.Restore.Preview(ctx, base)
	if err != nil || again.Totals.SkipSame != 2 || again.Totals.Writes() != 0 {
		t.Fatalf("restoring identical files must be a no-op: %+v, %v", again.Totals, err)
	}

	local := filepath.Join(e.RestoreRoot, "d", "a.txt")
	edit := func(content string) {
		t.Helper()
		if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	edit("LOCAL EDIT")
	skip := base
	skip.Policy = restore.PolicySkip
	plan, res, err := run(t, e, skip)
	if err != nil || plan.Totals.SkipExists != 1 || res.Skipped != 2 {
		t.Fatalf("skip policy: plan %+v res %+v err %v", plan.Totals, res, err)
	}
	if got := e.ReadRestored("d/a.txt"); got != "LOCAL EDIT" {
		t.Fatalf("skip policy changed the existing file: %q", got)
	}

	over := base
	over.Policy = restore.PolicyOverwrite
	plan, res, err = run(t, e, over)
	if err != nil || plan.Totals.Overwrite != 1 || res.Overwritten != 1 {
		t.Fatalf("overwrite policy: plan %+v res %+v err %v", plan.Totals, res, err)
	}
	if got := e.ReadRestored("d/a.txt"); got != "alpha" {
		t.Fatalf("overwrite policy left %q", got)
	}

	edit("LOCAL EDIT 2")
	ren := base
	ren.Policy = restore.PolicyRename
	plan, res, err = run(t, e, ren)
	if err != nil || plan.Totals.Rename != 1 || res.Renamed != 1 {
		t.Fatalf("rename policy: plan %+v res %+v err %v", plan.Totals, res, err)
	}
	if got := e.ReadRestored("d/a.txt"); got != "LOCAL EDIT 2" {
		t.Fatalf("rename policy must keep the existing file, got %q", got)
	}
	renamed := "d/a (restored 20260601-120000).txt"
	if got := e.ReadRestored(renamed); got != "alpha" {
		t.Fatalf("%s = %q", renamed, got)
	}
}

// Equal size and modification time do not prove equal content: a file corrupted in place keeps both.
func TestSameSizeAndDateButOtherBytesIsNotTreatedAsIdentical(t *testing.T) {
	e := testenv.New(t)
	e.Write("a.txt", "alpha")
	e.Write("b.txt", "bravo")
	p := e.Backup()
	base := req(e, p, "d", "a.txt", "b.txt")
	if _, _, err := run(t, e, base); err != nil {
		t.Fatal(err)
	}

	local := filepath.Join(e.RestoreRoot, "d", "a.txt")
	st, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("ALPHA"), 0o644); err != nil { // same length, other bytes
		t.Fatal(err)
	}
	if err := os.Chtimes(local, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}

	plan, err := e.Restore.Preview(ctx, base)
	if err != nil || plan.Totals.SkipSame != 1 || plan.Totals.SkipExists != 1 {
		t.Fatalf("the damaged file must not count as identical: %+v, %v", plan.Totals, err)
	}

	over := base
	over.Policy = restore.PolicyOverwrite
	plan, res, err := run(t, e, over)
	if err != nil || plan.Totals.Overwrite != 1 || plan.Totals.SkipSame != 1 || res.Overwritten != 1 {
		t.Fatalf("overwrite policy: plan %+v res %+v err %v", plan.Totals, res, err)
	}
	if got := e.ReadRestored("d/a.txt"); got != "alpha" {
		t.Fatalf("the damaged file was not replaced: %q", got)
	}
}

func TestMissingObjectBlocksTheRestoreUnlessPartialIsAllowed(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	b, err := e.Cat.ResolveAt(ctx, e.Src.ID, p.Seq, "docs/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := repository.HashFromBytes(b.SHA256)
	_ = os.Chmod(e.Repo.ObjectPath(h), 0o666)
	if err := os.Remove(e.Repo.ObjectPath(h)); err != nil {
		t.Fatal(err)
	}

	r := req(e, p, "r1", "docs", "a.txt")
	plan, err := e.Restore.Preview(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Totals.Missing != 1 || plan.Totals.Blocked != 1 || len(plan.Problems) != 1 || !strings.Contains(plan.Problems[0].Problem, "missing") {
		t.Fatalf("preview must flag the missing object: %+v problems %+v", plan.Totals, plan.Problems)
	}

	_, _, err = run(t, e, r)
	if !errors.Is(err, restore.ErrBlocked) {
		t.Fatalf("restore with a missing object = %v, want ErrBlocked", err)
	}
	if e.Restored("r1") {
		t.Fatal("nothing may be written when the restore is blocked")
	}

	r.AllowPartial = true
	jobID := newJob(t, e)
	res, err := e.Restore.Execute(ctx, jobID, r, nil)
	if err == nil || res.Failed != 1 {
		t.Fatalf("partial restore must report the failure: res %+v err %v", res, err)
	}
	if got := e.ReadRestored("r1/a.txt"); got != "alpha" {
		t.Fatalf("the restorable file must still be restored, got %q", got)
	}
	if got := e.ReadRestored("r1/docs/deep/c.txt"); got != "charlie" {
		t.Fatalf("c.txt = %q", got)
	}
	if e.Restored("r1/docs/b.txt") {
		t.Fatal("the file without content must not appear")
	}
	failed, err := e.Restore.ListItems(ctx, jobID, "failed", 0, 10)
	if err != nil || len(failed) != 1 || failed[0].Rel != "docs/b.txt" {
		t.Fatalf("failed items = %+v, %v", failed, err)
	}
}

func TestCorruptObjectIsNeverRestored(t *testing.T) {
	e := testenv.New(t)
	e.Write("a.txt", "alpha")
	e.Write("b.txt", "bravo")
	p := e.Backup()
	a, _ := e.Cat.ResolveAt(ctx, e.Src.ID, p.Seq, "a.txt")
	h, _ := repository.HashFromBytes(a.SHA256)
	_ = os.Chmod(e.Repo.ObjectPath(h), 0o666)
	if err := os.WriteFile(e.Repo.ObjectPath(h), []byte("ALPHA"), 0o666); err != nil { // same size, wrong bytes
		t.Fatal(err)
	}

	r := req(e, p, "r1", "a.txt", "b.txt")
	r.AllowPartial = true
	res, err := e.Restore.Execute(ctx, newJob(t, e), r, nil)
	if err == nil || res.Failed != 1 || res.Created != 1 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if e.Restored("r1/a.txt") {
		t.Fatal("corrupt content must never be moved into place")
	}
	if got := e.ReadRestored("r1/b.txt"); got != "bravo" {
		t.Fatalf("b.txt = %q", got)
	}
	_ = filepath.WalkDir(e.RestoreRoot, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".tvpart") {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}

func TestRequestValidation(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	good := req(e, p, "ok", "a.txt")

	cases := map[string]func(r *restore.Request){
		"empty destination":     func(r *restore.Request) { r.DestName = " " },
		"dot dot destination":   func(r *restore.Request) { r.DestName = ".." },
		"nested destination":    func(r *restore.Request) { r.DestName = "a/b" },
		"backslash destination": func(r *restore.Request) { r.DestName = `a\b` },
		"drive destination":     func(r *restore.Request) { r.DestName = "C:" },
		"reserved destination":  func(r *restore.Request) { r.DestName = "CON" },
		"temp prefix":           func(r *restore.Request) { r.DestName = ".tvpart-x" },
		"traversal path":        func(r *restore.Request) { r.Paths = []string{"../outside"} },
		"missing path":          func(r *restore.Request) { r.Paths = []string{"nope.txt"} },
		"unknown policy":        func(r *restore.Request) { r.Policy = "delete-extra-files" },
		"overlapping":           func(r *restore.Request) { r.Paths = []string{"docs", "docs/b.txt"} },
		"overlapping whole":     func(r *restore.Request) { r.Paths = []string{"", "a.txt"} },
		"duplicate":             func(r *restore.Request) { r.Paths = []string{"a.txt", "a.txt"} },
		"nothing selected":      func(r *restore.Request) { r.Paths = nil },
		"too many":              func(r *restore.Request) { r.Paths = make([]string, 51) },
		"same base name":        func(r *restore.Request) { r.Paths = []string{"docs/b.txt", "empty-ish/b.txt"} },
		"unknown point":         func(r *restore.Request) { r.PointID = 999999 },
	}
	for name, mutate := range cases {
		r := good
		mutate(&r)
		_, err := e.Restore.Preview(ctx, r)
		if err == nil {
			t.Errorf("%s: Preview accepted %+v", name, r)
		}
	}
	if _, err := e.Restore.Preview(ctx, good); err != nil {
		t.Fatalf("the valid request must work: %v", err)
	}

	// A point of another source cannot be combined with this source.
	otherDir := filepath.Join(t.TempDir(), "other")
	_ = os.MkdirAll(otherDir, 0o755)
	_ = os.WriteFile(filepath.Join(otherDir, "x.txt"), []byte("x"), 0o644)
	srcReq, err := e.Eng.PrepareSource(ctx, backup.SourceRequest{Name: "Other", RootPath: otherDir}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.Cat.CreateSource(ctx, srcReq)
	if err != nil {
		t.Fatal(err)
	}
	op, err := e.Eng.Run(ctx, other.ID, backup.RunOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mixed := good
	mixed.PointID = op.ID
	if _, err := e.Restore.Preview(ctx, mixed); !errors.Is(err, restore.ErrInvalidRequest) {
		t.Fatalf("mixed source/point = %v, want ErrInvalidRequest", err)
	}

	// A point whose verification failed is not restorable.
	if _, err := e.Pool.Exec(ctx, `UPDATE recovery_points SET verification = 'failed' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Restore.Preview(ctx, good); !errors.Is(err, restore.ErrNotRestorable) {
		t.Fatalf("failed point = %v, want ErrNotRestorable", err)
	}
}

func TestRestoreIsDisabledWithoutARoot(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	m, err := restore.New(restore.Config{Catalog: e.Cat, Repository: e.Repo, Pool: e.Pool, TokenKey: []byte("0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	if m.Enabled() {
		t.Fatal("a manager without a root must be disabled")
	}
	if _, err := m.Preview(ctx, req(e, p, "x", "a.txt")); !errors.Is(err, restore.ErrDisabled) {
		t.Fatalf("Preview = %v, want ErrDisabled", err)
	}
	if _, err := restore.New(restore.Config{Catalog: e.Cat, Repository: e.Repo, Root: e.Repo.Root(), TokenKey: []byte("0123456789abcdef")}); err == nil {
		t.Fatal("a restore root inside the repository must be rejected")
	}
	if _, err := restore.New(restore.Config{Catalog: e.Cat, TokenKey: []byte("short")}); err == nil {
		t.Fatal("a short token key must be rejected")
	}
}

func TestConfirmationTokenBindsUserAndPlan(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	r := req(e, p, "d", "a.txt")
	plan, err := e.Restore.Preview(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	tok := e.Restore.IssueToken(plan, "alice")

	if _, err := e.Restore.Confirm(ctx, r, tok, "alice"); err != nil {
		t.Fatalf("valid confirmation rejected: %v", err)
	}
	if _, err := e.Restore.Confirm(ctx, r, tok, "mallory"); !errors.Is(err, restore.ErrNotConfirmed) {
		t.Fatalf("another user = %v, want ErrNotConfirmed", err)
	}
	for _, bad := range []string{"", "garbage", tok + "x", strings.Replace(tok, ".", ".A", 1)} {
		if _, err := e.Restore.Confirm(ctx, r, bad, "alice"); !errors.Is(err, restore.ErrNotConfirmed) {
			t.Errorf("token %q = %v, want ErrNotConfirmed", bad, err)
		}
	}

	other := r
	other.Policy = restore.PolicyOverwrite
	if _, err := e.Restore.Confirm(ctx, other, tok, "alice"); !errors.Is(err, restore.ErrStalePreview) {
		t.Fatalf("different request = %v, want ErrStalePreview", err)
	}

	// The destination changed after the preview: the shown plan is no longer true.
	if err := os.MkdirAll(filepath.Join(e.RestoreRoot, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.RestoreRoot, "d", "a.txt"), []byte("someone was faster"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Restore.Confirm(ctx, r, tok, "alice"); !errors.Is(err, restore.ErrStalePreview) {
		t.Fatalf("changed destination = %v, want ErrStalePreview", err)
	}

	e.Clock.T = e.Clock.T.Add(time.Hour)
	if _, err := e.Restore.Confirm(ctx, r, tok, "alice"); !errors.Is(err, restore.ErrStalePreview) {
		t.Fatalf("expired token = %v, want ErrStalePreview", err)
	}
}

func TestHostileCatalogNamesCannotEscapeTheRestoreRoot(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()
	a, _ := e.Cat.ResolveAt(ctx, e.Src.ID, p.Seq, "a.txt")
	root, err := e.Cat.RootNode(ctx, e.Src.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a tampered catalog with names a file system would never produce.
	for _, name := range []string{"..", "evil:stream", `back\slash`, "CON"} {
		var nodeID int64
		err := e.Pool.QueryRow(ctx,
			`INSERT INTO nodes (source_id, parent_id, name, name_key) VALUES ($1, $2, $3, lower($3)) RETURNING id`,
			e.Src.ID, root, name).Scan(&nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx,
			`INSERT INTO node_versions (node_id, source_id, first_point, is_dir, state, size, mtime_ns, sha256)
			 VALUES ($1, $2, 1, false, 'ok', $3, 0, $4)`, nodeID, e.Src.ID, a.Size, a.SHA256); err != nil {
			t.Fatal(err)
		}
	}
	r := req(e, p, "r1", "")
	plan, err := e.Restore.Preview(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Totals.Blocked != 4 {
		t.Fatalf("blocked = %d, want 4 unsafe names: %+v", plan.Totals.Blocked, plan.Problems)
	}
	r.AllowPartial = true
	res, err := e.Restore.Execute(ctx, newJob(t, e), r, nil)
	if err == nil || res.Failed != 4 {
		t.Fatalf("res %+v err %v", res, err)
	}
	parent := filepath.Dir(e.RestoreRoot)
	entries, _ := os.ReadDir(parent)
	for _, en := range entries {
		if en.Name() != filepath.Base(e.RestoreRoot) && en.Name() != "repo" && en.Name() != "source" {
			t.Errorf("unexpected file outside the restore root: %s", en.Name())
		}
	}
	if got := e.ReadRestored("r1/Team Files/a.txt"); got != "alpha" {
		t.Fatalf("the safe files must still be restored, got %q", got)
	}
}
