// Package catalog is TimeVault's historian: sources, recovery points and the
// per-path version history stored in PostgreSQL.
//
// Every read is made "as of" a committed recovery point sequence number, so a
// recovery point that is still being built, or that failed, is never visible.
package catalog

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrNotFound means the source, point or path does not exist (at that point).
	ErrNotFound = errors.New("catalog: not found")
	// ErrBusy means another backup of the same source is running.
	ErrBusy = errors.New("catalog: source is busy")
	// ErrConflict means a unique constraint (source name or root) was violated.
	ErrConflict = errors.New("catalog: already exists")
)

// Point states.
const (
	StateBuilding  = "building"
	StateCommitted = "committed"
	StateFailed    = "failed"
	StateAborted   = "aborted"
)

// Verification states of a recovery point.
const (
	VerificationPending   = "pending"
	VerificationValidated = "validated"
	VerificationVerified  = "verified"
	VerificationFailed    = "failed"
)

// Consistency modes of a recovery point.
const (
	ConsistencyLiveScan = "live_scan"
	ConsistencyVSS      = "vss"
)

// Version states.
const (
	VersionOK      = "ok"
	VersionSkipped = "skipped"
)

// Source is a registered directory tree that TimeVault backs up.
type Source struct {
	ID        int64
	Name      string
	RootPath  string
	Excludes  []string
	Enabled   bool
	CreatedBy string
	CreatedAt time.Time
}

// SourceSummary is a source with its newest committed recovery point.
type SourceSummary struct {
	Source
	Latest *Point
}

// Point is one recovery point.
type Point struct {
	ID          int64
	SourceID    int64
	Seq         int
	Kind        string
	State       string
	Consistency string
	CapturedAt  time.Time
	CommittedAt *time.Time

	FileCount      int64
	DirCount       int64
	TotalBytes     int64
	AddedCount     int64
	ModifiedCount  int64
	DeletedCount   int64
	NewObjectCount int64
	NewObjectBytes int64
	SkippedCount   int64

	ManifestSHA256 []byte
	Verification   string
	VerifiedAt     *time.Time
	FailureReason  string
}

// Restorable reports whether files can be restored from the point.
func (p Point) Restorable() bool {
	return p.State == StateCommitted && p.Verification != VerificationFailed
}

// ManifestHex is the manifest digest in hex, or "" when there is none.
func (p Point) ManifestHex() string { return hex.EncodeToString(p.ManifestSHA256) }

// Stats are the totals recorded when a point is committed.
type Stats struct {
	FileCount      int64
	DirCount       int64
	TotalBytes     int64
	AddedCount     int64
	ModifiedCount  int64
	DeletedCount   int64
	NewObjectCount int64
	NewObjectBytes int64
	SkippedCount   int64
}

// NowStatus compares an entry of a historical listing with the newest point.
type NowStatus string

const (
	NowUnknown  NowStatus = ""
	NowSame     NowStatus = "same"
	NowModified NowStatus = "modified"
	NowDeleted  NowStatus = "deleted"
)

// Entry is one path as it existed at a recovery point.
type Entry struct {
	NodeID     int64
	VersionID  int64
	Name       string
	IsDir      bool
	State      string
	SkipReason string
	Size       int64
	MTimeNS    int64
	Attrs      uint32
	SHA256     []byte
	FirstPoint int
	LastPoint  *int
	Now        NowStatus
}

// ModTime is the file's modification time (zero for folders).
func (e Entry) ModTime() time.Time {
	if e.MTimeNS == 0 {
		return time.Time{}
	}
	return time.Unix(0, e.MTimeNS)
}

// Cursor is the keyset position after the last entry of a page.
type Cursor struct {
	Key  string `json:"k"`
	Name string `json:"n"`
}

// Encode returns an opaque, URL-safe token.
func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses a token produced by Encode.
func DecodeCursor(s string) (Cursor, error) {
	var c Cursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, errors.New("catalog: invalid cursor")
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, errors.New("catalog: invalid cursor")
	}
	return c, nil
}

// Listing is one page of directory entries.
type Listing struct {
	Entries []Entry
	// Next is nil when the page is the last one.
	Next *Cursor
}

// VersionInfo is one state a path went through, with the points that bound it.
type VersionInfo struct {
	Entry
	FirstPointID int64
	FirstAt      time.Time
	LastAt       *time.Time
	// StillCurrent is true while the version is the newest committed state of the path.
	StillCurrent bool
}
