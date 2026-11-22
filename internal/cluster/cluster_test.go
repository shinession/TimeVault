package cluster_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/audit"
	"timevault/internal/cluster"
	"timevault/internal/db"
	"timevault/internal/db/dbtest"
	"timevault/internal/jobs"
	"timevault/internal/listing"
	"timevault/internal/service"
	"timevault/internal/testenv"
)

// newRegistry installs a register in a schema of its own, so that the tests of different packages do not meet in the one
// register of the database, and returns it with the schema and the URL of the database.
func newRegistry(t *testing.T) (reg *cluster.Registry, schema, url string, admin *pgxpool.Pool) {
	t.Helper()
	url = os.Getenv(dbtest.EnvURL)
	if url == "" {
		t.Skipf("%s is not set; skipping PostgreSQL integration test", dbtest.EnvURL)
	}
	ctx := context.Background()
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	schema = "tvtest_reg_" + hex.EncodeToString(b)
	applied, err := cluster.Install(ctx, url, schema)
	if err != nil || len(applied) == 0 {
		t.Fatalf("Install = %v, %v", applied, err)
	}
	admin, err = db.Connect(ctx, url, db.Options{MaxConns: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return cluster.NewRegistry(admin, schema), schema, url, admin
}

func schemaOf(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	name, err := db.CurrentSchema(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestInstallIsRepeatableAndRegisterIsChecked(t *testing.T) {
	reg, schema, url, _ := newRegistry(t)
	ctx := context.Background()
	if applied, err := cluster.Install(ctx, url, schema); err != nil || len(applied) != 0 {
		t.Fatalf("a second Install = %v, %v; want nothing to apply", applied, err)
	}
	if ok, err := reg.Exists(ctx); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}

	if err := reg.Register(ctx, "alpha", "tvtest_a", "me"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(ctx, "alpha", "tvtest_other", "me"); !errors.Is(err, cluster.ErrNameTaken) {
		t.Fatalf("a second server named alpha: %v, want ErrNameTaken", err)
	}
	if err := reg.Register(ctx, "beta", "tvtest_a", "me"); !errors.Is(err, cluster.ErrSchemaTaken) {
		t.Fatalf("a second server in the same schema: %v, want ErrSchemaTaken", err)
	}
	for name, schemaName := range map[string]string{"Alpha": "tvtest_x", "bad name": "tvtest_x", "": "tvtest_x", "ok": "Bad-Schema", "ok2": schema, "ok3": "pg_catalog"} {
		if err := reg.Register(ctx, name, schemaName, "me"); err == nil {
			t.Errorf("Register(%q, %q) must be refused", name, schemaName)
		}
	}

	servers, err := reg.List(ctx)
	if err != nil || len(servers) != 1 || servers[0].Name != "alpha" || servers[0].Contract != 0 || servers[0].LastSeen != nil || servers[0].RegisteredBy != "me" {
		t.Fatalf("List = %+v, %v", servers, err)
	}

	// Only the row of the schema that reports is changed, and the contract is the one of the program.
	if ok, err := reg.Heartbeat(ctx, "tvtest_nobody", cluster.Report{Version: "1"}); err != nil || ok {
		t.Fatalf("a heartbeat of a schema that is not registered = %v, %v; want false", ok, err)
	}
	rep := cluster.Report{Version: "1.1.0", RepositoryID: "repo-1", RepositoryRoot: `D:\Repo`, RestoreRoot: `D:\Restore`, RestoreDisplayRoot: `\\alpha\restore`}
	if ok, err := reg.Heartbeat(ctx, "tvtest_a", rep); err != nil || !ok {
		t.Fatalf("Heartbeat = %v, %v", ok, err)
	}
	servers, _ = reg.List(ctx)
	got := servers[0]
	if got.Version != "1.1.0" || got.Contract != cluster.Contract || got.RepositoryID != "repo-1" || got.RepositoryRoot != `D:\Repo` ||
		got.RestoreRoot != `D:\Restore` || got.RestoreDisplayRoot != `\\alpha\restore` || got.LastSeen == nil || got.SeenAgo > time.Minute {
		t.Fatalf("after a heartbeat: %+v", got)
	}

	if ok, err := reg.Remove(ctx, "alpha"); err != nil || !ok {
		t.Fatalf("Remove = %v, %v", ok, err)
	}
	if ok, _ := reg.Remove(ctx, "alpha"); ok {
		t.Fatal("removing a server twice must say that it was not there")
	}
}

func TestGroupReadsTheHistoryOfOtherServers(t *testing.T) {
	reg, regSchema, url, admin := newRegistry(t)
	ctx := context.Background()
	poolA, poolB := dbtest.New(t), dbtest.New(t)
	schemaA, schemaB := schemaOf(t, poolA), schemaOf(t, poolB)
	for name, schema := range map[string]string{"alpha": schemaA, "beta": schemaB} {
		if err := reg.Register(ctx, name, schema, "test"); err != nil {
			t.Fatal(err)
		}
	}
	// Something to read in the schema of beta.
	if _, err := poolB.Exec(ctx, `INSERT INTO sources (name, root_path, root_key, created_by, display_path) VALUES ('Beta files', 'E:\Data\Beta', 'e:\data\beta', 'test', '\\beta\files')`); err != nil {
		t.Fatal(err)
	}
	if _, err := poolB.Exec(ctx, `INSERT INTO recovery_points (source_id, seq, kind, state, committed_at) VALUES (1, 1, 'full', 'committed', now())`); err != nil {
		t.Fatal(err)
	}

	report := func() cluster.Report { return cluster.Report{Version: "1.1.0", RepositoryRoot: `D:\Repo-A`} }
	g, err := cluster.Open(ctx, cluster.Options{Pool: poolA, URL: url, Registry: regSchema, Report: report, Every: 50 * time.Millisecond})
	if err != nil || g == nil {
		t.Fatalf("Open = %v, %v", g, err)
	}
	defer g.Close()
	if g.Name() != "alpha" || g.Schema() != schemaA {
		t.Fatalf("this server is %q in %q", g.Name(), g.Schema())
	}

	state := func(name string) cluster.State {
		t.Helper()
		entries, err := g.Servers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name == name {
				if (name == "alpha") != e.Self {
					t.Fatalf("%s: Self = %v", name, e.Self)
				}
				return e.State
			}
		}
		t.Fatalf("no server %s", name)
		return ""
	}
	// Open reported at once, so this server is online; beta has not reported yet and is not read.
	if state("alpha") != cluster.StateOnline || state("beta") != cluster.StateWaiting {
		t.Fatalf("alpha %s, beta %s", state("alpha"), state("beta"))
	}
	var notReadable *cluster.NotReadableError
	if _, _, err := g.Reader(ctx, "beta"); !errors.As(err, &notReadable) || notReadable.Entry.State != cluster.StateWaiting {
		t.Fatalf("a server that has not reported: %v, want a NotReadableError", err)
	}

	// Once beta reports, its history is read from its schema.
	rep := cluster.Report{Version: "1.1.0", RepositoryRoot: `E:\Repo-B`, RestoreRoot: `E:\Restore-B`, RestoreDisplayRoot: `\\beta\restore`}
	if _, err := reg.Heartbeat(ctx, schemaB, rep); err != nil {
		t.Fatal(err)
	}
	rd, entry, err := g.Reader(ctx, "beta")
	if err != nil || entry.State != cluster.StateOnline || entry.Version != "1.1.0" {
		t.Fatalf("Reader(beta) = %v, %+v", err, entry)
	}
	sources, err := rd.Sources(ctx)
	if err != nil || len(sources) != 1 || sources[0].Name != "Beta files" || sources[0].Latest == nil || sources[0].Latest.Seq != 1 {
		t.Fatalf("the sources of beta = %+v, %v", sources, err)
	}
	if rd.ContentChecked() {
		t.Fatal("the reader of another server cannot say that content was checked")
	}
	// The folders of beta are hidden in the texts that are shown for it.
	paths, err := rd.PathMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	masked := paths.Mask(`cannot write E:\Repo-B\objects and E:\Data\Beta\x.txt and E:\Restore-B\r1`)
	for _, real := range []string{`E:\Repo-B`, `E:\Data\Beta`, `E:\Restore-B`} {
		if strings.Contains(masked, real) {
			t.Fatalf("the text still shows %s: %s", real, masked)
		}
	}
	// A second call uses the pool that is open.
	if _, _, err := g.Reader(ctx, "beta"); err != nil {
		t.Fatal(err)
	}

	// Offline is only about the heartbeat: the history is read all the same.
	if _, err := admin.Exec(ctx, `UPDATE `+regSchema+`.servers SET last_seen = now() - interval '10 minutes' WHERE name = 'beta'`); err != nil {
		t.Fatal(err)
	}
	if state("beta") != cluster.StateOffline {
		t.Fatalf("beta = %s, want offline", state("beta"))
	}
	if rd, _, err := g.Reader(ctx, "beta"); err != nil {
		t.Fatalf("the history of an offline server must be read: %v", err)
	} else if s, err := rd.Sources(ctx); err != nil || len(s) != 1 {
		t.Fatalf("sources of an offline server: %v, %v", s, err)
	}

	// Another read contract is not read.
	if _, err := admin.Exec(ctx, `UPDATE `+regSchema+`.servers SET contract = 99 WHERE name = 'beta'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Reader(ctx, "beta"); !errors.As(err, &notReadable) || notReadable.Entry.State != cluster.StateIncompatible {
		t.Fatalf("a server with another contract: %v, want a NotReadableError (incompatible)", err)
	}

	if _, _, err := g.Reader(ctx, "nobody"); !errors.Is(err, cluster.ErrUnknownServer) {
		t.Fatalf("an unknown server: %v", err)
	}
	if _, _, err := g.Reader(ctx, "alpha"); !errors.Is(err, cluster.ErrSelf) {
		t.Fatalf("this server: %v", err)
	}

	// Run keeps reporting: after the clock of the report is put back, it moves forward again.
	if _, err := admin.Exec(ctx, `UPDATE `+regSchema+`.servers SET last_seen = now() - interval '10 minutes' WHERE name = 'alpha'`); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { g.Run(runCtx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	var ago time.Duration
	for time.Now().Before(deadline) {
		servers, _ := reg.List(ctx)
		for _, s := range servers {
			if s.Name == "alpha" {
				ago = s.SeenAgo
			}
		}
		if ago < 5*time.Second {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if ago >= 5*time.Second {
		t.Fatalf("Run did not report: alpha was last seen %v ago", ago)
	}
}

func TestOpenReturnsNothingForAServerOnItsOwn(t *testing.T) {
	reg, regSchema, url, _ := newRegistry(t)
	ctx := context.Background()
	pool := dbtest.New(t)
	if err := reg.Register(ctx, "other", "tvtest_other_schema", "test"); err != nil {
		t.Fatal(err)
	}
	// A register without this server in it.
	if g, err := cluster.Open(ctx, cluster.Options{Pool: pool, URL: url, Registry: regSchema}); err != nil || g != nil {
		t.Fatalf("Open with a register that does not list this server = %v, %v; want nil", g, err)
	}
	// No register at all.
	if g, err := cluster.Open(ctx, cluster.Options{Pool: pool, URL: url, Registry: "tvtest_reg_nowhere"}); err != nil || g != nil {
		t.Fatalf("Open without a register = %v, %v; want nil", g, err)
	}
}

// TestAccountWithTheDocumentedGrantsCanReadAServer is the grants of docs/runbook.md ("Several servers on one database"):
// the database account of one server may read another server's schema with USAGE on it and SELECT on the tables that the
// pages read, and nothing else. A page that needs another table fails here, so that the documented list stays complete.
func TestAccountWithTheDocumentedGrantsCanReadAServer(t *testing.T) {
	_, _, url, admin := newRegistry(t)
	ctx := context.Background()
	var super bool
	if err := admin.QueryRow(ctx, `SELECT usesuper FROM pg_user WHERE usename = current_user`).Scan(&super); err != nil || !super {
		t.Skip("the test database account is not a superuser, so it cannot make a role to act as")
	}

	beta := testenv.New(t)
	beta.Write("a.txt", "alpha")
	beta.Write("docs/b.txt", "bravo")
	point := beta.Backup()
	beta.Write("a.txt", "alpha, second")
	beta.Backup()
	betaSchema := schemaOf(t, beta.Pool)
	if _, err := beta.Pool.Exec(ctx, `INSERT INTO jobs (kind, source_id, state, requested_by) VALUES ('backup', $1, 'succeeded', 'E1')`, beta.Src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := beta.Pool.Exec(ctx, `INSERT INTO audit_events (actor, action, source_id) VALUES ('E1', 'backup.finish', $1)`, beta.Src.ID); err != nil {
		t.Fatal(err)
	}

	role := "tvtest_reader_" + betaSchema[len(betaSchema)-10:]
	if _, err := admin.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP OWNED BY `+role)
		_, _ = admin.Exec(context.Background(), `DROP ROLE `+role)
	})
	// What the runbook says to grant.
	for _, stmt := range []string{
		`GRANT USAGE ON SCHEMA ` + betaSchema + ` TO ` + role,
		`GRANT SELECT ON ` + betaSchema + `.sources, ` + betaSchema + `.source_access, ` + betaSchema + `.recovery_points, ` + betaSchema + `.nodes, ` +
			betaSchema + `.node_versions, ` + betaSchema + `.jobs, ` + betaSchema + `.job_events, ` + betaSchema + `.restore_items, ` +
			betaSchema + `.audit_events TO ` + role,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["search_path"] = betaSchema
	cfg.ConnConfig.RuntimeParams["role"] = role
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rd := service.NewReader(pool, nil)

	// Everything the pages and the API read.
	sources, err := rd.Sources(ctx)
	if err != nil || len(sources) != 1 || sources[0].Latest == nil {
		t.Fatalf("Sources = %+v, %v", sources, err)
	}
	src, err := rd.Source(ctx, beta.Src.ID)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if _, err := rd.Point(ctx, src.ID, point.ID); err != nil {
		t.Fatalf("Point: %v", err)
	}
	if _, err := rd.PointByID(ctx, point.ID); err != nil {
		t.Fatalf("PointByID: %v", err)
	}
	if pts, err := rd.Points(ctx, src.ID, 10, 0); err != nil || len(pts) != 2 {
		t.Fatalf("Points = %d, %v", len(pts), err)
	}
	if _, err := rd.Attempts(ctx, src.ID, 10); err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	latest, err := rd.LatestPoint(ctx, src.ID)
	if err != nil {
		t.Fatalf("LatestPoint: %v", err)
	}
	if l, err := rd.ListHistory(ctx, src, latest, "", listing.Options{Limit: 50}); err != nil || len(l.Entries) == 0 {
		t.Fatalf("ListHistory = %+v, %v", l, err)
	}
	if _, err := rd.BrowseHistory(ctx, src, latest, "docs", "", 50); err != nil {
		t.Fatalf("BrowseHistory: %v", err)
	}
	if h, err := rd.FileVersions(ctx, src, "a.txt"); err != nil || len(h.Versions) != 2 || h.Checked {
		t.Fatalf("FileVersions = %+v, %v", h, err)
	}
	jobList, err := rd.Jobs(ctx, jobs.Filter{Limit: 10})
	if err != nil || len(jobList) != 1 {
		t.Fatalf("Jobs = %v, %v", jobList, err)
	}
	if _, _, err := rd.JobCounts(ctx, jobs.Filter{}); err != nil {
		t.Fatalf("JobCounts: %v", err)
	}
	if _, err := rd.Job(ctx, jobList[0].ID); err != nil {
		t.Fatalf("Job: %v", err)
	}
	if _, err := rd.JobEvents(ctx, jobList[0].ID, 0, 10); err != nil {
		t.Fatalf("JobEvents: %v", err)
	}
	if _, err := rd.RestoreItems(ctx, jobList[0].ID, "", 0, 10); err != nil {
		t.Fatalf("RestoreItems: %v", err)
	}
	if evs, err := rd.AuditEvents(ctx, audit.Filter{Limit: 10}); err != nil || len(evs) != 1 {
		t.Fatalf("AuditEvents = %v, %v", evs, err)
	}
	if _, err := rd.PathMap(ctx); err != nil {
		t.Fatalf("PathMap: %v", err)
	}

	// Nothing more than that: no writing, and the Site settings of the other server are not readable.
	if _, err := pool.Exec(ctx, `INSERT INTO audit_events (actor, action) VALUES ('x', 'y')`); err == nil {
		t.Error("the account could write")
	}
	if _, err := pool.Exec(ctx, `SELECT * FROM site_settings`); err == nil {
		t.Error("the account could read the Site settings of the other server")
	}
}
