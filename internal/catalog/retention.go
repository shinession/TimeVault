package catalog

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	defaultPruneBatch = 500
	defaultObjectPage = 1000
)

// PruneOptions tunes PruneVersions.
type PruneOptions struct {
	// Max is the most versions of one file to keep. It must be at least 1.
	Max int
	// SinceSeq, when positive, limits the prune to the files that have a version starting at that recovery
	// point or later: the files a backup that just committed point SinceSeq can have pushed over the limit.
	// Zero looks at every file of the source.
	SinceSeq int
	// BatchFiles is how many files one transaction handles; 0 means 500.
	BatchFiles int
}

// PruneResult tells what PruneVersions deleted.
type PruneResult struct {
	// Files is the number of files that lost versions.
	Files int64
	// Versions is the number of version rows deleted.
	Versions int64
	// PointsMarked is the number of recovery points newly flagged partial.
	PointsMarked int
}

// PrunePreview tells what a prune with a given limit would delete.
type PrunePreview struct {
	Files    int64
	Versions int64
}

// pruneVictims is the start of the statements that decide which version rows a prune deletes; they share
// it so that a preview cannot disagree with the real thing. $1 is the source and $2 the limit. When byFiles
// is set only the files in $3 are looked at.
//
// A file keeps its newest $2 versions of state 'ok', newest meaning the latest first_point. Everything of the
// file that is older than the oldest of them goes too, skipped versions included, but only files with more
// than $2 such versions are touched. Folders never lose a version.
//
// Only committed history counts. A build that did not finish leaves versions that start after the newest
// committed point and closes versions at that point, which readers see as current; neither is counted or
// deleted. A version that is still open is never deleted either.
func pruneVictims(byFiles bool) string {
	files := ""
	if byFiles {
		files = ` AND v.node_id = ANY($3::bigint[])`
	}
	return `WITH latest AS (
		SELECT coalesce(max(seq), 0) AS seq FROM recovery_points WHERE source_id = $1 AND state = 'committed'
	), ranked AS (
		SELECT v.node_id, v.first_point, v.id,
		       row_number() OVER (PARTITION BY v.node_id ORDER BY v.first_point DESC, v.id DESC) AS rn,
		       count(*) OVER (PARTITION BY v.node_id) AS total
		FROM node_versions v
		WHERE v.source_id = $1 AND NOT v.is_dir AND v.state = 'ok' AND v.first_point <= (SELECT seq FROM latest)` + files + `
	), boundary AS (
		SELECT node_id, first_point, id FROM ranked WHERE rn = $2 AND total > $2
	), victims AS (
		SELECT d.id, d.node_id, d.first_point, d.last_point
		FROM node_versions d
		JOIN boundary b ON b.node_id = d.node_id
		WHERE d.source_id = $1 AND NOT d.is_dir
		  AND d.last_point IS NOT NULL AND d.last_point < (SELECT seq FROM latest)
		  AND (d.first_point, d.id) < (b.first_point, b.id)
	) `
}

var (
	pruneDeleteSQL = pruneVictims(true) + `
		DELETE FROM node_versions del USING victims v WHERE del.id = v.id
		RETURNING del.node_id, del.first_point, del.last_point`
	prunePreviewSQL = pruneVictims(false) + `SELECT count(DISTINCT node_id), count(*) FROM victims`
)

// Files that can have versions to prune. The statements only narrow the search; pruneVictims decides.
const (
	pruneOverLimitSQL = `
		SELECT node_id FROM node_versions
		WHERE source_id = $1 AND NOT is_dir AND state = 'ok'
		GROUP BY node_id HAVING count(*) > $2
		ORDER BY node_id`
	pruneRecentSQL = `
		SELECT c.node_id
		FROM (SELECT DISTINCT node_id FROM node_versions WHERE source_id = $1 AND NOT is_dir AND first_point >= $3) c
		WHERE (SELECT count(*) FROM node_versions v WHERE v.node_id = c.node_id AND NOT v.is_dir AND v.state = 'ok') > $2
		ORDER BY c.node_id`

	// A recovery point that a deleted version belonged to no longer shows the whole folder.
	pruneFlagPartialSQL = `
		UPDATE recovery_points p SET partial = true
		WHERE p.source_id = $1 AND p.state = 'committed' AND NOT p.partial
		  AND EXISTS (SELECT 1 FROM unnest($2::int[], $3::int[]) AS r(lo, hi) WHERE p.seq BETWEEN r.lo AND r.hi)`
)

// PruneVersions deletes the older versions of the files of a source until none has more than opts.Max
// versions, and flags the recovery points that lost versions as partial. The newest versions, and with them
// the whole history of the newest recovery point, are never touched.
//
// The lock is the caller's proof that it is the only writer of the source. Files are handled in batches,
// each in one transaction that also flags the recovery points, so a point is never left without its flag.
// An interrupted prune can be repeated. Content that no version needs any more is left in the repository
// until it is swept.
func (s *Store) PruneVersions(ctx context.Context, lock *SourceLock, opts PruneOptions) (PruneResult, error) {
	var res PruneResult
	if lock == nil {
		return res, errors.New("catalog: pruning needs the lock of the source")
	}
	if opts.Max < 1 {
		return res, fmt.Errorf("catalog: the version limit must be at least 1, got %d", opts.Max)
	}
	batch := opts.BatchFiles
	if batch < 1 {
		batch = defaultPruneBatch
	}
	sourceID := lock.sourceID

	var rows pgx.Rows
	var err error
	if opts.SinceSeq > 0 {
		rows, err = s.pool.Query(ctx, pruneRecentSQL, sourceID, opts.Max, opts.SinceSeq)
	} else {
		rows, err = s.pool.Query(ctx, pruneOverLimitSQL, sourceID, opts.Max)
	}
	if err != nil {
		return res, fmt.Errorf("catalog: find files to prune: %w", err)
	}
	files, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return res, fmt.Errorf("catalog: find files to prune: %w", err)
	}

	for start := 0; start < len(files); start += batch {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		done, err := s.pruneBatch(ctx, sourceID, opts.Max, files[start:min(start+batch, len(files))])
		if err != nil {
			return res, err
		}
		res.Files += done.Files
		res.Versions += done.Versions
		res.PointsMarked += done.PointsMarked
	}
	return res, nil
}

type span struct{ first, last int32 }

func (s *Store) pruneBatch(ctx context.Context, sourceID int64, keep int, files []int64) (PruneResult, error) {
	var res PruneResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("catalog: prune: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, pruneDeleteSQL, sourceID, keep, files)
	if err != nil {
		return res, fmt.Errorf("catalog: delete old versions: %w", err)
	}
	type deleted struct {
		node int64
		span
	}
	gone, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (deleted, error) {
		var d deleted
		err := r.Scan(&d.node, &d.first, &d.last)
		return d, err
	})
	if err != nil {
		return res, fmt.Errorf("catalog: delete old versions: %w", err)
	}
	if len(gone) == 0 {
		return res, nil
	}

	touched := make(map[int64]struct{})
	spans := make([]span, len(gone))
	for i, d := range gone {
		touched[d.node] = struct{}{}
		spans[i] = d.span
	}
	lo, hi := mergeSpans(spans)
	tag, err := tx.Exec(ctx, pruneFlagPartialSQL, sourceID, lo, hi)
	if err != nil {
		return res, fmt.Errorf("catalog: flag partial recovery points: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("catalog: commit prune: %w", err)
	}
	return PruneResult{Files: int64(len(touched)), Versions: int64(len(gone)), PointsMarked: int(tag.RowsAffected())}, nil
}

// mergeSpans unites ranges of sequence numbers into the fewest disjoint ones. Ranges that touch are united
// too, because sequence numbers are whole numbers.
func mergeSpans(spans []span) (lo, hi []int32) {
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.first, b.first) })
	for _, sp := range spans {
		if n := len(hi); n > 0 && sp.first <= hi[n-1]+1 {
			hi[n-1] = max(hi[n-1], sp.last)
			continue
		}
		lo = append(lo, sp.first)
		hi = append(hi, sp.last)
	}
	return lo, hi
}

// PreviewPrune tells how many files would lose versions, and how many version rows would be deleted, if
// the source were pruned to at most keep versions per file. It changes nothing and needs no lock.
func (s *Store) PreviewPrune(ctx context.Context, sourceID int64, keep int) (PrunePreview, error) {
	var p PrunePreview
	if keep < 1 {
		return p, fmt.Errorf("catalog: the version limit must be at least 1, got %d", keep)
	}
	if err := s.pool.QueryRow(ctx, prunePreviewSQL, sourceID, keep).Scan(&p.Files, &p.Versions); err != nil {
		return PrunePreview{}, fmt.Errorf("catalog: preview prune: %w", err)
	}
	return p, nil
}

// UnreferencedObjects lists objects that no version refers to, ordered by hash, starting strictly after
// the hash `after` (nil starts at the beginning). A limit below 1 means 1000.
func (s *Store) UnreferencedObjects(ctx context.Context, after []byte, limit int) ([]ObjectRef, error) {
	if limit < 1 {
		limit = defaultObjectPage
	}
	const unreferenced = `NOT EXISTS (SELECT 1 FROM node_versions v WHERE v.sha256 = o.sha256)`
	query, args := `SELECT o.sha256, o.size FROM objects o WHERE `+unreferenced+` ORDER BY o.sha256 LIMIT $1`, []any{limit}
	if len(after) > 0 {
		query = `SELECT o.sha256, o.size FROM objects o WHERE o.sha256 > $1 AND ` + unreferenced + ` ORDER BY o.sha256 LIMIT $2`
		args = []any{after, limit}
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list unreferenced objects: %w", err)
	}
	objects, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ObjectRef, error) {
		var o ObjectRef
		err := r.Scan(&o.SHA256, &o.Size)
		return o, err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: list unreferenced objects: %w", err)
	}
	return objects, nil
}

// ForgetObject deletes the record of an object, but only while no version refers to it. It reports whether
// the record was deleted: false means a version refers to the object, or there is no such record.
func (s *Store) ForgetObject(ctx context.Context, sha []byte) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM objects o WHERE o.sha256 = $1 AND NOT EXISTS (SELECT 1 FROM node_versions v WHERE v.sha256 = o.sha256)`, sha)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		// A version that was written since the check refers to it, and the foreign key refuses the delete.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: forget object: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
