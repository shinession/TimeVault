package catalog

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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
	// DisplayPath is the network path users know the folder by; empty means none.
	DisplayPath string
	// MaxVersions is the most versions of one file to keep; nil means unlimited.
	MaxVersions *int
}

// SourceUpdate replaces every editable field of a source. Callers validate it first.
type SourceUpdate struct {
	Name            string
	RootPath        string
	Excludes        []string
	Enabled         bool
	ScheduleMinutes *int
	// DisplayPath is the network path users know the folder by; empty removes it.
	DisplayPath string
	// MaxVersions is the most versions of one file to keep; nil means unlimited.
	MaxVersions *int
}

// sourceColumns lists the columns scanSource reads; a missing display path comes back as "".
const sourceColumns = `id, name, root_path, excludes, enabled, created_by, created_at, schedule_minutes, last_run_at, coalesce(display_path, ''),
	max_versions, last_run_ms, last_walk_at, last_walk_items`

func scanSource(row pgx.Row) (Source, error) {
	var s Source
	err := row.Scan(&s.ID, &s.Name, &s.RootPath, &s.Excludes, &s.Enabled, &s.CreatedBy, &s.CreatedAt, &s.ScheduleMinutes, &s.LastRunAt, &s.DisplayPath,
		&s.MaxVersions, &s.LastRunMS, &s.LastWalkAt, &s.LastWalkItems)
	return s, err
}

// textOrNull stores an empty string as NULL.
func textOrNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// CreateSource registers a source.
func (s *Store) CreateSource(ctx context.Context, in NewSource) (Source, error) {
	excludes := in.Excludes
	if excludes == nil {
		excludes = []string{}
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO sources (name, root_path, root_key, excludes, created_by, schedule_minutes, display_path, max_versions)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+sourceColumns,
		in.Name, in.RootPath, RootKey(in.RootPath), excludes, in.CreatedBy, in.ScheduleMinutes, textOrNull(in.DisplayPath), in.MaxVersions)
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
// folder forgets when it last ran and last scanned: those belonged to the old folder, and the
// new one is due at once if it has a schedule. Changing the exclude patterns forgets the last scan too,
// because what a scan has to capture changed without a file changing: the search engine could not tell that
// anything is new. The display path can change at any time, also when the source already has recovery points.
func (s *Store) UpdateSource(ctx context.Context, id int64, u SourceUpdate) (Source, error) {
	excludes := u.Excludes
	if excludes == nil {
		excludes = []string{}
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE sources
		 SET name = $2, root_path = $3, root_key = $4, excludes = $5, enabled = $6, schedule_minutes = $7, display_path = $8,
		     max_versions = $9,
		     last_run_at     = CASE WHEN root_key = $4 THEN last_run_at ELSE NULL END,
		     last_run_ms     = CASE WHEN root_key = $4 THEN last_run_ms ELSE NULL END,
		     last_walk_at    = CASE WHEN root_key = $4 AND excludes = $5 THEN last_walk_at ELSE NULL END,
		     last_walk_items = CASE WHEN root_key = $4 AND excludes = $5 THEN last_walk_items ELSE NULL END
		 WHERE id = $1
		 RETURNING `+sourceColumns,
		id, u.Name, u.RootPath, RootKey(u.RootPath), excludes, u.Enabled, u.ScheduleMinutes, textOrNull(u.DisplayPath), u.MaxVersions)
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

// RunEnd describes how a backup run ended, for the scheduler.
type RunEnd struct {
	// Duration is how long the run took; zero leaves the recorded duration as it is.
	Duration time.Duration
	// WalkStarted is when the scan of the folder began. It is zero when no complete scan was made (the run
	// failed, was canceled or was skipped), and then the scan time and item count recorded before stay as they are.
	WalkStarted time.Time
	// WalkItems is how many entries the search engine held below the folder when the scan began; nil when it was
	// not asked or did not know the folder. It is recorded together with WalkStarted.
	WalkItems *int64
}

// RecordRun records that a backup run of the source has just ended, whatever its outcome: the schedule
// interval restarts, and the duration and the scan start are kept for the scheduler.
func (s *Store) RecordRun(ctx context.Context, sourceID int64, end RunEnd) error {
	var ms *int64
	if end.Duration > 0 {
		v := end.Duration.Milliseconds()
		ms = &v
	}
	var walk *time.Time
	if !end.WalkStarted.IsZero() {
		walk = &end.WalkStarted
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE sources
		 SET last_run_at = now(), last_run_ms = coalesce($2, last_run_ms),
		     last_walk_at    = CASE WHEN $3::timestamptz IS NULL THEN last_walk_at ELSE $3 END,
		     last_walk_items = CASE WHEN $3::timestamptz IS NULL THEN last_walk_items ELSE $4 END
		 WHERE id = $1`, sourceID, ms, walk, end.WalkItems); err != nil {
		return fmt.Errorf("catalog: record run: %w", err)
	}
	return nil
}

// MarkRun records that a backup run of the source has just ended, whatever its outcome.
func (s *Store) MarkRun(ctx context.Context, sourceID int64) error {
	return s.RecordRun(ctx, sourceID, RunEnd{})
}

// Due is a source whose schedule says a backup is due.
type Due struct {
	ID              int64
	RootPath        string
	ScheduleMinutes int
	LastRunAt       *time.Time
	LastRunMS       *int64
	LastWalkAt      *time.Time
	LastWalkItems   *int64
	// HasPoint is true when the source has a committed recovery point.
	HasPoint bool
	// LatestSkipped is how many items its newest committed recovery point could not capture.
	LatestSkipped int64
}

// DueNow returns the enabled sources whose schedule says a backup is due, the one waiting longest first.
// A source that never ran is due at once.
func (s *Store) DueNow(ctx context.Context) ([]Due, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.root_path, s.schedule_minutes, s.last_run_at, s.last_run_ms, s.last_walk_at, s.last_walk_items,
		        latest.id IS NOT NULL, coalesce(latest.skipped_count, 0)
		 FROM sources s
		 LEFT JOIN LATERAL (
		     SELECT p.id, p.skipped_count FROM recovery_points p
		     WHERE p.source_id = s.id AND p.state = 'committed' ORDER BY p.seq DESC LIMIT 1
		 ) latest ON true
		 WHERE s.enabled AND s.schedule_minutes IS NOT NULL
		   AND (s.last_run_at IS NULL OR s.last_run_at + make_interval(mins => s.schedule_minutes) <= now())
		 ORDER BY coalesce(s.last_run_at, 'epoch'), s.id`)
	if err != nil {
		return nil, fmt.Errorf("catalog: due sources: %w", err)
	}
	due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Due, error) {
		var d Due
		err := r.Scan(&d.ID, &d.RootPath, &d.ScheduleMinutes, &d.LastRunAt, &d.LastRunMS, &d.LastWalkAt, &d.LastWalkItems, &d.HasPoint, &d.LatestSkipped)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: due sources: %w", err)
	}
	return due, nil
}

// DueSources returns the ids of the enabled sources whose schedule says a backup is due,
// the one waiting longest first. A source that never ran is due at once.
func (s *Store) DueSources(ctx context.Context) ([]int64, error) {
	due, err := s.DueNow(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(due))
	for i, d := range due {
		ids[i] = d.ID
	}
	return ids, nil
}

// ScheduleIntervals returns the schedule of every enabled source that has one, in minutes.
func (s *Store) ScheduleIntervals(ctx context.Context) ([]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT schedule_minutes FROM sources WHERE enabled AND schedule_minutes IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("catalog: schedule intervals: %w", err)
	}
	minutes, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, fmt.Errorf("catalog: schedule intervals: %w", err)
	}
	return minutes, nil
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

// Sources returns every source ordered by name, without recovery points. It is one query, for callers that only
// need the settings of the sources.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sourceColumns+` FROM sources ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list sources: %w", err)
	}
	sources, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Source, error) { return scanSource(r) })
	if err != nil {
		return nil, fmt.Errorf("catalog: list sources: %w", err)
	}
	return sources, nil
}

// ListSources returns every source with its newest committed point.
func (s *Store) ListSources(ctx context.Context) ([]SourceSummary, error) {
	sources, err := s.Sources(ctx)
	if err != nil {
		return nil, err
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
