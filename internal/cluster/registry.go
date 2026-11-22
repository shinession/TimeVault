// Package cluster is the register of the servers that share a database, and the way one server reads the history of another
// (docs/adr/0019-several-servers-one-database-one-schema-each.md). Every server keeps its tables in a schema of its own; this
// package knows which servers there are, tells the others that this one is alive, and opens read-only pools on the schemas of
// the others. It never writes into another server's schema.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/db"
	"timevault/migrations"
)

const (
	// Contract is the number of the read contract this program speaks: what the schema of a server offers to the other
	// servers. Within a contract the migrations only add (columns, tables), so servers that update themselves one after
	// another do not break each other's pages; a change that cannot be additive raises the number. A server reads only the
	// servers that report the number it knows.
	Contract = 1
	// OnlineWithin is how long after its last report a server counts as online. Its history is read either way.
	OnlineWithin = 2 * time.Minute
	// HeartbeatEvery is how often a server reports that it is alive.
	HeartbeatEvery = 30 * time.Second
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidName checks the name of a server: short, lower case, usable in an address.
func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%q is not a usable server name: use lower-case letters, digits, hyphens and underscores, starting with a letter or a digit, at most 32 characters", name)
	}
	return nil
}

// Errors of the register.
var (
	ErrNameTaken   = errors.New("a server with that name is already registered")
	ErrSchemaTaken = errors.New("a server is already registered for that schema")
)

// Server is one row of the register.
type Server struct {
	Name, Schema string
	// Version is the version of the program that last reported; Contract the read contract it speaks (0 before the first report).
	Version  string
	Contract int
	// RepositoryID and the three folders are what the server told about itself; the folders are for hiding the real folders in
	// texts, and are shown to administrators only.
	RepositoryID                                    string
	RepositoryRoot, RestoreRoot, RestoreDisplayRoot string
	RegisteredAt                                    time.Time
	RegisteredBy                                    string
	// LastSeen is when the server last reported, nil before its first report; SeenAgo is how long ago that was by the clock
	// of the database (the same clock wrote it), so the clocks of the servers need not agree for this.
	LastSeen *time.Time
	SeenAgo  time.Duration
}

// Report is what a server tells the register about itself each time it reports.
type Report struct {
	Version                                         string
	RepositoryID                                    string
	RepositoryRoot, RestoreRoot, RestoreDisplayRoot string
}

// Registry reads and writes the register. Every statement names the schema of the register, so the search path of the pool
// does not matter.
type Registry struct {
	pool   *pgxpool.Pool
	schema string
}

// NewRegistry returns the register in schema; an empty schema is db.RegistrySchema, which is what every installation uses
// (a test uses a schema of its own so that tests do not meet in one register).
func NewRegistry(pool *pgxpool.Pool, schema string) *Registry {
	if schema == "" {
		schema = db.RegistrySchema
	}
	return &Registry{pool: pool, schema: schema}
}

func (r *Registry) table() string { return pgx.Identifier{r.schema, "servers"}.Sanitize() }

// Install makes the schema of the register, when it does not exist, and applies the migrations of the register in it; it
// does nothing when it is all there. url is the connection URL of the account that may write the register. It returns the
// migrations it applied.
func Install(ctx context.Context, url, schema string) ([]string, error) {
	if schema == "" {
		schema = db.RegistrySchema
	}
	if err := db.ValidSchemaName(schema); err != nil {
		return nil, err
	}
	admin, err := db.Connect(ctx, url, db.Options{MaxConns: 1})
	if err != nil {
		return nil, err
	}
	var exists bool
	err = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists)
	if err == nil && !exists {
		_, err = admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize())
	}
	admin.Close()
	if err != nil {
		return nil, fmt.Errorf("cluster: make the schema %s of the register: %w", schema, err)
	}
	pool, err := db.Connect(ctx, url, db.Options{MaxConns: 1, SearchPath: schema})
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool, migrations.ClusterFS)
}

// Exists tells whether the register has been installed.
func (r *Registry) Exists(ctx context.Context) (bool, error) {
	var ok bool
	if err := r.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, r.table()).Scan(&ok); err != nil {
		return false, fmt.Errorf("cluster: look for the register: %w", err)
	}
	return ok, nil
}

const serverColumns = `name, schema, version, contract, repository_id, repository_root, restore_root, restore_display_root,
	registered_at, registered_by, last_seen, extract(epoch FROM (now() - last_seen))::float8`

func scanServer(row pgx.Row) (Server, error) {
	var s Server
	var ago *float64
	if err := row.Scan(&s.Name, &s.Schema, &s.Version, &s.Contract, &s.RepositoryID, &s.RepositoryRoot, &s.RestoreRoot,
		&s.RestoreDisplayRoot, &s.RegisteredAt, &s.RegisteredBy, &s.LastSeen, &ago); err != nil {
		return Server{}, err
	}
	if ago != nil {
		s.SeenAgo = time.Duration(*ago * float64(time.Second))
	}
	return s, nil
}

// List returns the servers of the register, by name.
func (r *Registry) List(ctx context.Context) ([]Server, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+serverColumns+` FROM `+r.table()+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("cluster: list the servers: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Server, error) { return scanServer(row) })
	if err != nil {
		return nil, fmt.Errorf("cluster: list the servers: %w", err)
	}
	return out, nil
}

// Register adds a server to the register. The server reports its version and its folders itself once it runs.
func (r *Registry) Register(ctx context.Context, name, schema, by string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if err := db.ValidSchemaName(schema); err != nil {
		return err
	}
	if schema == r.schema {
		return fmt.Errorf("the schema %q holds the register of the servers, not a server", schema)
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO `+r.table()+` (name, schema, registered_by) VALUES ($1, $2, $3)`, name, schema, by)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "servers_schema_uq" {
			return fmt.Errorf("%w (%s)", ErrSchemaTaken, schema)
		}
		return fmt.Errorf("%w (%s)", ErrNameTaken, name)
	}
	if err != nil {
		return fmt.Errorf("cluster: register %s: %w", name, err)
	}
	return nil
}

// Remove takes a server out of the register and says whether it was there. Its schema and its data stay as they are.
func (r *Registry) Remove(ctx context.Context, name string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM `+r.table()+` WHERE name = $1`, name)
	if err != nil {
		return false, fmt.Errorf("cluster: remove %s: %w", name, err)
	}
	return tag.RowsAffected() > 0, nil
}

// MoveSchema makes the server that is registered for schema from be registered for schema to, after its tables were moved
// (db.MoveSchema), and says which server that was. found is false when no server was registered for from.
func (r *Registry) MoveSchema(ctx context.Context, from, to string) (name string, found bool, err error) {
	if err := db.ValidSchemaName(to); err != nil {
		return "", false, err
	}
	err = r.pool.QueryRow(ctx, `UPDATE `+r.table()+` SET schema = $2 WHERE schema = $1 RETURNING name`, from, to).Scan(&name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("cluster: follow the move to schema %s: %w", to, err)
	}
	return name, true, nil
}

// Heartbeat records that the server of the schema is alive and what it says about itself, and says whether the schema is in
// the register. It changes the row of that schema only, and the time is the clock of the database.
func (r *Registry) Heartbeat(ctx context.Context, schema string, rep Report) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE `+r.table()+` SET version = $2, contract = $3, repository_id = $4, repository_root = $5, restore_root = $6,
		        restore_display_root = $7, last_seen = now()
		 WHERE schema = $1`,
		schema, rep.Version, Contract, rep.RepositoryID, rep.RepositoryRoot, rep.RestoreRoot, rep.RestoreDisplayRoot)
	if err != nil {
		return false, fmt.Errorf("cluster: report: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
