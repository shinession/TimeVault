package catalog

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the data access layer of the catalog. It is safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps an open pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool for packages that run their own queries (jobs, audit).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// RootKey is the case-insensitive identity of a source root path.
func RootKey(rootPath string) string {
	p := filepath.Clean(rootPath)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// NewSource describes a source to register. Callers validate the path first.
type NewSource struct {
	Name      string
	RootPath  string
	Excludes  []string
	CreatedBy string
	// ScheduleMinutes is the interval between automatic backups; nil means manual only.
	ScheduleMinutes *int
}

// SourceUpdate replaces every editable field of a source. Callers validate it first.
type SourceUpdate struct {
	Name            string
	RootPath        string
	Excludes        []string
	Enabled         bool
	ScheduleMinutes *int
}

const sourceColumns = `id, name, root_path, excludes, enabled, created_by, created_at, schedule_minutes, last_run_at`

func scanSource(row pgx.Row) (Source, error) {
	var s Source
	err := row.Scan(&s.ID, &s.Name, &s.RootPath, &s.Excludes, &s.Enabled, &s.CreatedBy, &s.CreatedAt, &s.ScheduleMinutes, &s.LastRunAt)
	return s, err
}

// CreateSource registers a source.
func (s *Store) CreateSource(ctx context.Context, in NewSource) (Source, error) {
	excludes := in.Excludes
	if excludes == nil {
		excludes = []string{}
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO sources (name, root_path, root_key, excludes, created_by, schedule_minutes)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+sourceColumns,
		in.Name, in.RootPath, RootKey(in.RootPath), excludes, in.CreatedBy, in.ScheduleMinutes)
	src, err := scanSource(row)
	if isUniqueViolation(err) {
		return Source{}, fmt.Errorf("%w: a source with this name or folder is already registered", ErrConflict)
	}
	if err != nil {
		return Source{}, fmt.Errorf("catalog: create source: %w", err)
	}
	return src, nil
}

// UpdateSource replaces the editable fields of a source. Pointing it at another
// folder forgets when it last ran: that run belonged to the old folder, and the
// new one is due at once if it has a schedule.
func (s *Store) UpdateSource(ctx context.Context, id int64, u SourceUpdate) (Source, error) {
	excludes := u.Excludes
	if excludes == nil {
		excludes = []string{}
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE sources
		 SET name = $2, root_path = $3, root_key = $4, excludes = $5, enabled = $6, schedule_minutes = $7,
		     last_run_at = CASE WHEN root_key = $4 THEN last_run_at ELSE NULL END
		 WHERE id = $1
		 RETURNING `+sourceColumns,
		id, u.Name, u.RootPath, RootKey(u.RootPath), excludes, u.Enabled, u.ScheduleMinutes)
	src, err := scanSource(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if isUniqueViolation(err) {
		return Source{}, fmt.Errorf("%w: a source with this name or folder is already registered", ErrConflict)
	}
	if err != nil {
		return Source{}, fmt.Errorf("catalog: update source: %w", err)
	}
	return src, nil
}

// MarkRun records that a backup run of the source has just ended, whatever its outcome.
func (s *Store) MarkRun(ctx context.Context, sourceID int64) error {
	if _, err := s.pool.Exec(ctx, `UPDATE sources SET last_run_at = now() WHERE id = $1`, sourceID); err != nil {
		return fmt.Errorf("catalog: mark run: %w", err)
	}
	return nil
}

// DueSources returns the enabled sources whose schedule says a backup is due,
// the one waiting longest first. A source that never ran is due at once.
func (s *Store) DueSources(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM sources
		 WHERE enabled AND schedule_minutes IS NOT NULL
		   AND (last_run_at IS NULL OR last_run_at + make_interval(mins => schedule_minutes) <= now())
		 ORDER BY coalesce(last_run_at, 'epoch'), id`)
	if err != nil {
		return nil, fmt.Errorf("catalog: due sources: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("catalog: due sources: %w", err)
	}
	return ids, nil
}

// GetSource returns one source.
func (s *Store) GetSource(ctx context.Context, id int64) (Source, error) {
	src, err := scanSource(s.pool.QueryRow(ctx, `SELECT `+sourceColumns+` FROM sources WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("catalog: get source: %w", err)
	}
	return src, nil
}

// ListSources returns every source with its newest committed point.
func (s *Store) ListSources(ctx context.Context) ([]SourceSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sourceColumns+` FROM sources ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list sources: %w", err)
	}
	sources, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Source, error) { return scanSource(r) })
	if err != nil {
		return nil, fmt.Errorf("catalog: list sources: %w", err)
	}
	out := make([]SourceSummary, 0, len(sources))
	for _, src := range sources {
		sum := SourceSummary{Source: src}
		p, err := s.LatestCommitted(ctx, src.ID)
		switch {
		case err == nil:
			sum.Latest = &p
		case errors.Is(err, ErrNotFound):
		default:
			return nil, err
		}
		out = append(out, sum)
	}
	return out, nil
}

// RootNode returns the id of the source's root folder node, or ErrNotFound if
// no backup has run yet.
func (s *Store) RootNode(ctx context.Context, sourceID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM nodes WHERE source_id = $1 AND parent_id IS NULL`, sourceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("catalog: root node: %w", err)
	}
	return id, nil
}
