package db_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/db"
	"timevault/internal/db/dbtest"
	"timevault/migrations"
)

// scratch gives a test an administrator pool and schemas of its own, which are dropped when the test ends.
type scratch struct {
	t     *testing.T
	url   string
	admin *pgxpool.Pool
}

func newScratch(t *testing.T) *scratch {
	t.Helper()
	url := os.Getenv(dbtest.EnvURL)
	if url == "" {
		t.Skipf("%s is not set; skipping PostgreSQL integration test", dbtest.EnvURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := db.Connect(ctx, url, db.Options{MaxConns: 4})
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	t.Cleanup(admin.Close)
	return &scratch{t: t, url: url, admin: admin}
}

func randomName(prefix string) string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// name returns a schema name that is dropped, with everything in it, when the test ends. The schema is not created.
func (s *scratch) name(prefix string) string {
	name := randomName(prefix)
	s.t.Cleanup(func() {
		if _, err := s.admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+name+` CASCADE`); err != nil {
			s.t.Logf("drop schema %s: %v", name, err)
		}
	})
	return name
}

// create makes a schema that has a name from name.
func (s *scratch) create(prefix string) string {
	name := s.name(prefix)
	if _, err := s.admin.Exec(context.Background(), `CREATE SCHEMA `+name); err != nil {
		s.t.Fatalf("create schema %s: %v", name, err)
	}
	return name
}

// pool opens a pool on the schema; role, when it is not empty, is the database role the connections act as.
func (s *scratch) pool(schema, role string) *pgxpool.Pool {
	s.t.Helper()
	cfg, err := pgxpool.ParseConfig(s.url)
	if err != nil {
		s.t.Fatal(err)
	}
	cfg.MaxConns = 3
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	if role != "" {
		cfg.ConnConfig.RuntimeParams["role"] = role
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		s.t.Fatalf("open a pool on %s: %v", schema, err)
	}
	s.t.Cleanup(pool.Close)
	return pool
}

func mustMigrate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := db.Migrate(context.Background(), pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestMoveSchemaMovesTheCatalogAndBack(t *testing.T) {
	s := newScratch(t)
	ctx := context.Background()
	latest, err := db.LatestVersion(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}

	a, b := s.create("tvmv_a"), s.name("tvmv_b")
	pa := s.pool(a, "")
	mustMigrate(t, pa)
	mustExec(t, pa, `INSERT INTO sources (name, root_path, root_key, created_by) VALUES ('Alpha', 'D:\a', 'd:\a', 'test')`)
	mustExec(t, pa, `INSERT INTO audit_events (actor, action) VALUES ('t', 'one'), ('t', 'two'), ('t', 'three')`)

	// A preview says what would happen and changes nothing.
	move, err := db.MoveSchema(ctx, s.admin, a, b, false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if move.Applied || !move.CreatesSchema || move.Version != latest || len(move.Tables) != len(db.CatalogTables()) || move.Owner == "" {
		t.Fatalf("preview = %+v", move)
	}
	if v, found, err := db.CatalogVersion(ctx, s.admin, a); err != nil || !found || v != latest {
		t.Fatalf("after the preview the catalog must still be in %s: %d %v %v", a, v, found, err)
	}
	if _, found, _ := db.CatalogVersion(ctx, s.admin, b); found {
		t.Fatal("a preview must not create the target schema")
	}

	move, err = db.MoveSchema(ctx, s.admin, a, b, true)
	if err != nil || !move.Applied {
		t.Fatalf("move: %+v, %v", move, err)
	}
	if _, found, _ := db.CatalogVersion(ctx, s.admin, a); found {
		t.Fatalf("the catalog must be gone from %s", a)
	}
	if v, found, err := db.CatalogVersion(ctx, s.admin, b); err != nil || !found || v != latest {
		t.Fatalf("the catalog must be in %s at version %d: %d %v %v", b, latest, v, found, err)
	}

	pb := s.pool(b, "")
	if n := count(t, pb, "audit_events"); n != 3 {
		t.Fatalf("audit events after the move = %d, want 3", n)
	}
	if n := count(t, pb, "sources"); n != 1 {
		t.Fatalf("sources after the move = %d, want 1", n)
	}
	// The numbering goes on where it was, the audit trail is still append-only and the service sees a finished catalog.
	var id int64
	if err := pb.QueryRow(ctx, `INSERT INTO audit_events (actor, action) VALUES ('t', 'four') RETURNING id`).Scan(&id); err != nil || id != 4 {
		t.Fatalf("next audit id = %d, %v; want 4", id, err)
	}
	if _, err := pb.Exec(ctx, `UPDATE audit_events SET actor = 'x'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("the audit trigger must still refuse changes, got %v", err)
	}
	if applied, err := db.Migrate(ctx, pb, migrations.FS); err != nil || len(applied) != 0 {
		t.Fatalf("Migrate on the moved catalog = %v, %v; want nothing to apply", applied, err)
	}

	// The same call the other way moves it back, into the schema that is empty now.
	move, err = db.MoveSchema(ctx, s.admin, b, a, true)
	if err != nil || !move.Applied || move.CreatesSchema {
		t.Fatalf("move back: %+v, %v", move, err)
	}
	if v, found, _ := db.CatalogVersion(ctx, s.admin, a); !found || v != latest {
		t.Fatalf("the catalog must be back in %s", a)
	}
}

func TestMoveSchemaRefusesWhatItCannotDoSafely(t *testing.T) {
	s := newScratch(t)
	ctx := context.Background()

	a := s.create("tvmv_a")
	pa := s.pool(a, "")
	mustMigrate(t, pa)
	empty := s.create("tvmv_empty")
	target := s.name("tvmv_to")

	partial := s.create("tvmv_part")
	pp := s.pool(partial, "")
	mustMigrate(t, pp)
	mustExec(t, pp, `DROP TABLE site_settings`)

	occupied := s.create("tvmv_full")
	mustMigrate(t, s.pool(occupied, ""))

	for _, c := range []struct{ name, from, to, want string }{
		{"the same schema", a, a, "are the same"},
		{"a name that is not usable", "Bad-Name", target, "not a usable schema name"},
		{"a name of PostgreSQL", a, "pg_catalog", "kept by PostgreSQL"},
		{"the register", a, db.RegistrySchema, "register of the servers"},
		{"a schema that does not exist", randomName("tvmv_none"), target, "does not exist"},
		{"a schema without a catalog", empty, target, "no TimeVault catalog"},
		{"a part of a catalog", partial, target, "only a part of a TimeVault catalog (missing: site_settings)"},
		{"a target that holds a catalog", a, occupied, "already holds TimeVault tables"},
	} {
		t.Run(c.name, func(t *testing.T) {
			move, err := db.MoveSchema(ctx, s.admin, c.from, c.to, true)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("MoveSchema(%s, %s) = %v, want an error containing %q", c.from, c.to, err, c.want)
			}
			if move.Applied {
				t.Fatal("a refused move must not be applied")
			}
		})
	}
	if v, found, _ := db.CatalogVersion(ctx, s.admin, a); !found || v == 0 {
		t.Fatal("a refused move must leave the catalog where it is")
	}

	// A running service holds the lock of its schema: the move waits for it to be stopped.
	lock, err := db.AcquireInstanceLock(ctx, pa)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.MoveSchema(ctx, s.admin, a, target, false); !errors.Is(err, db.ErrAlreadyRunning) {
		t.Fatalf("with a service running, MoveSchema = %v, want ErrAlreadyRunning", err)
	}
	lock.Release()
	if _, err := db.MoveSchema(ctx, s.admin, a, target, false); err != nil {
		t.Fatalf("after the service stopped, a preview must work: %v", err)
	}
}

// TestMoveSchemaChecksWhoMayDoWhat repeats what was seen by hand: a role that may not create schemas cannot make the target,
// the database administrator can make it for the role, and a schema that the owner of the tables cannot use is refused,
// because the foreign keys would fail afterwards.
func TestMoveSchemaChecksWhoMayDoWhat(t *testing.T) {
	s := newScratch(t)
	ctx := context.Background()
	var super bool
	if err := s.admin.QueryRow(ctx, `SELECT usesuper FROM pg_user WHERE usename = current_user`).Scan(&super); err != nil || !super {
		t.Skip("the test database account is not a superuser, so it cannot make a role to act as")
	}
	role := randomName("tvmv_role")
	mustExec(t, s.admin, `CREATE ROLE `+role+` NOLOGIN`)
	t.Cleanup(func() {
		_, _ = s.admin.Exec(context.Background(), `DROP OWNED BY `+role+` CASCADE`)
		_, _ = s.admin.Exec(context.Background(), `DROP ROLE `+role)
	})

	a := s.name("tvmv_a")
	mustExec(t, s.admin, `CREATE SCHEMA `+a+` AUTHORIZATION `+role)
	pa := s.pool(a, role)
	mustMigrate(t, pa)
	mustExec(t, pa, `INSERT INTO sources (name, root_path, root_key, created_by) VALUES ('Alpha', 'D:\a', 'd:\a', 'test')`)

	b := s.name("tvmv_b")
	if _, err := db.MoveSchema(ctx, pa, a, b, true); err == nil ||
		!strings.Contains(err.Error(), "may not create schemas") || !strings.Contains(err.Error(), `AUTHORIZATION "`+role+`"`) {
		t.Fatalf("a role that may not create schemas: %v, want the CREATE SCHEMA hint", err)
	}

	// Made by the administrator for the role, the schema takes the move.
	mustExec(t, s.admin, `CREATE SCHEMA `+b+` AUTHORIZATION `+role)
	if move, err := db.MoveSchema(ctx, pa, a, b, true); err != nil || !move.Applied || move.CreatesSchema || move.Owner != role {
		t.Fatalf("move into a schema made for the role: %+v, %v", move, err)
	}
	pb := s.pool(b, role)
	if _, err := pb.Exec(ctx, `INSERT INTO jobs (kind, source_id, state, requested_by) VALUES ('backup', 999, 'queued', 't')`); err == nil ||
		!strings.Contains(err.Error(), "foreign key") {
		t.Fatalf("the foreign keys must work in the new schema, got %v", err)
	}

	// A schema that the administrator made for himself: the role that owns the tables could not use it.
	c := s.create("tvmv_c")
	_, err := db.MoveSchema(ctx, s.admin, b, c, true)
	if err == nil || !strings.Contains(err.Error(), "could not use schema") || !strings.Contains(err.Error(), `GRANT USAGE ON SCHEMA "`+c+`" TO "`+role+`"`) {
		t.Fatalf("a schema the owner cannot use: %v, want the GRANT hint", err)
	}
	mustExec(t, s.admin, `GRANT USAGE ON SCHEMA `+c+` TO `+role)
	if move, err := db.MoveSchema(ctx, s.admin, b, c, true); err != nil || !move.Applied {
		t.Fatalf("after the grant: %+v, %v", move, err)
	}
}

// TestCatalogObjectsAreWhatTheMigrationsCreate keeps CatalogTables and CatalogFunctions complete: a migration that adds a
// table, a function or anything else that MoveSchema does not know how to move fails here, so that whoever adds it also
// teaches the move about it.
func TestCatalogObjectsAreWhatTheMigrationsCreate(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()

	names := func(sql string) []string {
		rows, err := pool.Query(ctx, sql)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		slices.Sort(out)
		return out
	}
	tables := names(`SELECT c.relname::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	                 WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p')`)
	if want := db.CatalogTables(); !slices.Equal(tables, slices.Sorted(slices.Values(want))) {
		t.Errorf("the migrations create the tables %v, but CatalogTables lists %v", tables, want)
	}
	functions := names(`SELECT p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')' FROM pg_proc p
	                    JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = current_schema()`)
	if want := db.CatalogFunctions(); !slices.Equal(functions, slices.Sorted(slices.Values(want))) {
		t.Errorf("the migrations create the functions %v, but CatalogFunctions lists %v", functions, want)
	}
	// Everything else that could live in the schema must be something that moves with a table.
	if other := names(`SELECT c.relname::text || ' (' || c.relkind::text || ')' FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	                   WHERE n.nspname = current_schema() AND c.relkind NOT IN ('r', 'p', 'i', 'S')`); len(other) > 0 {
		t.Errorf("the migrations create objects that MoveSchema does not move: %v", other)
	}
	if loose := names(`SELECT c.relname::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	                   WHERE n.nspname = current_schema() AND c.relkind = 'S'
	                     AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype IN ('a', 'i'))`); len(loose) > 0 {
		t.Errorf("sequences that belong to no column do not move with a table: %v", loose)
	}
	if types := names(`SELECT t.typname::text FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
	                   LEFT JOIN pg_class c ON c.oid = t.typrelid
	                   WHERE n.nspname = current_schema() AND (t.typtype IN ('e', 'd') OR (t.typtype = 'c' AND c.relkind = 'c'))`); len(types) > 0 {
		t.Errorf("types do not move with a table: %v", types)
	}
}

func TestReadOnlyPoolRefusesWrites(t *testing.T) {
	url := os.Getenv(dbtest.EnvURL)
	if url == "" {
		t.Skipf("%s is not set; skipping PostgreSQL integration test", dbtest.EnvURL)
	}
	schema := dbtest.New(t) // a migrated schema to read
	ctx := context.Background()
	name, err := db.CurrentSchema(ctx, schema)
	if err != nil {
		t.Fatal(err)
	}
	ro, err := db.Connect(ctx, url, db.Options{MaxConns: 2, SearchPath: name, ReadOnly: true, StatementTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if n := count(t, ro, "sources"); n != 0 {
		t.Fatalf("sources = %d", n)
	}
	if _, err := ro.Exec(ctx, `INSERT INTO audit_events (actor, action) VALUES ('t', 'x')`); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a write on a read-only pool = %v, want a read-only error", err)
	}
	start := time.Now()
	if _, err := ro.Exec(ctx, `SELECT pg_sleep(10)`); err == nil || !strings.Contains(err.Error(), "statement timeout") {
		t.Fatalf("a long statement = %v, want a statement timeout", err)
	}
	if time.Since(start) > 8*time.Second {
		t.Fatalf("the statement timeout did not end the statement in time: %v", time.Since(start))
	}
}
