//go:build windows

package pathutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(link) })
}

// filepath.EvalSymlinks leaves a junction unresolved, which would let a folder
// that is a junction to the repository pass an overlap check.
func TestRealResolvesJunctions(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	makeJunction(t, link, target)

	wantTarget, err := Real(target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Real(link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, wantTarget) {
		t.Fatalf("Real(junction) = %q, want %q", got, wantTarget)
	}
	if !Overlaps(got, target) {
		t.Fatal("a junction and its target must overlap")
	}

	// A folder that does not exist yet below a junction is resolved through it.
	missing, err := Real(filepath.Join(link, "new", "deeper"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wantTarget, "new", "deeper"); !strings.EqualFold(missing, want) {
		t.Fatalf("Real(missing below junction) = %q, want %q", missing, want)
	}
}

func TestRealOrCleanFallsBackToTheLexicalPath(t *testing.T) {
	// A name Windows refuses is neither "missing" nor resolvable.
	bad := filepath.Join(t.TempDir(), "bad|name")
	if _, err := Real(bad); err == nil {
		t.Skip("this Windows version accepts the name; nothing to fall back from")
	}
	if got := RealOrClean(bad); !strings.EqualFold(got, bad) {
		t.Fatalf("RealOrClean = %q, want %q", got, bad)
	}
}
