package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"timevault/internal/pathutil"
)

// ErrSkipDir may be returned by a WalkFunc to leave a folder unvisited.
var ErrSkipDir = errors.New("catalog: skip directory")

// WalkFunc is called for every entry in pre-order; rel is the source-relative path.
type WalkFunc func(rel string, e Entry) error

// ListParams selects one page of a folder as it existed at a recovery point.
type ListParams struct {
	Parent int64
	// Seq is the committed sequence number to read at.
	Seq int
	// LatestSeq is the newest committed sequence number. When > 0 each entry is
	// compared with that point and reported as same, modified or deleted.
	LatestSeq int
	After     *Cursor
	Limit     int
}

const visibleAt = `v.first_point <= %s AND (v.last_point IS NULL OR v.last_point >= %s)`

func scanEntry(row pgx.Row, withNow bool, latest int) (Entry, error) {
	var e Entry
	var reason *string
	var attrs int32
	var currentID *int64
	var err error
	if withNow {
		err = row.Scan(&e.NodeID, &e.Name, &e.VersionID, &e.IsDir, &e.State, &reason, &e.Size, &e.MTimeNS, &attrs,
			&e.SHA256, &e.FirstPoint, &e.LastPoint, &currentID)
	} else {
		err = row.Scan(&e.NodeID, &e.Name, &e.VersionID, &e.IsDir, &e.State, &reason, &e.Size, &e.MTimeNS, &attrs,
			&e.SHA256, &e.FirstPoint, &e.LastPoint)
	}
	if err != nil {
		return e, err
	}
	if reason != nil {
		e.SkipReason = *reason
	}
	e.Attrs = uint32(attrs)
	if latest > 0 && e.LastPoint != nil && *e.LastPoint >= latest {
		// Closed at the newest committed point: only a build that has not committed yet does that.
		e.LastPoint = nil
	}
	if withNow && latest > 0 {
		switch {
		case currentID == nil:
			e.Now = NowDeleted
		case *currentID == e.VersionID:
			e.Now = NowSame
		default:
			e.Now = NowModified
		}
	}
	return e, nil
}

// childrenQuery builds the statement that reads the children of p.Parent as
// they were at p.Seq, ordered by case-insensitive name. p.After, when set,
// skips everything up to that cursor and limit > 0 caps the number of rows.
func childrenQuery(p ListParams, limit int) (query string, args []any) {
	withNow := p.LatestSeq > 0
	cols := `n.id, n.name, v.id, v.is_dir, v.state, v.skip_reason, v.size, v.mtime_ns, v.attrs, v.sha256, v.first_point, v.last_point`
	join := ``
	if withNow {
		cols += `, c.id`
		join = ` LEFT JOIN node_versions c ON c.node_id = n.id AND c.first_point <= $3 AND (c.last_point IS NULL OR c.last_point >= $3)`
	}
	query = `SELECT ` + cols + ` FROM nodes n
		JOIN node_versions v ON v.node_id = n.id AND v.first_point <= $2 AND (v.last_point IS NULL OR v.last_point >= $2)` + join + `
		WHERE n.parent_id = $1`
	args = []any{p.Parent, p.Seq, p.LatestSeq}
	if !withNow {
		args = args[:2]
	}
	if p.After != nil {
		args = append(args, p.After.Key, p.After.Name)
		query += fmt.Sprintf(` AND (n.name_key, n.name) > ($%d, $%d)`, len(args)-1, len(args))
	}
	query += ` ORDER BY n.name_key, n.name`
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(` LIMIT $%d`, len(args))
	}
	return query, args
}

func (s *Store) queryChildren(ctx context.Context, p ListParams, limit int) ([]Entry, error) {
	query, args := childrenQuery(p, limit)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list children: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Entry, error) { return scanEntry(r, p.LatestSeq > 0, p.LatestSeq) })
	if err != nil {
		return nil, fmt.Errorf("catalog: list children: %w", err)
	}
	return entries, nil
}

// ListChildren returns one page of a folder at a recovery point, ordered by
// case-insensitive name. Folders are not grouped first; the UI does that.
func (s *Store) ListChildren(ctx context.Context, p ListParams) (Listing, error) {
	if p.Limit <= 0 || p.Limit > 5000 {
		p.Limit = 500
	}
	entries, err := s.queryChildren(ctx, p, p.Limit+1)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Entries: entries}
	if len(entries) > p.Limit {
		out.Entries = entries[:p.Limit]
		last := out.Entries[len(out.Entries)-1]
		out.Next = &Cursor{Key: pathutil.NameKey(last.Name), Name: last.Name}
	}
	return out, nil
}

// ListAllChildren returns every child of a folder at a recovery point, ordered
// by case-insensitive name and carrying the status relative to the newest point
// when p.LatestSeq is set. p.Limit and p.After are ignored; callers that sort
// or filter a folder themselves need all of it.
func (s *Store) ListAllChildren(ctx context.Context, p ListParams) ([]Entry, error) {
	p.After = nil
	return s.queryChildren(ctx, p, 0)
}

// ChildrenAt returns every child of a folder at a recovery point keyed by exact name.
func (s *Store) ChildrenAt(ctx context.Context, parent int64, seq int) (map[string]Entry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT n.id, n.name, v.id, v.is_dir, v.state, v.skip_reason, v.size, v.mtime_ns, v.attrs, v.sha256, v.first_point, v.last_point
		 FROM nodes n
		 JOIN node_versions v ON v.node_id = n.id AND v.first_point <= $2 AND (v.last_point IS NULL OR v.last_point >= $2)
		 WHERE n.parent_id = $1`, parent, seq)
	if err != nil {
		return nil, fmt.Errorf("catalog: children: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Entry, error) { return scanEntry(r, false, 0) })
	if err != nil {
		return nil, fmt.Errorf("catalog: children: %w", err)
	}
	out := make(map[string]Entry, len(entries))
	for _, e := range entries {
		out[e.Name] = e
	}
	return out, nil
}

// ResolveAt finds the entry at a source-relative path as it existed at a
// recovery point. Names match case-insensitively; an exact match wins.
func (s *Store) ResolveAt(ctx context.Context, sourceID int64, seq int, rel string) (Entry, error) {
	parts, err := pathutil.SplitRel(rel)
	if err != nil {
		return Entry{}, err
	}
	root, err := s.RootNode(ctx, sourceID)
	if err != nil {
		return Entry{}, err
	}
	cur := Entry{NodeID: root, IsDir: true, State: VersionOK}
	for _, part := range parts {
		if !cur.IsDir {
			return Entry{}, ErrNotFound
		}
		next, err := scanEntry(s.pool.QueryRow(ctx,
			`SELECT n.id, n.name, v.id, v.is_dir, v.state, v.skip_reason, v.size, v.mtime_ns, v.attrs, v.sha256, v.first_point, v.last_point
			 FROM nodes n
			 JOIN node_versions v ON v.node_id = n.id AND v.first_point <= $3 AND (v.last_point IS NULL OR v.last_point >= $3)
			 WHERE n.parent_id = $1 AND n.name_key = $2
			 ORDER BY (n.name = $4) DESC, n.name LIMIT 1`,
			cur.NodeID, pathutil.NameKey(part), seq, part), false, 0)
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, ErrNotFound
		}
		if err != nil {
			return Entry{}, fmt.Errorf("catalog: resolve %q: %w", rel, err)
		}
		cur = next
	}
	return cur, nil
}

// ResolveNode finds the node of a path regardless of time, for version history.
// Names that differ only by letter case can have several nodes; one with
// committed history wins over an empty one a failed build left behind, and then
// the exact spelling wins.
func (s *Store) ResolveNode(ctx context.Context, sourceID int64, rel string) (int64, error) {
	parts, err := pathutil.SplitRel(rel)
	if err != nil {
		return 0, err
	}
	cur, err := s.RootNode(ctx, sourceID)
	if err != nil {
		return 0, err
	}
	var latest int
	if len(parts) > 0 {
		if err := s.pool.QueryRow(ctx,
			`SELECT coalesce(max(seq), 0) FROM recovery_points WHERE source_id = $1 AND state = 'committed'`,
			sourceID).Scan(&latest); err != nil {
			return 0, fmt.Errorf("catalog: resolve %q: %w", rel, err)
		}
	}
	for _, part := range parts {
		err := s.pool.QueryRow(ctx,
			`SELECT n.id FROM nodes n
			 WHERE n.parent_id = $1 AND n.name_key = $2
			 ORDER BY EXISTS (SELECT 1 FROM node_versions v WHERE v.node_id = n.id AND v.first_point <= $4) DESC,
			          (n.name = $3) DESC, n.name
			 LIMIT 1`,
			cur, pathutil.NameKey(part), part, latest).Scan(&cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, fmt.Errorf("catalog: resolve %q: %w", rel, err)
		}
	}
	return cur, nil
}

// Versions lists every committed state a path went through, newest first.
// latestSeq is the newest committed sequence number; a version whose last point
// is that sequence is still current.
func (s *Store) Versions(ctx context.Context, nodeID int64, latestSeq int) ([]VersionInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT n.id, n.name, v.id, v.is_dir, v.state, v.skip_reason, v.size, v.mtime_ns, v.attrs, v.sha256,
		        v.first_point, v.last_point, fp.id, fp.captured_at, lp.captured_at
		 FROM node_versions v
		 JOIN nodes n ON n.id = v.node_id
		 JOIN recovery_points fp ON fp.source_id = v.source_id AND fp.seq = v.first_point AND fp.state = 'committed'
		 LEFT JOIN recovery_points lp ON lp.source_id = v.source_id AND lp.seq = v.last_point AND lp.state = 'committed'
		 WHERE v.node_id = $1
		 ORDER BY v.first_point DESC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("catalog: versions: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (VersionInfo, error) {
		var vi VersionInfo
		var reason *string
		var attrs int32
		err := r.Scan(&vi.NodeID, &vi.Name, &vi.VersionID, &vi.IsDir, &vi.State, &reason, &vi.Size, &vi.MTimeNS, &attrs, &vi.SHA256,
			&vi.FirstPoint, &vi.LastPoint, &vi.FirstPointID, &vi.FirstAt, &vi.LastAt)
		if reason != nil {
			vi.SkipReason = *reason
		}
		vi.Attrs = uint32(attrs)
		vi.StillCurrent = vi.LastPoint == nil || *vi.LastPoint >= latestSeq
		if vi.StillCurrent {
			// A closure at the newest committed point was made by a build that has not
			// committed yet, so as far as readers can tell the version is still current.
			vi.LastPoint, vi.LastAt = nil, nil
		}
		return vi, err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: versions: %w", err)
	}
	return out, nil
}

// WalkAt visits everything below a folder at a recovery point in pre-order,
// children sorted by case-insensitive name. rel is the path of the folder.
func (s *Store) WalkAt(ctx context.Context, parent int64, seq int, rel string, fn WalkFunc) error {
	var after *Cursor
	for {
		page, err := s.ListChildren(ctx, ListParams{Parent: parent, Seq: seq, After: after, Limit: 1000})
		if err != nil {
			return err
		}
		for _, e := range page.Entries {
			childRel := pathutil.JoinRel(rel, e.Name)
			if err := fn(childRel, e); err != nil {
				if errors.Is(err, ErrSkipDir) {
					continue
				}
				return err
			}
			if e.IsDir {
				if err := s.WalkAt(ctx, e.NodeID, seq, childRel, fn); err != nil {
					return err
				}
			}
		}
		if page.Next == nil {
			return nil
		}
		after = page.Next
	}
}
