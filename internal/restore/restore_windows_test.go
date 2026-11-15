//go:build windows

package restore_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"timevault/internal/restore"
	"timevault/internal/testenv"
)

func TestRestoreDoesNotFollowJunctionsInsideTheDestination(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()

	outside := t.TempDir()
	dest := filepath.Join(e.RestoreRoot, "d")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dest, "docs")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	r := req(e, p, "d", "docs")
	r.Policy = restore.PolicyOverwrite
	r.AllowPartial = true
	_, _, _ = run(t, e, r) // success or failure is fine; escaping is not

	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the restore wrote %d item(s) through a junction into a folder outside the restore root", len(entries))
	}
}

// A destination that is itself a junction would send the whole restore to
// wherever the junction points, for example into a source or the repository.
func TestRestoreRefusesADestinationThatIsAJunction(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()

	elsewhere := t.TempDir()
	link := filepath.Join(e.RestoreRoot, "d")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, elsewhere).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	r := req(e, p, "d", "docs")
	r.Policy = restore.PolicyOverwrite
	if _, err := e.Restore.Preview(ctx, r); !errors.Is(err, restore.ErrInvalidRequest) {
		t.Fatalf("Preview into a junction = %v, want ErrInvalidRequest", err)
	}
	if _, err := e.Restore.Execute(ctx, newJob(t, e), r, nil); !errors.Is(err, restore.ErrInvalidRequest) {
		t.Fatalf("Execute into a junction = %v, want ErrInvalidRequest", err)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("the junction target received %d item(s) (%v)", len(entries), err)
	}
}

// A restore folder that is a junction to a source is refused when the restore starts.
func TestRestoreRootThroughAJunctionIsComparedByItsRealLocation(t *testing.T) {
	e := testenv.New(t)
	seed(e)
	p := e.Backup()

	holder := t.TempDir()
	link := filepath.Join(holder, "restores")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, e.SrcDir).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	m, err := restore.New(restore.Config{
		Catalog: e.Cat, Repository: e.Repo, Pool: e.Pool, Root: link,
		TokenKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Preview(ctx, req(e, p, "d", "docs")); err == nil || !strings.Contains(err.Error(), "overlaps the source") {
		t.Fatalf("a restore root that is a junction to a source was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.SrcDir, "d")); err == nil {
		t.Fatal("something was written into the source")
	}
}
