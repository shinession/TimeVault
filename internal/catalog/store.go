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
}

const sourceColumns = `id, name, root_path, excludes, enabled, created_by, created_at`

func scanSource(row pgx.Row) (Source, error) {
	var s Source
	err := row.Scan(&s.ID, &s.Name, &s.RootPath, &s.Excludes, &s.Enabled, &s.CreatedBy, &s.CreatedAt)
	return s, err
}

// CreateSource registers a source.
func (s *Store) CreateSource(ctx context.Context, in NewSource) (Source, error) {
	excludes := in.Excludes
	if excludes == nil {
		excludes = []string{}
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO sources (name, root_path, root_key, excludes, created_by)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+sourceColumns,
		in.Name, in.RootPath, RootKey(in.RootPath), excludes, in.CreatedBy)
	src, err := scanSource(row)
	if isUniqueViolation(err) {
		return Source{}, fmt.Errorf("%w: a source with this name or folder is already registered", ErrConflict)
	}
	if err != nil {
		return Source{}, fmt.Errorf("catalog: create source: %w", err)
	}
	return src, nil
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
