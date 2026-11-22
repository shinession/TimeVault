package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/cluster"
	"timevault/internal/db"
	"timevault/internal/db/dbtest"
)

// commandDB gives a test a migrated schema (the one of "this server"), the URL of the database, and a register of a schema of
// its own that the commands use, so that the tests do not meet in the register of the database.
func commandDB(t *testing.T) (pool *pgxpool.Pool, url, schema string) {
	t.Helper()
	url = os.Getenv(dbtest.EnvURL)
	if url == "" {
		t.Skipf("%s is not set; skipping PostgreSQL integration test", dbtest.EnvURL)
	}
	pool = dbtest.New(t)
	var err error
	if schema, err = db.CurrentSchema(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	previous := registrySchema
	registrySchema = "tvtest_reg_" + hex.EncodeToString(b)
	admin, err := db.Connect(context.Background(), url, db.Options{MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+registrySchema+` CASCADE`)
		admin.Close()
		registrySchema = previous
	})
	return pool, url, schema
}

func run(t *testing.T, pool *pgxpool.Pool, url string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := clusterCommand(context.Background(), &out, pool, url, args)
	return out.String(), err
}

func TestClusterRegisterListRemove(t *testing.T) {
	pool, url, schema := commandDB(t)
	ctx := context.Background()

	// Nothing is registered, and there is no register yet: the server is on its own.
	out, err := run(t, pool, url, "list")
	if err != nil || !strings.Contains(out, "runs on its own") {
		t.Fatalf("list without a register = %q, %v", out, err)
	}

	for _, bad := range []string{"Alpha", "bad name", ""} {
		if _, err := run(t, pool, url, "register", bad); err == nil {
			t.Errorf("register %q must be refused", bad)
		}
	}
	if _, err := run(t, pool, url, "register"); err == nil {
		t.Error("register without a name must be refused")
	}

	out, err = run(t, pool, url, "register", "alpha")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.Contains(out, "register of the servers was made") || !strings.Contains(out, schema) || !strings.Contains(out, `"alpha"`) || !strings.Contains(out, "restart") {
		t.Errorf("register said %q", out)
	}
	if _, err := run(t, pool, url, "register", "alpha"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("a second registration of the same schema: %v", err)
	}
	if _, err := run(t, pool, url, "register", "beta"); err == nil {
		t.Error("this schema is registered already; another name must be refused")
	}

	out, err = run(t, pool, url, "list")
	if err != nil || !strings.Contains(out, "alpha (this server)") || !strings.Contains(out, schema) || !strings.Contains(out, "waiting") {
		t.Fatalf("list = %q, %v", out, err)
	}

	// A schema without a catalog is not a server.
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	empty := "tvtest_empty_" + hex.EncodeToString(suffix)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA `+empty); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA `+empty+` CASCADE`) })
	other := openOn(t, url, empty)
	if _, err := run(t, other, url, "register", "gamma"); err == nil || !strings.Contains(err.Error(), "no TimeVault catalog") {
		t.Errorf("register of a schema without a catalog: %v", err)
	}

	out, err = run(t, pool, url, "remove", "alpha")
	if err != nil || !strings.Contains(out, "That is this server") {
		t.Fatalf("remove = %q, %v", out, err)
	}
	if _, err := run(t, pool, url, "remove", "alpha"); err == nil {
		t.Error("removing a server that is not registered must say so")
	}
	if _, err := run(t, pool, url, "nonsense"); err == nil {
		t.Error("an unknown command must be refused")
	}
}

func openOn(t *testing.T, url, schema string) *pgxpool.Pool {
	t.Helper()
	pool, err := db.Connect(context.Background(), url, db.Options{MaxConns: 2, SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestMoveSchemaCommandPreviewsMovesAndFollowsTheRegister(t *testing.T) {
	pool, url, schema := commandDB(t)
	ctx := context.Background()
	if _, err := run(t, pool, url, "register", "alpha"); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	target := "tvtest_to_" + hex.EncodeToString(b)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+target+` CASCADE`) })

	// The preview changes nothing and says how to go on.
	var out bytes.Buffer
	if err := moveSchema(ctx, &out, pool, "", target, false); err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{"preview", "Nothing was changed", "--apply", schema} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview does not say %q: %s", want, out.String())
		}
	}
	if _, found, _ := db.CatalogVersion(ctx, pool, schema); !found {
		t.Fatal("a preview moved the catalog")
	}

	out.Reset()
	if err := moveSchema(ctx, &out, pool, "", target, true); err != nil {
		t.Fatalf("move: %v", err)
	}
	if !strings.Contains(out.String(), "search_path="+target) || !strings.Contains(out.String(), `server "alpha"`) {
		t.Errorf("the move said %q", out.String())
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Error("the output names the database URL")
	}
	if _, found, _ := db.CatalogVersion(ctx, pool, target); !found {
		t.Fatal("the catalog is not in the target")
	}
	// The register names the new schema now, so the other servers read the right one.
	servers, err := cluster.NewRegistry(pool, registrySchema).List(ctx)
	if err != nil || len(servers) != 1 || servers[0].Schema != target {
		t.Fatalf("register after the move = %+v, %v", servers, err)
	}

	// Moving what is not there is refused with a reason.
	out.Reset()
	if err := moveSchema(ctx, &out, pool, schema, target, true); err == nil || !strings.Contains(err.Error(), "no TimeVault catalog") {
		t.Fatalf("moving from a schema that holds nothing: %v", err)
	}
	// The usage text answers help, and a missing --to is an error.
	if err := runMoveSchema([]string{"--from", "public"}); err == nil || !strings.Contains(err.Error(), "--to") {
		t.Errorf("move-schema without --to: %v", err)
	}
}
