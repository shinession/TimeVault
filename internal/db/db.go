// Package db opens the PostgreSQL pool and applies the embedded migrations.
package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationLockKey serializes concurrent migrators ("TVMI").
const migrationLockKey int64 = 0x54564D49

// Options tunes the connection pool.
type Options struct {
	MaxConns int32
	// SearchPath pins the schema, which lets tests run in a private schema and lets several servers share a database,
	// each in a schema of its own.
	SearchPath string
	// ReadOnly makes the transactions of the pool read-only unless one asks for more (default_transaction_read_only),
	// which is how a server reads the schema of another one. It is a safeguard of the program, not a protection: that is
	// the job of the privileges of the database account.
	ReadOnly bool
	// StatementTimeout ends a statement that runs longer than this; zero leaves the setting of the database.
	StatementTimeout time.Duration
}

// Connect opens a pool and verifies that the server answers.
func Connect(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse connection settings: %w", err)
	}
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "timevault"
	if opts.SearchPath != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = opts.SearchPath
	}
	if opts.ReadOnly {
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	if opts.StatementTimeout > 0 {
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(opts.StatementTimeout.Milliseconds(), 10)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: server is not reachable: %w", err)
	}
	return pool, nil
}

// WithTx runs fn in a transaction and commits it when fn returns nil.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return err
	}
	return tx.Commit(ctx)
}

// ErrAlreadyRunning means another service already uses this database.
var ErrAlreadyRunning = errors.New("db: another TimeVault service is already running against this database")

// instanceLockKey is scoped to the schema, like every other advisory lock here.
const instanceLockKey = `hashtextextended(coalesce(current_schema(), '') || ':timevault-service', 0)`

// InstanceLock is the right to run as the only service of a database.
type InstanceLock struct {
	conn *pgxpool.Conn
	once sync.Once
}

// AcquireInstanceLock takes the service-wide advisory lock on a dedicated
// connection, without waiting. Startup recovery marks jobs that were running as
// failed and aborts unfinished recovery points; a second service doing that
// while the first one is working would damage its jobs. PostgreSQL drops the
// lock if the process dies.
func AcquireInstanceLock(ctx context.Context, pool *pgxpool.Pool) (*InstanceLock, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire connection: %w", err)
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(`+instanceLockKey+`)`).Scan(&ok); err != nil {
		conn.Release()
		return nil, fmt.Errorf("db: take the service lock: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, ErrAlreadyRunning
	}
	return &InstanceLock{conn: conn}, nil
}

// Release gives the lock up. It is safe to call more than once.
func (l *InstanceLock) Release() {
	l.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock(`+instanceLockKey+`)`); err != nil {
			// Closing the session is the only way left to drop the lock.
			_ = l.conn.Hijack().Close(ctx)
			return
		}
		l.conn.Release()
	})
}

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("db: read migrations: %w", err)
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, _ := strings.Cut(e.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("db: migration %q must start with a positive number and underscore", e.Name())
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("db: migrations %q and %q share version %d", other, e.Name(), version)
		}
		seen[version] = e.Name()
		raw, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("db: read migration %s: %w", e.Name(), err)
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		sum := sha256.Sum256([]byte(text))
		out = append(out, migration{version: version, name: e.Name(), sql: text, checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// LatestVersion is the highest migration version contained in fsys.
func LatestVersion(fsys fs.FS) (int, error) {
	migs, err := loadMigrations(fsys)
	if err != nil || len(migs) == 0 {
		return 0, err
	}
	return migs[len(migs)-1].version, nil
}

// CurrentVersion is the highest migration version applied to the database (0 if none).
func CurrentVersion(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("db: inspect schema: %w", err)
	}
	if !exists {
		return 0, nil
	}
	var v int
	if err := pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("db: read schema version: %w", err)
	}
	return v, nil
}

// Migrate applies every migration in fsys that the database has not seen yet and
// returns the names it applied. An already applied migration whose text changed
// is an error, because the database no longer matches the code that produced it.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	migs, err := loadMigrations(fsys)
	if err != nil {
		return nil, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return nil, fmt.Errorf("db: take migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    integer     PRIMARY KEY,
		name       text        NOT NULL,
		checksum   text        NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("db: create schema_migrations: %w", err)
	}

	applied := map[int]string{}
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("db: read applied migrations: %w", err)
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var done []string
	for _, m := range migs {
		if sum, ok := applied[m.version]; ok {
			if sum != m.checksum {
				return done, fmt.Errorf("db: migration %s was changed after it was applied", m.name)
			}
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return done, err
		}
		if _, err := tx.Exec(ctx, m.sql); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return done, fmt.Errorf("db: apply %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`, m.version, m.name, m.checksum); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return done, fmt.Errorf("db: record %s: %w", m.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return done, fmt.Errorf("db: commit %s: %w", m.name, err)
		}
		done = append(done, m.name)
	}
	return done, nil
}

// ErrSchemaBehind is returned by CheckSchema when migrations are pending.
var ErrSchemaBehind = errors.New("db: schema is behind the application; run: timevault-server migrate")

// CheckSchema fails when the database has not been migrated to the version the code expects.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	want, err := LatestVersion(fsys)
	if err != nil {
		return err
	}
	have, err := CurrentVersion(ctx, pool)
	if err != nil {
		return err
	}
	if have < want {
		return fmt.Errorf("%w (database is at version %d, application needs %d)", ErrSchemaBehind, have, want)
	}
	return nil
}
