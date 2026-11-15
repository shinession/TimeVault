// Package dbtest gives integration tests a private, migrated PostgreSQL schema.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/db"
	"timevault/migrations"
)

// EnvURL names the variable that points tests at a disposable PostgreSQL database.
const EnvURL = "TIMEVAULT_TEST_DATABASE_URL"

// New returns a pool whose search_path is a fresh schema with all migrations
// applied. The schema is dropped when the test ends. The test is skipped when
// EnvURL is not set.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skipf("%s is not set; skipping PostgreSQL integration test", EnvURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := db.Connect(ctx, url, db.Options{MaxConns: 2})
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "tvtest_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	pool, err := db.Connect(ctx, url, db.Options{MaxConns: 8, SearchPath: schema})
	if err != nil {
		admin.Close()
		t.Fatalf("connect with search_path: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanCancel()
		if _, err := admin.Exec(cleanCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Logf("drop test schema %s: %v", schema, err)
		}
		admin.Close()
	})
	if _, err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return pool
}
