package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/audit"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/pathutil"
	"timevault/internal/restore"
)

// Roots are the folders of a server that must not be shown in texts to people who may not see the disk of that server:
// the folder of its repository, its restore folder, and the network path people reach the restore folder by.
type Roots struct {
	Repository, Restore, RestoreDisplay string
}

// Reader reads the catalog, the jobs and the audit trail of one server: of this one, through the pool of the service, or of
// another server of the group, through a read-only pool on the schema of that server (see internal/cluster). It never
// writes. Everything that changes something, and everything that needs the disk of the server, is the Service's.
//
// A Reader does not know who asks: what a visitor may see is decided by the HTTP layer (internal/server).
type Reader struct {
	pool  *pgxpool.Pool
	cat   *catalog.Store
	audit *audit.Logger
	// present tells whether the repository still has the content with a hash. It is nil for the reader of another server,
	// which has no repository to ask: what it says about content is what the catalog records, not that it was checked.
	present func(sha []byte) bool
	roots   func() Roots
}

// NewReader returns the Reader of another server's schema. pool must be a pool on that schema (see db.Options.ReadOnly);
// roots says where that server keeps its repository and restores, for hiding them in texts.
func NewReader(pool *pgxpool.Pool, roots func() Roots) *Reader {
	if roots == nil {
		roots = func() Roots { return Roots{} }
	}
	return &Reader{pool: pool, cat: catalog.NewStore(pool), audit: audit.New(pool, slog.New(slog.DiscardHandler)), roots: roots}
}

// ContentChecked reports whether the answers about the content of versions come from the repository (the reader of this
// server) or only from the catalog (the reader of another server).
func (r *Reader) ContentChecked() bool { return r.present != nil }

// AuditEvents lists audit events newest first.
func (r *Reader) AuditEvents(ctx context.Context, f audit.Filter) ([]audit.Event, error) {
	return r.audit.List(ctx, f)
}

// --- Sources -------------------------------------------------------------

// Sources lists the registered sources with their newest recovery point.
func (r *Reader) Sources(ctx context.Context) ([]catalog.SourceSummary, error) {
	return r.cat.ListSources(ctx)
}

// Source returns one source.
func (r *Reader) Source(ctx context.Context, id int64) (catalog.Source, error) {
	src, err := r.cat.GetSource(ctx, id)
	if errors.Is(err, catalog.ErrNotFound) {
		return src, ErrNotFound
	}
	return src, err
}

// --- Points and browsing ---------------------------------------------------

// Point returns a recovery point of a source and verifies it belongs to it.
func (r *Reader) Point(ctx context.Context, sourceID, pointID int64) (catalog.Point, error) {
	p, err := r.cat.GetPoint(ctx, pointID)
	if errors.Is(err, catalog.ErrNotFound) || (err == nil && p.SourceID != sourceID) {
		return catalog.Point{}, ErrNotFound
	}
	return p, err
}

// PointByID returns a recovery point of any source, for the page of a job that names it.
func (r *Reader) PointByID(ctx context.Context, pointID int64) (catalog.Point, error) {
	p, err := r.cat.GetPoint(ctx, pointID)
	if errors.Is(err, catalog.ErrNotFound) {
		return catalog.Point{}, ErrNotFound
	}
	return p, err
}

// Points lists committed recovery points, newest first.
func (r *Reader) Points(ctx context.Context, sourceID int64, limit, beforeSeq int) ([]catalog.Point, error) {
	return r.cat.ListPoints(ctx, sourceID, limit, beforeSeq)
}

// Attempts lists backup attempts in any state, newest first.
func (r *Reader) Attempts(ctx context.Context, sourceID int64, limit int) ([]catalog.Point, error) {
	return r.cat.ListAttempts(ctx, sourceID, limit)
}

// LatestPoint returns the newest committed point, or ErrNotFound.
func (r *Reader) LatestPoint(ctx context.Context, sourceID int64) (catalog.Point, error) {
	p, err := r.cat.LatestCommitted(ctx, sourceID)
	if errors.Is(err, catalog.ErrNotFound) {
		return p, ErrNotFound
	}
	return p, err
}

// HistoryListing is one page of a folder at a recovery point.
type HistoryListing struct {
	Point   catalog.Point
	Latest  catalog.Point
	Rel     string
	Entries []catalog.Entry
	Next    string
}

// BrowseHistory lists a folder as it existed at a recovery point. Each entry
// carries its status relative to the newest point.
func (r *Reader) BrowseHistory(ctx context.Context, src catalog.Source, point catalog.Point, rel, cursor string, limit int) (HistoryListing, error) {
	rel, err := pathutil.CleanRel(rel)
	if err != nil {
		return HistoryListing{}, err
	}
	latest, err := r.cat.LatestCommitted(ctx, src.ID)
	if err != nil {
		return HistoryListing{}, ErrNotFound
	}
	dir, err := r.cat.ResolveAt(ctx, src.ID, point.Seq, rel)
	if errors.Is(err, catalog.ErrNotFound) {
		return HistoryListing{}, ErrNotFound
	}
	if err != nil {
		return HistoryListing{}, err
	}
	if !dir.IsDir {
		return HistoryListing{}, ErrNotFound
	}
	params := catalog.ListParams{Parent: dir.NodeID, Seq: point.Seq, LatestSeq: latest.Seq, Limit: limit}
	if cursor != "" {
		c, err := catalog.DecodeCursor(cursor)
		if err != nil {
			return HistoryListing{}, err
		}
		params.After = &c
	}
	page, err := r.cat.ListChildren(ctx, params)
	if err != nil {
		return HistoryListing{}, err
	}
	out := HistoryListing{Point: point, Latest: latest, Rel: rel, Entries: page.Entries}
	if page.Next != nil {
		out.Next = page.Next.Encode()
	}
	return out, nil
}

// FileHistory is every committed version of one path.
type FileHistory struct {
	Rel      string
	Name     string
	Versions []catalog.VersionInfo
	Latest   catalog.Point
	// Checked says that Present comes from the repository. It is false for a folder of another server, whose versions are
	// only recorded in the catalog: nobody has looked at its repository.
	Checked bool
	// Present maps a content hash (hex) to whether the repository still has it; empty when Checked is false.
	Present map[string]bool
}

// FileVersions lists the versions of a path across all recovery points.
func (r *Reader) FileVersions(ctx context.Context, src catalog.Source, rel string) (FileHistory, error) {
	rel, err := pathutil.CleanRel(rel)
	if err != nil {
		return FileHistory{}, err
	}
	latest, err := r.cat.LatestCommitted(ctx, src.ID)
	if err != nil {
		return FileHistory{}, ErrNotFound
	}
	node, err := r.cat.ResolveNode(ctx, src.ID, rel)
	if errors.Is(err, catalog.ErrNotFound) {
		return FileHistory{}, ErrNotFound
	}
	if err != nil {
		return FileHistory{}, err
	}
	versions, err := r.cat.Versions(ctx, node, latest.Seq)
	if err != nil {
		return FileHistory{}, err
	}
	if len(versions) == 0 {
		return FileHistory{}, ErrNotFound
	}
	h := FileHistory{Rel: rel, Name: pathutil.BaseRel(rel), Versions: versions, Latest: latest, Checked: r.present != nil, Present: map[string]bool{}}
	if rel == "" {
		h.Name = src.Name
	}
	if r.present == nil {
		return h, nil
	}
	for _, v := range versions {
		if len(v.SHA256) == 0 {
			continue
		}
		key := fmt.Sprintf("%x", v.SHA256)
		if _, done := h.Present[key]; done {
			continue
		}
		h.Present[key] = r.present(v.SHA256)
	}
	return h, nil
}

// --- Jobs --------------------------------------------------------------------

// Job returns one job.
func (r *Reader) Job(ctx context.Context, id int64) (jobs.Job, error) {
	j, err := jobs.Get(ctx, r.pool, id)
	if errors.Is(err, jobs.ErrNotFound) {
		return j, ErrNotFound
	}
	return j, err
}

// Jobs lists recent jobs, newest first, as the filter says.
func (r *Reader) Jobs(ctx context.Context, f jobs.Filter) ([]jobs.Job, error) {
	return jobs.List(ctx, r.pool, f)
}

// JobCounts counts the queued and the running jobs that the filter selects.
func (r *Reader) JobCounts(ctx context.Context, f jobs.Filter) (queued, running int, err error) {
	return jobs.Counts(ctx, r.pool, f)
}

// JobEvents returns a job's log lines after afterID.
func (r *Reader) JobEvents(ctx context.Context, jobID, afterID int64, limit int) ([]jobs.Event, error) {
	return jobs.Events(ctx, r.pool, jobID, afterID, limit)
}

// RestoreItems lists the stored per-item outcomes of a restore job.
func (r *Reader) RestoreItems(ctx context.Context, jobID int64, status string, afterID int64, limit int) ([]restore.StoredItem, error) {
	return restore.ListStoredItems(ctx, r.pool, jobID, status, afterID, limit)
}

// RestoreJobRequest decodes the request a restore job was started with.
func (r *Reader) RestoreJobRequest(job jobs.Job) (restore.Request, restore.Totals, bool) {
	var p restoreParams
	if job.Kind != jobs.KindRestore || json.Unmarshal(job.Params, &p) != nil {
		return restore.Request{}, restore.Totals{}, false
	}
	return p.Request, p.Totals, true
}

// --- Folders in texts ----------------------------------------------------------

// PathMap builds the mapper from the sources as they are now, one query. Build it per request instead of
// keeping it: a source may be added or get another display path at any time.
func (r *Reader) PathMap(ctx context.Context) (*PathMap, error) {
	return r.PathMapFor(ctx, nil)
}

// PathMapFor is PathMap for a visitor who may see only some of the monitored folders: the others are left out, so
// nothing of them (their names, their display folders) can come out of the mapper, and a text that quotes the real
// folder of one of them is left as it is. Such texts are not shown to that visitor: what belongs to a folder they
// may not see is not shown at all. keep says which folders are in; nil keeps them all.
func (r *Reader) PathMapFor(ctx context.Context, keep func(catalog.Source) bool) (*PathMap, error) {
	sources, err := r.cat.Sources(ctx)
	if err != nil {
		return nil, err
	}
	if keep != nil {
		kept := make([]catalog.Source, 0, len(sources))
		for _, src := range sources {
			if keep(src) {
				kept = append(kept, src)
			}
		}
		sources = kept
	}
	roots := r.roots()
	return NewPathMap(sources, roots.Repository, roots.Restore, roots.RestoreDisplay), nil
}
