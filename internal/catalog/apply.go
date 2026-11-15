package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"timevault/internal/pathutil"
)

// ChangeKind says what happened to one name in a folder since the previous point.
type ChangeKind int

const (
	ChangeAdd ChangeKind = iota + 1
	ChangeModify
	ChangeDelete
)

// Change is one difference between a folder on disk and its previous recorded state.
type Change struct {
	Kind ChangeKind
	Name string

	// New state (ChangeAdd, ChangeModify).
	IsDir      bool
	State      string
	SkipReason string
	Size       int64
	MTimeNS    int64
	Attrs      uint32
	SHA256     []byte

	// Previous state (ChangeModify, ChangeDelete).
	NodeID    int64
	VersionID int64
	WasDir    bool
}

// ObjectRef is a content object that must exist before versions can point at it.
type ObjectRef struct {
	SHA256 []byte
	Size   int64
}

// ApplyResult reports what ApplyDir wrote.
type ApplyResult struct {
	// NodeIDs maps every added or modified name to its node.
	NodeIDs map[string]int64
	// DescendantsClosed counts paths removed because a folder was deleted or replaced.
	DescendantsClosed int64
}

// ApplyDir records the changes of one folder for a point under construction.
// Old versions are closed at prevSeq (the last committed point) and new ones
// start at the point's own sequence number, so nothing becomes visible until
// the point is committed. Everything is written in one transaction.
func (s *Store) ApplyDir(ctx context.Context, p Point, prevSeq int, parent int64, changes []Change, objects []ObjectRef) (ApplyResult, error) {
	res := ApplyResult{NodeIDs: map[string]int64{}}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("catalog: apply: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if len(objects) > 0 {
		shas := make([][]byte, len(objects))
		sizes := make([]int64, len(objects))
		for i, o := range objects {
			shas[i], sizes[i] = o.SHA256, o.Size
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO objects (sha256, size) SELECT * FROM unnest($1::bytea[], $2::bigint[]) ON CONFLICT (sha256) DO NOTHING`,
			shas, sizes); err != nil {
			return res, fmt.Errorf("catalog: register objects: %w", err)
		}
	}

	var addNames, addKeys []string
	var closeIDs []int64
	for _, c := range changes {
		switch c.Kind {
		case ChangeAdd:
			addNames = append(addNames, c.Name)
			addKeys = append(addKeys, pathutil.NameKey(c.Name))
		case ChangeModify:
			res.NodeIDs[c.Name] = c.NodeID
			closeIDs = append(closeIDs, c.VersionID)
		case ChangeDelete:
			closeIDs = append(closeIDs, c.VersionID)
		}
	}

	if len(addNames) > 0 {
		rows, err := tx.Query(ctx,
			`INSERT INTO nodes (source_id, parent_id, name, name_key)
			 SELECT $1, $2, t.name, t.key FROM unnest($3::text[], $4::text[]) AS t(name, key)
			 ON CONFLICT (parent_id, name) WHERE parent_id IS NOT NULL DO UPDATE SET name = EXCLUDED.name
			 RETURNING id, name`, p.SourceID, parent, addNames, addKeys)
		if err != nil {
			return res, fmt.Errorf("catalog: add nodes: %w", err)
		}
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return res, err
			}
			res.NodeIDs[name] = id
		}
		if err := rows.Err(); err != nil {
			return res, fmt.Errorf("catalog: add nodes: %w", err)
		}
	}

	if len(closeIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE node_versions SET last_point = $2 WHERE id = ANY($1) AND last_point IS NULL`, closeIDs, prevSeq); err != nil {
			return res, fmt.Errorf("catalog: close versions: %w", err)
		}
	}

	for _, c := range changes {
		folderGone := c.WasDir && (c.Kind == ChangeDelete || (c.Kind == ChangeModify && !c.IsDir))
		if !folderGone {
			continue
		}
		tag, err := tx.Exec(ctx,
			`WITH RECURSIVE sub(id) AS (
				SELECT id FROM nodes WHERE parent_id = $1
				UNION ALL
				SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id
			)
			UPDATE node_versions SET last_point = $2
			WHERE node_id IN (SELECT id FROM sub) AND last_point IS NULL AND first_point <= $2`, c.NodeID, prevSeq)
		if err != nil {
			return res, fmt.Errorf("catalog: close folder contents: %w", err)
		}
		res.DescendantsClosed += tag.RowsAffected()
	}

	var rows [][]any
	for _, c := range changes {
		if c.Kind == ChangeDelete {
			continue
		}
		var reason any
		if c.SkipReason != "" {
			reason = c.SkipReason
		}
		var sha any
		if len(c.SHA256) > 0 {
			sha = c.SHA256
		}
		rows = append(rows, []any{res.NodeIDs[c.Name], p.SourceID, int32(p.Seq), c.IsDir, c.State, reason, c.Size, c.MTimeNS, int32(c.Attrs), sha})
	}
	if len(rows) > 0 {
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"node_versions"},
			[]string{"node_id", "source_id", "first_point", "is_dir", "state", "skip_reason", "size", "mtime_ns", "attrs", "sha256"},
			pgx.CopyFromRows(rows))
		if err != nil {
			return res, fmt.Errorf("catalog: write versions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("catalog: commit apply: %w", err)
	}
	return res, nil
}
