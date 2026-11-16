// Package search defines the boundary between TimeVault and a current-file
// search engine (Everything in the MVP).
//
// The search engine only ever answers "what exists on disk right now". It is
// not a history store and not an authorization layer: callers must confine
// every query to a registered source root and re-check results themselves.
package search

import (
	"context"
	"errors"
	"time"
)

const (
	// DefaultLimit is used when a caller does not choose a page size.
	DefaultLimit = 50
	// MaxLimit is the largest page size a provider accepts.
	MaxLimit = 200
	// MaxTextLength is the longest raw user query (in bytes) a provider accepts.
	MaxTextLength = 256
	// MaxTerms is the largest number of search terms taken from a query.
	MaxTerms = 16
)

var (
	// ErrDisabled means no search engine is configured.
	ErrDisabled = errors.New("search: provider disabled")
	// ErrUnavailable means the engine is configured but did not answer correctly.
	ErrUnavailable = errors.New("search: engine unavailable")
	// ErrBadQuery means the caller supplied an invalid query, root, offset or limit.
	ErrBadQuery = errors.New("search: invalid query")
	// ErrNotIndexed means the engine does not know the folder at all, so it can say nothing about it.
	ErrNotIndexed = errors.New("search: folder is not in the index")
)

// Provider searches the files that currently exist under a root directory.
type Provider interface {
	// Name is a short, stable identifier such as "everything".
	Name() string
	// Search runs one bounded, root-confined search.
	Search(ctx context.Context, q Query) (Page, error)
	// Health reports whether the engine is reachable. It must be cheap and
	// bounded by the provider's own timeout.
	Health(ctx context.Context) Health
}

// FolderInfo is the size and modification time of one folder as the engine's index knows them.
type FolderInfo struct {
	Name    string
	Size    int64     // total size of everything inside, -1 when the index has none
	ModTime time.Time // zero when unknown
}

// FolderLister is implemented by providers that can list the folders directly inside a folder with the sizes
// and modification times they know. The answer describes the index, which can lag a little behind the disk.
type FolderLister interface {
	// Subfolders lists the folders directly inside dir, an absolute path, by name. It returns at most limit
	// folders (0 means the provider's maximum); more is true when the folder has further ones.
	Subfolders(ctx context.Context, dir string, limit int) (folders []FolderInfo, more bool, err error)
}

// ProbeResult is what the engine knows about a folder tree right now.
type ProbeResult struct {
	// Items is the number of files and folders the index holds below the root, plus the root itself.
	Items int64
	// Changed is true when something below the root was created, modified, renamed, moved or had its
	// attributes changed at or after the time asked about. A deletion does not show here, only in Items.
	Changed bool
}

// ChangeProber is implemented by providers that can tell cheaply whether a folder tree changed.
type ChangeProber interface {
	// Probe summarises the tree below root, an absolute path. A zero since asks only for the number of items; the
	// result then has no Changed. It returns ErrNotIndexed when the engine holds nothing about root, and
	// ErrUnavailable when the engine does not answer.
	Probe(ctx context.Context, root string, since time.Time) (ProbeResult, error)
}

// Query is one search request.
type Query struct {
	// Text is the raw, untrusted text typed by the user.
	Text string
	// Root is the absolute directory every result must stay inside.
	Root string
	// Offset is the zero-based index of the first result to return.
	Offset int
	// Limit is the page size, 1..MaxLimit.
	Limit int
}

// Result is one file or folder reported by the engine.
type Result struct {
	Name    string
	Path    string // absolute path including Name
	IsDir   bool
	Size    int64     // -1 when unknown (for example folders)
	ModTime time.Time // zero when unknown
}

// Page is one page of results.
type Page struct {
	Items []Result
	// Total is the number of matches the engine reported for the confined query.
	Total  int
	Offset int
	// NextOffset is the offset to request next, or -1 when there are no more results.
	NextOffset int
	// Dropped counts engine results discarded by the provider's own root re-check.
	Dropped int
}

// State is the coarse health of a provider.
type State string

const (
	StateOK       State = "ok"
	StateDegraded State = "degraded"
	StateDisabled State = "disabled"
)

// Health is a point-in-time provider status. Detail never contains secrets.
type Health struct {
	State     State
	Detail    string
	Latency   time.Duration
	CheckedAt time.Time
}
