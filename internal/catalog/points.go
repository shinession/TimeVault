package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pointColumns = `id, source_id, seq, kind, state, consistency, captured_at, committed_at,
	file_count, dir_count, total_bytes, added_count, modified_count, deleted_count,
	new_object_count, new_object_bytes, skipped_count, manifest_sha256, verification, verified_at, failure_reason`

func scanPoint(row pgx.Row) (Point, error) {
	var p Point
	var reason *string
	err := row.Scan(&p.ID, &p.SourceID, &p.Seq, &p.Kind, &p.State, &p.Consistency, &p.CapturedAt, &p.CommittedAt,
		&p.FileCount, &p.DirCount, &p.TotalBytes, &p.AddedCount, &p.ModifiedCount, &p.DeletedCount,
		&p.NewObjectCount, &p.NewObjectBytes, &p.SkippedCount, &p.ManifestSHA256, &p.Verification, &p.VerifiedAt, &reason)
	if reason != nil {
		p.FailureReason = *reason
	}
	return p, err
}

// GetPoint returns one recovery point in any state.
func (s *Store) GetPoint(ctx context.Context, id int64) (Point, error) {
	p, err := scanPoint(s.pool.QueryRow(ctx, `SELECT `+pointColumns+` FROM recovery_points WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Point{}, ErrNotFound
	}
	if err != nil {
		return Point{}, fmt.Errorf("catalog: get point: %w", err)
	}
	return p, nil
}

// LatestCommitted returns the newest committed point of a source.
func (s *Store) LatestCommitted(ctx context.Context, sourceID int64) (Point, error) {
	p, err := scanPoint(s.pool.QueryRow(ctx,
		`SELECT `+pointColumns+` FROM recovery_points
		 WHERE source_id = $1 AND state = 'committed' ORDER BY seq DESC LIMIT 1`, sourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Point{}, ErrNotFound
	}
	if err != nil {
		return Point{}, fmt.Errorf("catalog: latest point: %w", err)
	}
	return p, nil
}

// ListPoints returns committed points, newest first. beforeSeq > 0 continues after a previous page.
func (s *Store) ListPoints(ctx context.Context, sourceID int64, limit, beforeSeq int) ([]Point, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+pointColumns+` FROM recovery_points
		 WHERE source_id = $1 AND state = 'committed' AND ($3 = 0 OR seq < $3)
		 ORDER BY seq DESC LIMIT $2`, sourceID, limit, beforeSeq)
	if err != nil {
		return nil, fmt.Errorf("catalog: list points: %w", err)
	}
	pts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Point, error) { return scanPoint(r) })
	if err != nil {
		return nil, fmt.Errorf("catalog: list points: %w", err)
	}
	return pts, nil
}

// ListAttempts returns the newest backup attempts in any state, newest first.
func (s *Store) ListAttempts(ctx context.Context, sourceID int64, limit int) ([]Point, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+pointColumns+` FROM recovery_points WHERE source_id = $1 ORDER BY seq DESC LIMIT $2`, sourceID, limit)
	if err != nil {
		return nil, fmt.Errorf("catalog: list attempts: %w", err)
	}
	pts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Point, error) { return scanPoint(r) })
	if err != nil {
		return nil, fmt.Errorf("catalog: list attempts: %w", err)
	}
	return pts, nil
}

// SetVerification records the outcome of an integrity check on a committed point.
func (s *Store) SetVerification(ctx context.Context, pointID int64, status string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE recovery_points
		 SET verification = $2,
		     verified_at = CASE WHEN $2 IN ('verified', 'failed') THEN now() ELSE verified_at END
		 WHERE id = $1 AND state = 'committed'`, pointID, status)
	if err != nil {
		return fmt.Errorf("catalog: set verification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SourceLock is held by the only writer of a source's history. It is an
// advisory lock bound to a dedicated connection, so it is released
// automatically if the process dies.
type SourceLock struct {
	conn     *pgxpool.Conn
	sourceID int64
	once     sync.Once
}

func lockName(sourceID int64) string { return "timevault-source-" + strconv.FormatInt(sourceID, 10) }

// lockKey is the advisory-lock key of a source. Advisory locks are shared by
// the whole database, so the key includes the schema: two catalogs that live
// in different schemas never block each other.
const lockKey = `hashtextextended(coalesce(current_schema(), '') || ':' || $1::text, 0)`

// LockSource takes the writer lock of a source without waiting. It returns
// ErrBusy when another backup already holds it.
func (s *Store) LockSource(ctx context.Context, sourceID int64) (*SourceLock, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog: acquire connection: %w", err)
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(`+lockKey+`)`, lockName(sourceID)).Scan(&ok); err != nil {
		conn.Release()
		return nil, fmt.Errorf("catalog: lock source: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, ErrBusy
	}
	return &SourceLock{conn: conn, sourceID: sourceID}, nil
}

// Unlock releases the lock. It is safe to call more than once.
func (l *SourceLock) Unlock() {
	l.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock(`+lockKey+`)`, lockName(l.sourceID)); err != nil {
			// Closing the session is the only way left to drop the lock.
			_ = l.conn.Hijack().Close(ctx)
			return
		}
		l.conn.Release()
	})
}

// cleanupUncommitted removes everything a build that never committed left in
// the history: versions it created, and the closures it applied to versions
// that are still current. It must run while holding the source lock.
func cleanupUncommitted(ctx context.Context, tx pgx.Tx, sourceID int64) error {
	var latest int
	if err := tx.QueryRow(ctx,
		`SELECT coalesce(max(seq), 0) FROM recovery_points WHERE source_id = $1 AND state = 'committed'`,
		sourceID).Scan(&latest); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM node_versions WHERE source_id = $1 AND first_point > $2`, sourceID, latest); err != nil {
		return err
	}
	if latest > 0 {
		if _, err := tx.Exec(ctx, `UPDATE node_versions SET last_point = NULL WHERE source_id = $1 AND last_point >= $2`, sourceID, latest); err != nil {
			return err
		}
	}
	return nil
}

// BeginPoint starts a new recovery point. It first discards anything an
// earlier unfinished build left behind. prevSeq is the sequence number of the
// newest committed point (0 for the first backup).
func (s *Store) BeginPoint(ctx context.Context, lock *SourceLock) (p Point, prevSeq int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return p, 0, fmt.Errorf("catalog: begin point: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	sourceID := lock.sourceID
	if _, err = tx.Exec(ctx, `SELECT 1 FROM sources WHERE id = $1 FOR UPDATE`, sourceID); err != nil {
		return p, 0, err
	}
	if _, err = tx.Exec(ctx,
		`UPDATE recovery_points SET state = 'aborted', failure_reason = 'interrupted before it finished'
		 WHERE source_id = $1 AND state = 'building'`, sourceID); err != nil {
		return p, 0, err
	}
	if err = cleanupUncommitted(ctx, tx, sourceID); err != nil {
		return p, 0, fmt.Errorf("catalog: clean up unfinished backup: %w", err)
	}
	if err = tx.QueryRow(ctx,
		`SELECT coalesce(max(seq), 0) FROM recovery_points WHERE source_id = $1 AND state = 'committed'`,
		sourceID).Scan(&prevSeq); err != nil {
		return p, 0, err
	}
	var nextSeq int
	if err = tx.QueryRow(ctx, `SELECT coalesce(max(seq), 0) + 1 FROM recovery_points WHERE source_id = $1`, sourceID).Scan(&nextSeq); err != nil {
		return p, 0, err
	}
	kind := "incremental"
	if prevSeq == 0 {
		kind = "full"
	}
	p, err = scanPoint(tx.QueryRow(ctx,
		`INSERT INTO recovery_points (source_id, seq, kind, state, consistency)
		 VALUES ($1, $2, $3, 'building', 'live_scan')
		 RETURNING `+pointColumns, sourceID, nextSeq, kind))
	if err != nil {
		return p, 0, fmt.Errorf("catalog: insert point: %w", err)
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO nodes (source_id, parent_id, name, name_key) VALUES ($1, NULL, '', '')
		 ON CONFLICT (source_id) WHERE parent_id IS NULL DO NOTHING`, sourceID); err != nil {
		return p, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return p, 0, fmt.Errorf("catalog: commit begin: %w", err)
	}
	return p, prevSeq, nil
}

// CommitPoint publishes a built point: from now on readers can see its history.
func (s *Store) CommitPoint(ctx context.Context, lock *SourceLock, p Point, st Stats, manifest []byte) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE recovery_points SET
			state = 'committed', committed_at = now(), verification = 'validated',
			file_count = $2, dir_count = $3, total_bytes = $4, added_count = $5, modified_count = $6,
			deleted_count = $7, new_object_count = $8, new_object_bytes = $9, skipped_count = $10,
			manifest_sha256 = $11
		 WHERE id = $1 AND source_id = $12 AND state = 'building'`,
		p.ID, st.FileCount, st.DirCount, st.TotalBytes, st.AddedCount, st.ModifiedCount,
		st.DeletedCount, st.NewObjectCount, st.NewObjectBytes, st.SkippedCount, manifest, lock.sourceID)
	if err != nil {
		return fmt.Errorf("catalog: commit point: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("catalog: point %d is not building", p.ID)
	}
	return nil
}

// FailPoint marks a building point failed or aborted and removes its partial history.
func (s *Store) FailPoint(ctx context.Context, lock *SourceLock, p Point, state, reason string) error {
	if state != StateFailed && state != StateAborted {
		return fmt.Errorf("catalog: %q is not a failure state", state)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx,
		`UPDATE recovery_points SET state = $2, failure_reason = $3 WHERE id = $1 AND state = 'building'`,
		p.ID, state, reason); err != nil {
		return err
	}
	if err := cleanupUncommitted(ctx, tx, lock.sourceID); err != nil {
		return fmt.Errorf("catalog: clean up failed point: %w", err)
	}
	return tx.Commit(ctx)
}

// RecoverStalePoints runs at startup: any point still marked building belongs
// to a process that died, so it is aborted and its partial history removed.
func (s *Store) RecoverStalePoints(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT source_id FROM recovery_points WHERE state = 'building'`)
	if err != nil {
		return 0, fmt.Errorf("catalog: find stale points: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, id := range ids {
		lock, err := s.LockSource(ctx, id)
		if errors.Is(err, ErrBusy) {
			continue
		}
		if err != nil {
			return recovered, err
		}
		err = func() error {
			defer lock.Unlock()
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			tag, err := tx.Exec(ctx,
				`UPDATE recovery_points SET state = 'aborted', failure_reason = 'interrupted by a service restart'
				 WHERE source_id = $1 AND state = 'building'`, id)
			if err != nil {
				return err
			}
			if err := cleanupUncommitted(ctx, tx, id); err != nil {
				return err
			}
			recovered += int(tag.RowsAffected())
			return tx.Commit(ctx)
		}()
		if err != nil {
			return recovered, fmt.Errorf("catalog: recover source %d: %w", id, err)
		}
	}
	return recovered, nil
}
