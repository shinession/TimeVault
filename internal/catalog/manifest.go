package catalog

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
)

// ManifestEntry is the part of an entry that is covered by a point's manifest digest.
type ManifestEntry struct {
	IsDir   bool
	State   string
	Size    int64
	MTimeNS int64
	Attrs   uint32
	SHA256  []byte
}

// ManifestOf converts a catalog entry.
func ManifestOf(e Entry) ManifestEntry {
	return ManifestEntry{IsDir: e.IsDir, State: e.State, Size: e.Size, MTimeNS: e.MTimeNS, Attrs: e.Attrs, SHA256: e.SHA256}
}

// Manifest accumulates the digest of a recovery point's complete state: one
// canonical line per path, in pre-order with children sorted by (lower-case
// name, name). The backup engine feeds it while scanning and the verifier
// recomputes it from the catalog, so the two must agree.
type Manifest struct {
	h hash.Hash
}

// NewManifest starts an empty digest.
func NewManifest() *Manifest { return &Manifest{h: sha256.New()} }

// Add appends one path.
func (m *Manifest) Add(rel string, e ManifestEntry) {
	kind := byte('f')
	switch {
	case e.IsDir:
		kind = 'd'
		e.Size, e.MTimeNS, e.SHA256 = 0, 0, nil
	case e.State == VersionSkipped:
		kind = 's'
	}
	m.h.Write([]byte(rel))
	m.h.Write([]byte{0, kind, 0})
	fmt.Fprintf(m.h, "%d\x00%d\x00%d\x00%x\n", e.Size, e.MTimeNS, e.Attrs, e.SHA256)
}

// Sum returns the digest of everything added so far.
func (m *Manifest) Sum() []byte { return m.h.Sum(nil) }

// ComputeManifest recomputes the manifest digest of a committed point from the catalog.
func (s *Store) ComputeManifest(ctx context.Context, sourceID int64, seq int) ([]byte, error) {
	root, err := s.RootNode(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	m := NewManifest()
	err = s.WalkAt(ctx, root, seq, "", func(rel string, e Entry) error {
		m.Add(rel, ManifestOf(e))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.Sum(), nil
}
