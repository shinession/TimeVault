//go:build windows

package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"timevault/internal/catalog"
	"timevault/internal/repository"
)

// A process that has an object open without allowing it to be deleted (a restore, a virus scanner) makes the
// file undeletable. The sweep leaves that object alone and counts it, and takes it the next time.
func TestSweepSkipsAnObjectThatIsInUse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.leaveUnreferenced("a.txt", "v1", "v2", "v3") // v1 and v2 are unreferenced
	busy, err := repository.ParseHash(contentHash("v1"))
	if err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(e.repo.ObjectPath(busy))
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	open := true
	defer func() {
		if open {
			_ = syscall.CloseHandle(h)
		}
	}()

	rec := &recorder{}
	st, err := e.eng.SweepObjects(ctx, rec)
	if err != nil || st.Objects != 1 || st.Failed != 1 || st.More {
		t.Fatalf("SweepObjects = %+v, %v; want v2 removed and v1 counted as failed", st, err)
	}
	if !rec.has("warn: repository: delete object "+busy.String()) || !rec.has("1 object(s) could not be deleted") {
		t.Fatalf("events = %v", rec.events)
	}
	eq(t, "object files", e.objectFiles(), hashes("v1", "v3"))
	eq(t, "object records", e.objectRecords(), hashes("v1", "v3"))
	if info, err := os.Stat(e.repo.ObjectPath(busy)); err != nil || info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("an object that could not be deleted must stay read-only: %v, %v", info, err)
	}

	if err := syscall.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	open = false
	st, err = e.eng.SweepObjects(ctx, nil)
	if err != nil || st.Objects != 1 || st.Failed != 0 {
		t.Fatalf("the next SweepObjects = %+v, %v", st, err)
	}
	eq(t, "object files", e.objectFiles(), hashes("v3"))
	eq(t, "object records", e.objectRecords(), hashes("v3"))
}

func TestLockedFileIsSkippedThenCapturedWhenReleased(t *testing.T) {
	e := newEnv(t)
	e.write("open-in-another-app.docx", "locked content")
	e.write("fine.txt", "fine")

	name, err := syscall.UTF16PtrFromString(filepath.Join(e.srcDir, "open-in-another-app.docx"))
	if err != nil {
		t.Fatal(err)
	}
	// No sharing at all: any other open of the file fails with a sharing violation.
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = syscall.CloseHandle(h)
		}
	}()

	rec := &recorder{}
	p, err := e.eng.Run(context.Background(), e.src.ID, RunOptions{}, rec)
	if err != nil {
		t.Fatalf("a locked file must not fail the backup: %v", err)
	}
	if p.SkippedCount != 1 || !rec.has("locked by another process") {
		t.Fatalf("skipped = %d, events = %v", p.SkippedCount, rec.events)
	}
	locked := e.entry(p.Seq, "open-in-another-app.docx")
	if locked.State != catalog.VersionSkipped || locked.SHA256 != nil {
		t.Fatalf("locked entry = %+v", locked)
	}
	if got := e.content(e.entry(p.Seq, "fine.txt")); got != "fine" {
		t.Fatalf("fine.txt = %q", got)
	}

	if err := syscall.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	closed = true
	next := e.backup()
	if got := e.content(e.entry(next.Seq, "open-in-another-app.docx")); got != "locked content" {
		t.Fatalf("after the lock was released the file holds %q", got)
	}
	e.assertManifest(next)
}

func TestJunctionIsRecordedButNeverFollowed(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("must not be backed up"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.srcDir, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	e.write("real.txt", "real")

	p := e.backup()
	eq(t, "root", e.names(p.Seq, ""), []string{"link", "real.txt"})
	en := e.entry(p.Seq, "link")
	if en.IsDir || en.State != catalog.VersionSkipped || !strings.Contains(en.SkipReason, "reparse point") {
		t.Fatalf("junction entry = %+v", en)
	}
	if _, err := e.cat.ResolveAt(context.Background(), e.src.ID, p.Seq, "link/secret.txt"); err == nil {
		t.Fatal("the content behind a junction must not be recorded")
	}
	if p.SkippedCount != 1 {
		t.Fatalf("skipped = %d", p.SkippedCount)
	}
	e.assertManifest(p)

	// A junction that stays the same is not a change.
	second := e.backup()
	if second.AddedCount+second.ModifiedCount+second.DeletedCount != 0 {
		t.Fatalf("unchanged junction produced changes: %+v", second)
	}
}

// A folder that is only a junction to the repository holds the repository's
// files; accepting it as a source would back the repository up into itself.
func TestSourceThatIsAJunctionToTheRepositoryIsRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := t.TempDir()

	direct := filepath.Join(base, "to-repo")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", direct, e.repo.Root()).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(direct) })
	if _, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "via junction", RootPath: direct}, "tester"); err == nil {
		t.Fatal("a junction to the repository was accepted as a source")
	}
	// A folder that does not exist yet below the junction would be created inside the repository.
	if _, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "below junction", RootPath: filepath.Join(direct, "objects")}, "tester"); err == nil {
		t.Fatal("a folder inside the repository, reached through a junction, was accepted as a source")
	}

	// A source that was fine when it was registered can be swapped for a junction later:
	// the backup itself must refuse to run.
	hijack := filepath.Join(base, "hijack")
	if err := os.MkdirAll(hijack, 0o755); err != nil {
		t.Fatal(err)
	}
	req, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "swapped", RootPath: hijack}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	src, err := e.cat.CreateSource(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hijack); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", hijack, e.repo.Root()).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(hijack) })
	if _, err := e.eng.Run(ctx, src.ID, RunOptions{}, nil); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("a backup whose source became a junction to the repository ran: %v", err)
	}
}
