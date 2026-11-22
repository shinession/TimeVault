package db

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegistrySchema is the schema that holds the register of the servers that share a database (see internal/cluster). It is
// not the schema of a server.
const RegistrySchema = "timevault_cluster"

// catalogTables and catalogFunctions are the objects that the migrations create in the schema of a server, plus the table
// in which the migrations are recorded. They move together when a server is moved into another schema. Indexes, identity
// sequences, constraints and triggers belong to their table and move with it.
// TestCatalogObjectsAreWhatTheMigrationsCreate compares these lists with what the migrations create, so a migration that
// adds an object cannot be forgotten here.
var (
	catalogTables = []string{
		"schema_migrations", "sources", "objects", "recovery_points", "nodes", "node_versions", "jobs", "job_events",
		"restore_items", "audit_events", "source_access", "site_settings",
	}
	catalogFunctions = []string{"audit_events_immutable()"}
)

// CatalogTables lists the tables of the schema of a server.
func CatalogTables() []string { return slices.Clone(catalogTables) }

// CatalogFunctions lists the functions of the schema of a server, as name and argument types.
func CatalogFunctions() []string { return slices.Clone(catalogFunctions) }

var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ValidSchemaName says whether name can be used as the schema of a server: lower-case letters, digits and underscores, not
// starting with a digit, at most 63 characters, and not one that PostgreSQL keeps for itself.
func ValidSchemaName(name string) error {
	switch {
	case !schemaNamePattern.MatchString(name):
		return fmt.Errorf("%q is not a usable schema name: use lower-case letters, digits and underscores, starting with a letter or an underscore, at most 63 characters", name)
	case strings.HasPrefix(name, "pg_") || name == "information_schema":
		return fmt.Errorf("the schema name %q is kept by PostgreSQL", name)
	}
	return nil
}

func ident(name string) string { return pgx.Identifier{name}.Sanitize() }

// CurrentSchema is the schema that the pool creates and finds its tables in: the first one of its search path.
func CurrentSchema(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var name *string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&name); err != nil {
		return "", fmt.Errorf("db: read the current schema: %w", err)
	}
	if name == nil || *name == "" {
		return "", errors.New("db: no schema is selected: the search path of the connection names none that exists")
	}
	return *name, nil
}

// CatalogVersion tells whether the schema holds the table of the applied migrations, and which version it is at (0 when the
// table is empty). It reads nothing else.
func CatalogVersion(ctx context.Context, pool *pgxpool.Pool, schema string) (version int, found bool, err error) {
	table := ident(schema) + ".schema_migrations"
	if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&found); err != nil {
		return 0, false, fmt.Errorf("db: inspect schema %s: %w", schema, err)
	}
	if !found {
		return 0, false, nil
	}
	if err := pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+table).Scan(&version); err != nil {
		return 0, true, fmt.Errorf("db: read the version of schema %s: %w", schema, err)
	}
	return version, true, nil
}

// SchemaMove is what moving the catalog of one schema into another does, or, for a preview, would do.
type SchemaMove struct {
	From, To string
	// Version is the version of the catalog: the highest migration that was applied.
	Version   int
	Tables    []string
	Functions []string
	// Owner is the database role that owns the objects; it owns the new schema when the move creates it.
	Owner string
	// CreatesSchema says that To does not exist yet and is created by the move.
	CreatesSchema bool
	// Applied says that the move was made. It is false for a preview.
	Applied bool
}

// serviceLockKey is the key of the advisory lock that a running service holds on its schema (see AcquireInstanceLock): it is
// made from the name of the schema, so a move can ask whether a service works on the schema it moves from. $1 is the name.
const serviceLockKey = `hashtextextended($1::text || ':timevault-service', 0)`

// MoveSchema moves the catalog of a server, the tables of the migrations and the function of the audit log, from the schema
// from into the schema to, in one transaction, with ALTER TABLE ... SET SCHEMA: only entries of the system catalog change, so
// it takes milliseconds whatever the size of the tables, and a failure leaves everything where it was. The same call with the
// two names swapped moves it back.
//
// Nothing is changed unless apply is true; the returned SchemaMove says what was or would be done. The move is refused,
// before anything is changed, when
//   - a service holds the lock of the schema it moves from (the service must be stopped),
//   - that schema does not hold the whole catalog,
//   - the target already holds a table of TimeVault,
//   - the account that makes the move does not own what it moves, or may not create the target schema, or may not create
//     objects in it, or
//   - the owner of the tables could not use the target afterwards: PostgreSQL checks the foreign keys with the rights of the
//     owner of a table, so without the right to use the schema they fail.
//
// The target schema is created, with the owner of the tables as its owner, when it does not exist and the account may create
// schemas; otherwise the database administrator creates it once (CREATE SCHEMA name AUTHORIZATION owner) and the move uses it.
func MoveSchema(ctx context.Context, pool *pgxpool.Pool, from, to string, apply bool) (SchemaMove, error) {
	move := SchemaMove{From: from, To: to, Tables: CatalogTables(), Functions: CatalogFunctions()}
	for _, name := range []string{from, to} {
		if err := ValidSchemaName(name); err != nil {
			return move, err
		}
		if name == RegistrySchema {
			return move, fmt.Errorf("the schema %q holds the register of the servers, not a server", RegistrySchema)
		}
	}
	if from == to {
		return move, errors.New("the schema to move from and the schema to move to are the same")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return move, fmt.Errorf("db: acquire connection: %w", err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(`+serviceLockKey+`)`, from).Scan(&locked); err != nil {
		return move, fmt.Errorf("db: take the service lock of schema %s: %w", from, err)
	}
	if !locked {
		return move, fmt.Errorf("%w: it holds the lock of schema %q; stop the service and run the command again", ErrAlreadyRunning, from)
	}
	defer func() {
		unlockCtx := context.WithoutCancel(ctx)
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(`+serviceLockKey+`)`, from); err != nil {
			// Closing the session is the only way left to drop the lock.
			_ = conn.Hijack().Close(unlockCtx)
		}
	}()

	if err := move.plan(ctx, conn); err != nil {
		return move, err
	}
	if !apply {
		return move, nil
	}
	if err := move.apply(ctx, conn); err != nil {
		return move, err
	}
	move.Applied = true
	return move, nil
}

// holdings lists, by name, what a schema holds of the tables and functions of the catalog and who owns it.
type holdings struct {
	tables, functions map[string]string // name -> owner
}

func holdingsOf(ctx context.Context, conn *pgxpool.Conn, schema string) (holdings, error) {
	h := holdings{tables: map[string]string{}, functions: map[string]string{}}
	rows, err := conn.Query(ctx,
		`SELECT c.relname, pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND c.relname = ANY($2)`, schema, catalogTables)
	if err != nil {
		return h, fmt.Errorf("db: list the tables of schema %s: %w", schema, err)
	}
	for rows.Next() {
		var name, owner string
		if err := rows.Scan(&name, &owner); err != nil {
			rows.Close()
			return h, err
		}
		h.tables[name] = owner
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	rows, err = conn.Query(ctx,
		`SELECT p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')', pg_get_userbyid(p.proowner)
		 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = $1`, schema)
	if err != nil {
		return h, fmt.Errorf("db: list the functions of schema %s: %w", schema, err)
	}
	for rows.Next() {
		var name, owner string
		if err := rows.Scan(&name, &owner); err != nil {
			rows.Close()
			return h, err
		}
		if slices.Contains(catalogFunctions, name) {
			h.functions[name] = owner
		}
	}
	return h, rows.Err()
}

func (h holdings) any() bool { return len(h.tables)+len(h.functions) > 0 }

// missing lists the tables and functions of the catalog that the schema does not hold.
func (h holdings) missing() []string {
	var out []string
	for _, t := range catalogTables {
		if _, ok := h.tables[t]; !ok {
			out = append(out, t)
		}
	}
	for _, f := range catalogFunctions {
		if _, ok := h.functions[f]; !ok {
			out = append(out, f)
		}
	}
	return out
}

func (h holdings) owners() []string {
	set := map[string]bool{}
	for _, o := range h.tables {
		set[o] = true
	}
	for _, o := range h.functions {
		set[o] = true
	}
	out := make([]string, 0, len(set))
	for o := range set {
		out = append(out, o)
	}
	slices.Sort(out)
	return out
}

func (h holdings) names() []string {
	out := make([]string, 0, len(h.tables)+len(h.functions))
	for t := range h.tables {
		out = append(out, t)
	}
	for f := range h.functions {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}

// plan checks everything that can be checked without changing anything, and fills in Version, Owner and CreatesSchema.
func (m *SchemaMove) plan(ctx context.Context, conn *pgxpool.Conn) error {
	var fromExists, toExists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1), EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $2)`,
		m.From, m.To).Scan(&fromExists, &toExists); err != nil {
		return fmt.Errorf("db: look up the schemas: %w", err)
	}
	if !fromExists {
		return fmt.Errorf("the schema %q does not exist", m.From)
	}
	m.CreatesSchema = !toExists

	held, err := holdingsOf(ctx, conn, m.From)
	if err != nil {
		return err
	}
	if !held.any() {
		return fmt.Errorf("the schema %q holds no TimeVault catalog (no table of the migrations)", m.From)
	}
	if missing := held.missing(); len(missing) > 0 {
		return fmt.Errorf("the schema %q holds only a part of a TimeVault catalog (missing: %s); nothing is moved", m.From, strings.Join(missing, ", "))
	}
	owners := held.owners()
	if len(owners) != 1 {
		return fmt.Errorf("the tables of schema %q have more than one owner (%s); give them one owner before they are moved", m.From, strings.Join(owners, ", "))
	}
	m.Owner = owners[0]

	var account string
	var member bool
	if err := conn.QueryRow(ctx, `SELECT current_user::text, pg_has_role(current_user, $1, 'MEMBER')`, m.Owner).Scan(&account, &member); err != nil {
		return fmt.Errorf("db: check the owner of the tables: %w", err)
	}
	if !member {
		return fmt.Errorf("the tables of schema %q belong to the database role %q, and the account that runs the command is not that role or one of its members: run it with the account of the service", m.From, m.Owner)
	}

	if err := conn.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+ident(m.From)+`.schema_migrations`).Scan(&m.Version); err != nil {
		return fmt.Errorf("db: read the version of schema %s: %w", m.From, err)
	}

	if toExists {
		there, err := holdingsOf(ctx, conn, m.To)
		if err != nil {
			return err
		}
		if there.any() {
			return fmt.Errorf("the schema %q already holds TimeVault tables (%s); a catalog can only be moved into a schema that has none", m.To, strings.Join(there.names(), ", "))
		}
		var create, useOwn, useOwner bool
		if err := conn.QueryRow(ctx,
			`SELECT has_schema_privilege(current_user, $1, 'CREATE'), has_schema_privilege(current_user, $1, 'USAGE'), has_schema_privilege($2, $1, 'USAGE')`,
			m.To, m.Owner).Scan(&create, &useOwn, &useOwner); err != nil {
			return fmt.Errorf("db: check the rights on schema %s: %w", m.To, err)
		}
		switch {
		case !create || !useOwn:
			return fmt.Errorf("the account that runs the command may not create objects in schema %q: the database administrator can run  GRANT USAGE, CREATE ON SCHEMA %s TO %s;", m.To, ident(m.To), ident(account))
		case !useOwner:
			return fmt.Errorf("the role %q that owns the tables could not use schema %q afterwards, and PostgreSQL checks foreign keys with the rights of the table owner: the database administrator can run  GRANT USAGE ON SCHEMA %s TO %s;", m.Owner, m.To, ident(m.To), ident(m.Owner))
		}
		return nil
	}
	var mayCreate bool
	if err := conn.QueryRow(ctx, `SELECT has_database_privilege(current_user, current_database(), 'CREATE')`).Scan(&mayCreate); err != nil {
		return fmt.Errorf("db: check the right to create schemas: %w", err)
	}
	if !mayCreate {
		return fmt.Errorf("the schema %q does not exist and the account that runs the command may not create schemas: the database administrator can run  CREATE SCHEMA %s AUTHORIZATION %s;  once, and the command does the rest",
			m.To, ident(m.To), ident(m.Owner))
	}
	return nil
}

// apply makes the move in one transaction and checks the result before it commits.
func (m *SchemaMove) apply(ctx context.Context, conn *pgxpool.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin the move: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// A table that somebody else holds open would make the move wait; waiting for ever is worse than failing.
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '15s'`); err != nil {
		return err
	}
	if m.CreatesSchema {
		if _, err := tx.Exec(ctx, `CREATE SCHEMA `+ident(m.To)+` AUTHORIZATION `+ident(m.Owner)); err != nil {
			return fmt.Errorf("db: create schema %s: %w", m.To, err)
		}
	}
	for _, t := range m.Tables {
		if _, err := tx.Exec(ctx, `ALTER TABLE `+ident(m.From)+`.`+ident(t)+` SET SCHEMA `+ident(m.To)); err != nil {
			return fmt.Errorf("db: move table %s: %w", t, err)
		}
	}
	for _, f := range m.Functions {
		if _, err := tx.Exec(ctx, `ALTER FUNCTION `+ident(m.From)+`.`+f+` SET SCHEMA `+ident(m.To)); err != nil {
			return fmt.Errorf("db: move function %s: %w", f, err)
		}
	}
	var left, arrived, version int
	if err := tx.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		         WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND c.relname = ANY($3)),
		        (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		         WHERE n.nspname = $2 AND c.relkind IN ('r', 'p') AND c.relname = ANY($3))`,
		m.From, m.To, catalogTables).Scan(&left, &arrived); err != nil {
		return fmt.Errorf("db: check the move: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+ident(m.To)+`.schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("db: read the version in schema %s after the move: %w", m.To, err)
	}
	if left != 0 || arrived != len(catalogTables) || version != m.Version {
		return fmt.Errorf("db: the move did not give the expected result (%d tables left in %s, %d in %s, version %d, expected %d): nothing was changed",
			left, m.From, arrived, m.To, version, m.Version)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit the move: %w", err)
	}
	return nil
}
