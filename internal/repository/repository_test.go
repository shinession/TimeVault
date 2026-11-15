package repository

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestPutDeduplicatesAndReadsBack(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	content := []byte("time vault object")

	first, created, err := s.Put(ctx, bytes.NewReader(content))
	if err != nil || !created {
		t.Fatalf("first Put = %+v created=%v err=%v", first, created, err)
	}
	second, created, err := s.Put(ctx, bytes.NewReader(content))
	if err != nil || created {
		t.Fatalf("second Put created=%v err=%v, want dedup", created, err)
	}
	if first != second {
		t.Fatalf("hash/size differ: %+v vs %+v", first, second)
	}
	want := sha256.Sum256(content)
	if first.Hash != Hash(want) || first.Size != int64(len(content)) {
		t.Fatalf("info = %+v", first)
	}

	f, err := s.Open(first.Hash)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if !bytes.Equal(got, content) {
		t.Fatalf("read back %q", got)
	}
	if size, err := s.Stat(first.Hash); err != nil || size != int64(len(content)) {
		t.Fatalf("Stat = %d, %v", size, err)
	}
	if err := s.Verify(ctx, first.Hash, first.Size); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestPutEmptyAndLargeObjects(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	empty, _, err := s.Put(ctx, bytes.NewReader(nil))
	if err != nil || empty.Size != 0 {
		t.Fatalf("empty Put = %+v, %v", empty, err)
	}
	if err := s.Verify(ctx, empty.Hash, 0); err != nil {
		t.Fatalf("Verify empty: %v", err)
	}

	big := make([]byte, 5<<20+123)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	info, _, err := s.Put(ctx, bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	if info.Hash != Hash(sha256.Sum256(big)) || info.Size != int64(len(big)) {
		t.Fatalf("large info = %+v", info)
	}
}

func TestConcurrentPutOfSameContentCreatesOneObject(t *testing.T) {
	s := newStore(t)
	content := bytes.Repeat([]byte("x"), 1<<20)
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, c, err := s.Put(context.Background(), bytes.NewReader(content))
			if err != nil {
				t.Errorf("Put: %v", err)
				return
			}
			if c {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created %d times, want exactly 1", created.Load())
	}
	assertNoTemp(t, s)
}

func TestVerifyDetectsCorruptionAndMissing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	info, _, err := s.Put(ctx, bytes.NewReader([]byte("original content")))
	if err != nil {
		t.Fatal(err)
	}

	path := s.ObjectPath(info.Hash)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}

	// Same length, different bytes: only a full hash check can see it.
	if err := os.WriteFile(path, []byte("tampered content"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, info.Hash, info.Size); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify after same-size tamper = %v, want ErrCorrupt", err)
	}

	// A truncated object is detected by size and replaced when the content is written again.
	if err := os.WriteFile(path, []byte("short"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, info.Hash, info.Size); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify after truncation = %v, want ErrCorrupt", err)
	}
	if _, created, err := s.Put(ctx, bytes.NewReader([]byte("original content"))); err != nil || !created {
		t.Fatalf("repair Put created=%v err=%v", created, err)
	}
	if err := s.Verify(ctx, info.Hash, info.Size); err != nil {
		t.Fatalf("Verify after repair: %v", err)
	}

	var missing Hash
	missing[0] = 0xAB
	if err := s.Verify(ctx, missing, -1); !errors.Is(err, ErrMissing) {
		t.Fatalf("Verify missing = %v, want ErrMissing", err)
	}
	if _, err := s.Stat(missing); !errors.Is(err, ErrMissing) {
		t.Fatalf("Stat missing = %v, want ErrMissing", err)
	}
}

type cancelAfterReader struct {
	cancel context.CancelFunc
	left   int
}

func (c *cancelAfterReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		c.cancel()
		return 0, nil
	}
	n := min(len(p), c.left, 1024)
	c.left -= n
	return n, nil
}

func TestCancelledPutLeavesNothingBehind(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _, err := s.Put(ctx, &cancelAfterReader{cancel: cancel, left: 4096})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put = %v, want context.Canceled", err)
	}
	assertNoTemp(t, s)
	assertNoObjects(t, s)
}

func TestOpenRefusesForeignDirectoryAndReusesRepository(t *testing.T) {
	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(foreign); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Open(foreign) = %v, want ErrNotRepository", err)
	}

	dir := filepath.Join(t.TempDir(), "repo")
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == "" || first.ID() != second.ID() {
		t.Fatalf("repository ids %q and %q should match", first.ID(), second.ID())
	}
	if _, err := Open("  "); err == nil {
		t.Fatal("Open with a blank path must fail")
	}
}

func TestCleanTmpRemovesOnlyOldPartFiles(t *testing.T) {
	s := newStore(t)
	oldPart := filepath.Join(s.Root(), tmpDir, "old"+partSuffix)
	freshPart := filepath.Join(s.Root(), tmpDir, "fresh"+partSuffix)
	other := filepath.Join(s.Root(), tmpDir, "notes.txt")
	for _, p := range []string{oldPart, freshPart, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPart, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, past, past); err != nil {
		t.Fatal(err)
	}

	removed, err := s.CleanTmp(24 * time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("CleanTmp = %d, %v; want 1", removed, err)
	}
	for _, p := range []string{freshPart, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s should have been kept: %v", p, err)
		}
	}
}

func TestHealthAndHashParsing(t *testing.T) {
	s := newStore(t)
	if h := s.Health(); !h.OK || h.Path != s.Root() {
		t.Fatalf("Health = %+v", h)
	}
	sum := sha256.Sum256([]byte("abc"))
	parsed, err := ParseHash(Hash(sum).String())
	if err != nil || parsed != Hash(sum) {
		t.Fatalf("ParseHash round trip = %v, %v", parsed, err)
	}
	for _, bad := range []string{"", "zz", "abcd"} {
		if _, err := ParseHash(bad); err == nil {
			t.Errorf("ParseHash(%q) should fail", bad)
		}
	}
	if _, err := HashFromBytes([]byte{1, 2}); err == nil {
		t.Error("HashFromBytes with a short digest should fail")
	}
}

func TestBarrierFlushesNewObjectFoldersOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Barrier(); err != nil {
		t.Fatalf("Barrier on an untouched repository: %v", err)
	}
	for i := 0; i < 20; i++ {
		if _, _, err := s.Put(ctx, bytes.NewReader([]byte{byte(i), 'x'})); err != nil {
			t.Fatal(err)
		}
	}
	s.dirtyMu.Lock()
	pending := len(s.dirty)
	s.dirtyMu.Unlock()
	if pending == 0 {
		t.Fatal("new objects must leave their folders pending")
	}
	if err := s.Barrier(); err != nil {
		t.Fatalf("Barrier: %v", err)
	}
	s.dirtyMu.Lock()
	pending = len(s.dirty)
	s.dirtyMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d folders still pending after a successful Barrier", pending)
	}

	// A folder that cannot be flushed is reported and stays pending.
	if _, _, err := s.Put(ctx, bytes.NewReader([]byte("again"))); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(s.Root(), objectsDir)); err != nil {
		t.Fatal(err)
	}
	if err := s.Barrier(); err == nil {
		t.Fatal("Barrier must report a folder that cannot be flushed")
	}
	s.dirtyMu.Lock()
	pending = len(s.dirty)
	s.dirtyMu.Unlock()
	if pending == 0 {
		t.Fatal("a folder that failed to flush must stay pending")
	}
}

func assertNoTemp(t *testing.T, s *Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.Root(), tmpDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("tmp directory still has %d entries", len(entries))
	}
}

func assertNoObjects(t *testing.T, s *Store) {
	t.Helper()
	count := 0
	_ = filepath.WalkDir(filepath.Join(s.Root(), objectsDir), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			count++
		}
		return nil
	})
	if count != 0 {
		t.Fatalf("found %d unexpected objects", count)
	}
}
