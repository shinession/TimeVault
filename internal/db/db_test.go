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
