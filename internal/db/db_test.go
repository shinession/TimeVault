package db_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"timevault/internal/db"
	"timevault/internal/db/dbtest"
	"timevault/migrations"
)

func TestMigrateAppliesOnceAndDetectsChanges(t *testing.T) {
	pool := dbtest.New(t) // already migrated once by the helper
	ctx := context.Background()

	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second Migrate = %v, %v; want nothing to apply", applied, err)
	}
	want, _ := db.LatestVersion(migrations.FS)
	have, err := db.CurrentVersion(ctx, pool)
	if err != nil || have != want {
		t.Fatalf("CurrentVersion = %d, %v; want %d", have, err, want)
	}
	if err := db.CheckSchema(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("CheckSchema: %v", err)
	}

	tampered := fstest.MapFS{}
	original, _ := migrations.FS.ReadFile("0001_init.sql")
	tampered["0001_init.sql"] = &fstest.MapFile{Data: append(original, []byte("\n-- edited\n")...)}
	if _, err := db.Migrate(ctx, pool, tampered); err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("Migrate with a modified migration = %v, want a changed-migration error", err)
	}

	ahead := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		ahead[e.Name()] = &fstest.MapFile{Data: data}
	}
	probe := fmt.Sprintf("%04d_next.sql", want+1)
	ahead[probe] = &fstest.MapFile{Data: []byte("CREATE TABLE migration_probe (id int);")}
	if err := db.CheckSchema(ctx, pool, ahead); err == nil {
		t.Fatal("CheckSchema must report pending migrations")
	}
	if applied, err := db.Migrate(ctx, pool, ahead); err != nil || len(applied) != 1 || applied[0] != probe {
		t.Fatalf("Migrate ahead = %v, %v", applied, err)
	}
}

// Migration 3 adds the display path to sources that may already exist: they get none, and the column only takes
// NULL or a text of 1 to 520 characters.
func TestMigrationAddsTheDisplayPathToExistingSources(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	exec := func(query string, args ...any) error {
		_, err := pool.Exec(ctx, query, args...)
		return err
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Return to the schema of version 2 with a source in it, then migrate again.
	must(exec(`ALTER TABLE sources DROP COLUMN display_path`))
	must(exec(`DELETE FROM schema_migrations WHERE version = 3`))
	must(exec(`INSERT INTO sources (name, root_path, root_key, created_by) VALUES ('old', 'D:\old', 'd:\old', 'tester')`))
	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil || len(applied) != 1 || applied[0] != "0003_display_path.sql" {
		t.Fatalf("Migrate = %v, %v; want only 0003_display_path.sql", applied, err)
	}
	var withoutPath int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM sources WHERE name = 'old' AND display_path IS NULL`).Scan(&withoutPath))
	if withoutPath != 1 {
		t.Fatal("an existing source must have no display path after the migration")
	}

	insert := func(name string, display any) error {
		return exec(`INSERT INTO sources (name, root_path, root_key, created_by, display_path) VALUES ($1, $1, $1, 'tester', $2)`, name, display)
	}
	if err := insert("null", nil); err != nil {
		t.Errorf("NULL display path: %v", err)
	}
	if err := insert("shortest", "x"); err != nil {
		t.Errorf("display path of 1 character: %v", err)
	}
	if err := insert("longest", strings.Repeat("x", 519)+"é"); err != nil {
		t.Errorf("display path of 520 characters: %v", err)
	}
	if err := insert("empty", ""); err == nil {
		t.Error("an empty display path must be refused: no display path is NULL")
	}
	if err := insert("too long", strings.Repeat("x", 521)); err == nil {
		t.Error("a display path of 521 characters must be refused")
	}
}

// Migration 5 gives the folders that exist what everybody could do until then, and the table only takes grants
// that mean something.
func TestMigrationGivesExistingSourcesTheirOldRights(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	exec := func(query string, args ...any) error {
		_, err := pool.Exec(ctx, query, args...)
		return err
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Return to the schema of version 4 with two folders in it, then migrate again.
	must(exec(`DROP TABLE source_access`))
	must(exec(`DELETE FROM schema_migrations WHERE version = 5`))
	must(exec(`INSERT INTO sources (name, root_path, root_key, created_by) VALUES ('first', 'D:\first', 'd:\first', 'tester'), ('second', 'D:\second', 'd:\second', 'tester')`))
	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil || len(applied) != 1 || applied[0] != "0005_source_access.sql" {
		t.Fatalf("Migrate = %v, %v; want only 0005_source_access.sql", applied, err)
	}
	rows, err := pool.Query(ctx, `SELECT s.name, a.kind, a.account, a.level FROM source_access a JOIN sources s ON s.id = a.source_id ORDER BY s.name, a.kind`)
	must(err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, kind, account, level string
		must(rows.Scan(&name, &kind, &account, &level))
		got = append(got, fmt.Sprintf("%s/%s/%q/%s", name, kind, account, level))
	}
	must(rows.Err())
	want := []string{`first/everyone/""/backup`, `first/guest/""/browse`, `second/everyone/""/backup`, `second/guest/""/browse`}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("grants after the migration = %v, want %v", got, want)
	}

	var id int64
	must(pool.QueryRow(ctx, `SELECT id FROM sources WHERE name = 'first'`).Scan(&id))
	insert := func(kind, account, level string) error {
		return exec(`INSERT INTO source_access (source_id, kind, account, level) VALUES ($1, $2, $3, $4)`, id, kind, account, level)
	}
	if err := insert("user", "E1001", "full"); err != nil {
		t.Errorf("a grant for an account: %v", err)
	}
	if err := insert("user", strings.Repeat("E", 64), "browse"); err != nil {
		t.Errorf("an employee ID of 64 characters: %v", err)
	}
	for name, bad := range map[string][3]string{
		"guests above browse":      {"guest", "", "backup"},
		"an unknown kind":          {"group", "", "browse"},
		"an unknown level":         {"everyone", "", "admin"},
		"a user without an ID":     {"user", "", "browse"},
		"an ID on everyone":        {"everyone", "E1", "browse"},
		"an ID that is too long":   {"user", strings.Repeat("E", 65), "browse"},
		"the same principal twice": {"user", "E1001", "browse"},
	} {
		if err := insert(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("%s was stored", name)
		}
	}
}

func TestMigrationFilesAreValid(t *testing.T) {
	if v, err := db.LatestVersion(migrations.FS); err != nil || v < 1 {
		t.Fatalf("LatestVersion = %d, %v", v, err)
	}
	bad := fstest.MapFS{"init.sql": &fstest.MapFile{Data: []byte("SELECT 1")}}
	if _, err := db.LatestVersion(bad); err == nil {
		t.Fatal("a migration without a numeric prefix must be rejected")
	}
	dup := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
		"0001_b.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
	}
	if _, err := db.LatestVersion(dup); err == nil {
		t.Fatal("duplicate versions must be rejected")
	}
}

// Startup recovery of a second service would mark the first one's running jobs as failed,
// so only one service may run against a database (schema).
func TestOnlyOneServiceInstancePerDatabase(t *testing.T) {
	ctx := context.Background()
	poolA := dbtest.New(t)
	poolB := dbtest.New(t) // a different schema: independent

	first, err := db.AcquireInstanceLock(ctx, poolA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireInstanceLock(ctx, poolA); !errors.Is(err, db.ErrAlreadyRunning) {
		t.Fatalf("second instance on the same database = %v, want ErrAlreadyRunning", err)
	}
	other, err := db.AcquireInstanceLock(ctx, poolB)
	if err != nil {
		t.Fatalf("an instance on another schema must not be blocked: %v", err)
	}
	other.Release()

	first.Release()
	first.Release() // safe to repeat
	again, err := db.AcquireInstanceLock(ctx, poolA)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again.Release()
}

func TestAuditEventsAreAppendOnly(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO audit_events (actor, action) VALUES ('tester', 'probe')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_events SET actor = 'someone else'`); err == nil {
		t.Fatal("UPDATE on audit_events must be rejected")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_events`); err == nil {
		t.Fatal("DELETE on audit_events must be rejected")
	}
}
