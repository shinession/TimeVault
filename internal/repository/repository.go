// Package repository is TimeVault's content store: immutable, content-addressed
// objects (SHA-256 of the whole file) on storage that is separate from the
// backed-up sources.
//
// An object is first streamed into the repository's tmp directory while being
// hashed, flushed to disk and only then renamed to its final name, so a crash
// never leaves a partially written object under objects/.
package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"timevault/internal/platform"
)

const (
	formatVersion = 1
	markerFile    = "timevault-repository.json"
	objectsDir    = "objects"
	tmpDir        = "tmp"
	partSuffix    = ".part"
	copyBufSize   = 1 << 20
)

var (
	// ErrMissing means the object is not in the repository.
	ErrMissing = errors.New("repository: object missing")
	// ErrCorrupt means the stored bytes do not match the object's hash or size.
	ErrCorrupt = errors.New("repository: object corrupt")
	// ErrNotRepository means the directory exists, is not empty and is not a TimeVault repository.
	ErrNotRepository = errors.New("repository: directory is not a TimeVault repository")
)

// Hash is the SHA-256 of an object's content.
type Hash [sha256.Size]byte

// String returns the lower-case hex form.
func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// ParseHash parses the hex form produced by Hash.String.
func ParseHash(s string) (Hash, error) {
	var h Hash
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		return h, fmt.Errorf("repository: invalid hash %q", s)
	}
	copy(h[:], b)
	return h, nil
}

// HashFromBytes converts a raw 32-byte digest (for example a database column).
func HashFromBytes(b []byte) (Hash, error) {
	var h Hash
	if len(b) != len(h) {
		return h, fmt.Errorf("repository: digest has %d bytes, want %d", len(b), len(h))
	}
	copy(h[:], b)
	return h, nil
}

// Info describes a stored object.
type Info struct {
	Hash Hash
	Size int64
}

// Health is a point-in-time status of the repository directory.
type Health struct {
	Path       string
	OK         bool
	Detail     string
	FreeBytes  uint64
	TotalBytes uint64
}

type marker struct {
	Format    int       `json:"format"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is an opened repository. It is safe for concurrent use.
type Store struct {
	root string
	id   string
	// publish serializes "does the object exist? then rename" per hash prefix so that
	// concurrent writers of identical content agree on who created the object.
	publish [256]sync.Mutex

	// dirty holds the object folders that received a new entry since the last Barrier.
	dirtyMu sync.Mutex
	dirty   map[string]struct{}
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, copyBufSize); return &b }}

// Open opens the repository at root, creating and initializing it when the
// directory is missing or empty. A non-empty directory without a repository
// marker is refused so TimeVault never fills a folder it does not own.
func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("repository: path is empty")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("repository: resolve path: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("repository: create directory: %w", err)
	}
	m, err := readMarker(root)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		empty, emptyErr := isEmptyDir(root)
		if emptyErr != nil {
			return nil, fmt.Errorf("repository: inspect directory: %w", emptyErr)
		}
		if !empty {
			return nil, fmt.Errorf("%w: %s", ErrNotRepository, root)
		}
		if m, err = writeMarker(root); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	for _, d := range []string{objectsDir, tmpDir} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, fmt.Errorf("repository: create %s: %w", d, err)
		}
	}
	return &Store{root: root, id: m.ID}, nil
}

// Root is the absolute repository directory.
func (s *Store) Root() string { return s.root }

// ID is the repository's unique identifier.
func (s *Store) ID() string { return s.id }

func readMarker(root string) (marker, error) {
	var m marker
	b, err := os.ReadFile(filepath.Join(root, markerFile))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("repository: read marker: %w", err)
	}
	if m.Format != formatVersion {
		return m, fmt.Errorf("repository: unsupported format version %d", m.Format)
	}
	return m, nil
}

func writeMarker(root string) (marker, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return marker{}, err
	}
	m := marker{Format: formatVersion, ID: hex.EncodeToString(id), CreatedAt: time.Now().UTC()}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	tmp := filepath.Join(root, markerFile+partSuffix)
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return m, fmt.Errorf("repository: write marker: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(root, markerFile)); err != nil {
		return m, fmt.Errorf("repository: publish marker: %w", err)
	}
	return m, nil
}

func isEmptyDir(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return len(names) == 0, err
}

// ObjectPath is where the object with hash h is (or would be) stored.
func (s *Store) ObjectPath(h Hash) string {
	hexed := h.String()
	return filepath.Join(s.root, objectsDir, hexed[0:2], hexed[2:4], hexed)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

// SourceError wraps an error returned by the reader given to Put, so callers can
// tell a failing source file (skip it) from a failing repository (stop).
type SourceError struct{ Err error }

func (e *SourceError) Error() string { return "read source: " + e.Err.Error() }
func (e *SourceError) Unwrap() error { return e.Err }

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = &SourceError{Err: err}
	}
	return n, err
}

func newTempName() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + partSuffix, nil
}

// Put stores everything read from r and returns its hash and size. created is
// false when an identical object already existed (deduplication).
func (s *Store) Put(ctx context.Context, r io.Reader) (info Info, created bool, err error) {
	name, err := newTempName()
	if err != nil {
		return info, false, err
	}
	tmpPath := filepath.Join(s.root, tmpDir, name)
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return info, false, fmt.Errorf("repository: create temp object: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	h := sha256.New()
	buf := bufPool.Get().(*[]byte)
	n, copyErr := io.CopyBuffer(io.MultiWriter(tmp, h), &ctxReader{ctx: ctx, r: r}, *buf)
	bufPool.Put(buf)
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return info, false, fmt.Errorf("repository: write object: %w", copyErr)
	}

	copy(info.Hash[:], h.Sum(nil))
	info.Size = n
	final := s.ObjectPath(info.Hash)

	lock := &s.publish[info.Hash[0]]
	lock.Lock()
	defer lock.Unlock()

	if st, statErr := os.Stat(final); statErr == nil {
		if st.Size() == n {
			return info, false, nil
		}
		// A truncated or damaged object under this name is replaced by the good copy.
		_ = os.Chmod(final, 0o666)
		_ = os.Remove(final)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return info, false, fmt.Errorf("repository: create object directory: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		if st, statErr := os.Stat(final); statErr == nil && st.Size() == n {
			return info, false, nil
		}
		return info, false, fmt.Errorf("repository: publish object: %w", err)
	}
	published = true
	_ = os.Chmod(final, 0o444)
	s.markDirty(filepath.Dir(final))
	return info, true, nil
}

func (s *Store) markDirty(dir string) {
	s.dirtyMu.Lock()
	if s.dirty == nil {
		s.dirty = make(map[string]struct{})
	}
	s.dirty[dir] = struct{}{}
	s.dirtyMu.Unlock()
}

// Barrier flushes the folders that received objects since the previous call, so
// that those objects survive a power failure and not only the file contents,
// which Put already flushes. Call it before the catalog starts to refer to them.
// When it fails the folders stay pending for the next call.
func (s *Store) Barrier() error {
	s.dirtyMu.Lock()
	pending := s.dirty
	s.dirty = nil
	s.dirtyMu.Unlock()

	// A new object folder also adds an entry to its parents.
	targets := make(map[string]struct{}, len(pending)*2)
	objects := filepath.Join(s.root, objectsDir)
	for dir := range pending {
		for d := dir; ; d = filepath.Dir(d) {
			targets[d] = struct{}{}
			if d == objects || len(d) <= len(objects) {
				break
			}
		}
	}

	var first error
	failed := 0
	for dir := range targets {
		if err := platform.SyncDir(dir); err != nil {
			failed++
			if first == nil {
				first = err
			}
			s.markDirty(dir)
		}
	}
	if first != nil {
		return fmt.Errorf("repository: flush %d folder(s): %w", failed, first)
	}
	return nil
}

// Open returns the stored object for reading. The caller closes it.
func (s *Store) Open(h Hash) (*os.File, error) {
	f, err := os.Open(s.ObjectPath(h))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrMissing, h)
	}
	return f, err
}

// Stat returns the stored size of an object without reading it.
func (s *Store) Stat(h Hash) (int64, error) {
	st, err := os.Stat(s.ObjectPath(h))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("%w: %s", ErrMissing, h)
	}
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// Verify re-reads an object and checks its hash and, when size >= 0, its size.
func (s *Store) Verify(ctx context.Context, h Hash, size int64) error {
	f, err := s.Open(h)
	if err != nil {
		return err
	}
	defer f.Close()
	hasher := sha256.New()
	buf := bufPool.Get().(*[]byte)
	n, err := io.CopyBuffer(hasher, &ctxReader{ctx: ctx, r: f}, *buf)
	bufPool.Put(buf)
	if err != nil {
		return fmt.Errorf("repository: read %s: %w", h, err)
	}
	if size >= 0 && n != size {
		return fmt.Errorf("%w: %s has %d bytes, expected %d", ErrCorrupt, h, n, size)
	}
	var got Hash
	copy(got[:], hasher.Sum(nil))
	if got != h {
		return fmt.Errorf("%w: %s content hashes to %s", ErrCorrupt, h, got)
	}
	return nil
}

// Delete removes an object from the repository. An object that is not there counts as deleted, so an
// interrupted delete can simply be repeated. The caller must know that nothing refers to the object any
// more and that no backup can be storing the same content at the moment (see backup.Engine.SweepObjects).
func (s *Store) Delete(h Hash) error {
	path := s.ObjectPath(h)
	lock := &s.publish[h[0]]
	lock.Lock()
	defer lock.Unlock()

	// Put leaves objects read-only and Windows refuses to delete such a file. Any other failure shows up in Remove.
	if err := os.Chmod(path, 0o666); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Chmod(path, 0o444) // an object that could not be deleted stays as Put made it
		return fmt.Errorf("repository: delete object %s: %w", h, err)
	}
	return nil
}

// CleanTmp removes temporary objects older than maxAge that a crashed run left behind.
func (s *Store) CleanTmp(maxAge time.Duration) (int, error) {
	dir := filepath.Join(s.root, tmpDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), partSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}

// Health checks that the repository can be written and reports free space.
func (s *Store) Health() Health {
	h := Health{Path: s.root}
	name, err := newTempName()
	if err != nil {
		h.Detail = err.Error()
		return h
	}
	probe := filepath.Join(s.root, tmpDir, "health-"+name)
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		h.Detail = "repository is not writable: " + err.Error()
		return h
	}
	_ = os.Remove(probe)
	h.OK = true
	if free, total, err := platform.DiskSpace(s.root); err == nil {
		h.FreeBytes, h.TotalBytes = free, total
	}
	return h
}
