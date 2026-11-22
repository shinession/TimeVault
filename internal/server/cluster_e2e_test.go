package server_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/access"
	"timevault/internal/catalog"
	"timevault/internal/cluster"
	"timevault/internal/config"
	"timevault/internal/db"
	"timevault/internal/db/dbtest"
	"timevault/internal/server"
	"timevault/internal/service"
	"timevault/internal/testenv"
)

// group is a server ("alpha", the one under test) that shares its database with another one ("beta"): each has its own
// schema, beta has a folder with two recovery points and a folder that only one account may browse, and both are in a
// register of a schema of its own.
type group struct {
	*harness
	test       *testing.T
	beta       *testenv.Env
	betaSchema string
	regSchema  string
	reg        *cluster.Registry
	admin      *pgxpool.Pool
	// betaSource is the folder of beta that guests may browse, betaSecret the one that only userID may browse.
	betaSource, betaSecret int64
	betaSecretRoot         string
	betaPoints             []int64
	betaJob                int64
}

func newGroup(t *testing.T) *group {
	t.Helper()
	dbURL := os.Getenv(dbtest.EnvURL)
	if dbURL == "" {
		t.Skipf("%s is not set; skipping PostgreSQL integration test", dbtest.EnvURL)
	}
	ctx := context.Background()
	g := &group{test: t}

	// Beta: another server with a folder that has a history.
	g.beta = testenv.New(t)
	g.beta.Write("a.txt", "alpha")
	g.beta.Write("docs/b.txt", "bravo")
	p1 := g.beta.Backup()
	g.beta.Write("a.txt", "alpha, second version")
	p2 := g.beta.Backup()
	g.betaPoints = []int64{p1.ID, p2.ID}
	g.betaSource = g.beta.Src.ID
	if _, err := g.beta.Pool.Exec(ctx, `UPDATE sources SET name = 'Beta files', display_path = '\\beta\files' WHERE id = $1`, g.betaSource); err != nil {
		t.Fatal(err)
	}
	only, err := access.Spec{Browse: []string{userID}}.List()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := catalog.NewStore(g.beta.Pool).CreateSource(ctx, catalog.NewSource{
		Name: "Beta secret", RootPath: filepath.Join(t.TempDir(), "secret"), CreatedBy: "test", DisplayPath: `\\beta\secret`, Access: only,
	})
	if err != nil {
		t.Fatal(err)
	}
	g.betaSecret, g.betaSecretRoot = secret.ID, secret.RootPath
	// A failed backup whose text quotes the real folder of beta, and two audit events.
	if err := g.beta.Pool.QueryRow(ctx,
		`INSERT INTO jobs (kind, source_id, state, requested_by, error) VALUES ('backup', $1, 'failed', $2, $3) RETURNING id`,
		g.betaSource, userID, "cannot read "+g.beta.SrcDir+`\locked.txt`).Scan(&g.betaJob); err != nil {
		t.Fatal(err)
	}
	for src, root := range map[int64]string{g.betaSource: g.beta.SrcDir, g.betaSecret: g.betaSecretRoot} {
		if _, err := g.beta.Pool.Exec(ctx,
			`INSERT INTO audit_events (actor, action, target_type, target_id, source_id, detail) VALUES ($1, 'backup.finish', 'source', $2, $3, $4)`,
			userID, itoa(src), src, `{"error": "failed in `+strings.ReplaceAll(root, `\`, `\\`)+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	if g.betaSchema, err = db.CurrentSchema(ctx, g.beta.Pool); err != nil {
		t.Fatal(err)
	}

	// The register, in a schema of its own.
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	g.regSchema = "tvtest_reg_" + hex.EncodeToString(b)
	if _, err := cluster.Install(ctx, dbURL, g.regSchema); err != nil {
		t.Fatal(err)
	}
	if g.admin, err = db.Connect(ctx, dbURL, db.Options{MaxConns: 3}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = g.admin.Exec(context.Background(), `DROP SCHEMA `+g.regSchema+` CASCADE`)
		g.admin.Close()
	})
	g.reg = cluster.NewRegistry(g.admin, g.regSchema)

	// Alpha: the server under test. Its group is made once its pool exists.
	var members *cluster.Group
	tune := func(cfg *service.Config, _ *config.Deployment) {
		alphaSchema, err := db.CurrentSchema(ctx, cfg.Pool)
		if err != nil {
			t.Fatal(err)
		}
		for name, schema := range map[string]string{"alpha": alphaSchema, "beta": g.betaSchema} {
			if err := g.reg.Register(ctx, name, schema, "test"); err != nil {
				t.Fatal(err)
			}
		}
		g.report("beta", cluster.Report{Version: "1.1.0", RepositoryID: g.beta.Repo.ID(), RepositoryRoot: g.beta.Repo.Root(), RestoreRoot: g.beta.RestoreRoot})
		members, err = cluster.Open(ctx, cluster.Options{
			Pool: cfg.Pool, URL: dbURL, Registry: g.regSchema,
			Report: func() cluster.Report {
				return cluster.Report{Version: "test", RepositoryID: cfg.Repo.ID(), RepositoryRoot: cfg.Repo.Root()}
			},
		})
		if err != nil || members == nil {
			t.Fatalf("Open = %v, %v", members, err)
		}
		t.Cleanup(members.Close)
	}
	g.harness = newHarnessOpts(t, tune, func(o *server.Options) { o.Group = members })
	return g
}

// report makes the server of the register say what it says when it reports.
func (g *group) report(name string, rep cluster.Report) {
	g.test.Helper()
	servers, err := g.reg.List(context.Background())
	if err != nil {
		g.test.Fatal(err)
	}
	for _, s := range servers {
		if s.Name == name {
			if _, err := g.reg.Heartbeat(context.Background(), s.Schema, rep); err != nil {
				g.test.Fatal(err)
			}
			return
		}
	}
	g.test.Fatalf("no server %s in the register", name)
}

// sql runs a statement on the register as the administrator of the database.
func (g *group) sql(stmt string, args ...any) {
	g.test.Helper()
	if _, err := g.admin.Exec(context.Background(), strings.ReplaceAll(stmt, "REG.", g.regSchema+"."), args...); err != nil {
		g.test.Fatalf("%s: %v", stmt, err)
	}
}

func (g *group) betaPath(path string, kv ...string) string {
	q := url.Values{"server": {"beta"}}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return path + "?" + q.Encode()
}

func mustContain(t *testing.T, what, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(body, p) {
			t.Errorf("%s does not contain %q", what, p)
		}
	}
}

func mustNotContain(t *testing.T, what, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(body, p) {
			t.Errorf("%s contains %q", what, p)
		}
	}
}

func TestGroupPagesShowTheHistoryOfTheOtherServers(t *testing.T) {
	g := newGroup(t)
	seed(g.harness)
	user := g.visitor()
	user.loginAs(userID)

	// The servers.
	res, body := user.get("/servers")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/servers = %d", res.StatusCode)
	}
	mustContain(t, "/servers", body, "alpha", "beta", "this server", "Online", "1.1.0")
	assertCSPClean(t, "/servers", body)
	// The nav offers the page; the Explorer says where the folders of the other servers are.
	_, explorer := user.get("/explorer")
	mustContain(t, "/explorer", explorer, `href="/servers"`, "no live view here")

	// The folder selector lists both servers' folders, the number of a folder is only meaningful with its server.
	_, home := user.get("/history")
	mustContain(t, "/history", home, `<optgroup label="This server">`, `<optgroup label="beta">`, `value="beta:`+itoa(g.betaSource)+`"`, "Beta files")

	// The history of the folder of beta, with beta's own numbers: alpha has a folder with the same number.
	if g.env.Src.ID != g.betaSource {
		t.Fatalf("the test wants the same folder number on both servers (%d, %d)", g.env.Src.ID, g.betaSource)
	}
	res, body = user.get(g.betaPath("/history", "source", itoa(g.betaSource)))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("history of beta = %d", res.StatusCode)
	}
	mustContain(t, "history of beta", body, "Beta files", "a.txt", "docs", "Server beta", `tv-point-now is-disabled`, "A restore is made on the server that owns the folder")
	mustNotContain(t, "history of beta", body, `action="/restore/preview"`, `href="/explorer?`)
	assertCSPClean(t, "history of beta", body)
	_, body = user.get("/history?source=beta:" + itoa(g.betaSource) + "&path=docs")
	mustContain(t, "history of beta, written server:number", body, "b.txt", "Beta files")
	// The other point of the timeline.
	_, body = user.get(g.betaPath("/history", "source", itoa(g.betaSource), "point", itoa(g.betaPoints[0])))
	mustContain(t, "an older point of beta", body, "Older point")

	// The versions of a file: recorded, not checked, and nothing to restore.
	res, body = user.get(g.betaPath("/versions", "source", itoa(g.betaSource), "path", "a.txt"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("versions of beta = %d", res.StatusCode)
	}
	mustContain(t, "versions of beta", body, "Recorded", "a.txt", `href="/history?`)
	mustNotContain(t, "versions of beta", body, `action="/restore/preview"`, "Content missing", `href="/explorer?`)
	// The same file of this server, which was never backed up, is not there under that number.
	if res, _ := user.get("/versions?source=" + itoa(g.betaSource) + "&path=a.txt"); res.StatusCode != http.StatusNotFound {
		t.Errorf("versions of this server's folder with the number of beta's = %d, want 404", res.StatusCode)
	}

	// The backups: the cards of beta have no button that starts something, and the log of its jobs is there.
	res, body = user.get(g.betaPath("/backups"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("backups of beta = %d", res.StatusCode)
	}
	mustContain(t, "backups of beta", body, "Beta files", "Beta secret", `class="tv-tab is-active"`, `data-job-server="beta"`)
	mustNotContain(t, "backups of beta", body, `action="/backups/run"`, `action="/backups/verify"`, "Restore settings")
	mustContain(t, "backups of beta", body, `href="/jobs/`+itoa(g.betaJob)+`?server=beta"`)

	// A job of beta: its text hides the real folders of beta.
	res, body = user.get(g.betaPath("/jobs/" + itoa(g.betaJob)))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("job of beta = %d", res.StatusCode)
	}
	mustContain(t, "job of beta", body, `\\beta\files\locked.txt`, `data-job-server`)
	mustNotContain(t, "job of beta", body, g.beta.SrcDir, `action="/jobs/`)
	// A job number of beta is not a job of this server.
	if res, _ := user.get("/jobs/" + itoa(g.betaJob)); res.StatusCode != http.StatusNotFound {
		t.Errorf("job of beta asked for on this server = %d, want 404", res.StatusCode)
	}

	// The audit log of beta: what is shown is the folders that this account may browse.
	res, body = user.get(g.betaPath("/audit"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("audit of beta = %d", res.StatusCode)
	}
	// The details of an event are shown as JSON, in which a backslash is written twice.
	asJSON := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	mustContain(t, "audit of beta", body, asJSON(`\\beta\files`), asJSON(`\\beta\secret`))
	mustNotContain(t, "audit of beta", body, g.beta.SrcDir, asJSON(g.beta.SrcDir), g.betaSecretRoot, asJSON(g.betaSecretRoot))
	other := g.visitor()
	other.loginAs(otherID)
	_, body = other.get(g.betaPath("/audit"))
	mustContain(t, "audit of beta for an account with no grant on the secret folder", body, asJSON(`\\beta\files`))
	mustNotContain(t, "audit of beta for an account with no grant on the secret folder", body,
		asJSON(`\\beta\secret`), g.betaSecretRoot, asJSON(g.betaSecretRoot), asJSON(g.beta.SrcDir))
}

func TestGroupPagesKeepToThePermissionsOfTheOtherServer(t *testing.T) {
	g := newGroup(t)
	seed(g.harness)

	guest := g.visitor()
	_, body := guest.get("/servers")
	mustContain(t, "servers for a guest", body, "beta")
	mustNotContain(t, "servers for a guest", body, "Database schema", g.betaSchema)
	_, body = guest.get(g.betaPath("/backups"))
	mustContain(t, "backups of beta for a guest", body, "Beta files")
	mustNotContain(t, "backups of beta for a guest", body, "Beta secret", g.beta.SrcDir)
	if res, _ := guest.get(g.betaPath("/history", "source", itoa(g.betaSecret))); res.StatusCode != http.StatusNotFound {
		t.Errorf("a folder of beta that a guest may not browse = %d, want 404", res.StatusCode)
	}
	if status, out := guest.api("GET", "/api/v1/sources?server=beta", nil); status != http.StatusOK || len(out["sources"].([]any)) != 1 {
		t.Errorf("sources of beta for a guest = %d %v", status, out)
	}

	// Somebody who has no grant on the secret folder of beta, and one who has.
	other := g.visitor()
	other.loginAs(otherID)
	if res, _ := other.get(g.betaPath("/history", "source", itoa(g.betaSecret))); res.StatusCode != http.StatusNotFound {
		t.Errorf("secret folder of beta for an account without a grant = %d, want 404", res.StatusCode)
	}
	user := g.visitor()
	user.loginAs(userID)
	res, body := user.get(g.betaPath("/history", "source", itoa(g.betaSecret)))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("secret folder of beta for the account that may browse it = %d", res.StatusCode)
	}
	mustContain(t, "secret folder of beta", body, "Beta secret")

	// An administrator of the user directory has full reach on the other server too, and sees its real folders.
	admin := g.visitor()
	admin.loginAs(adminID)
	_, body = admin.get(g.betaPath("/history", "source", itoa(g.betaSource)))
	mustContain(t, "history of beta for an administrator", body, "Beta files", g.beta.SrcDir)
	_, body = admin.get("/servers")
	mustContain(t, "servers for an administrator", body, "Database schema", g.betaSchema)

	// The local administrator of this server is not known on beta: it may read, and it sees no real folder of beta.
	local := g.visitor()
	local.login()
	res, body = local.get(g.betaPath("/history", "source", itoa(g.betaSecret)))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the local administrator reading the history of beta = %d", res.StatusCode)
	}
	_, body = local.get(g.betaPath("/history", "source", itoa(g.betaSource)))
	mustContain(t, "history of beta for the local administrator", body, `\\beta\files`)
	mustNotContain(t, "history of beta for the local administrator", body, g.beta.SrcDir)
	_, body = local.get(g.betaPath("/jobs/" + itoa(g.betaJob)))
	mustNotContain(t, "job of beta for the local administrator", body, g.beta.SrcDir)
	// ...and on its own folders it is still the administrator.
	_, body = local.get("/backups")
	mustContain(t, "backups of this server for the local administrator", body, `action="/backups/run"`)
}

func TestGroupRefusesToChangeAnythingOnAnotherServer(t *testing.T) {
	g := newGroup(t)
	seed(g.harness)
	user := g.visitor()
	user.loginAs(userID)
	localJobs := func() int {
		n, err := countRows(g.env.Pool, "jobs")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := localJobs()

	id := itoa(g.betaSource)
	form := url.Values{"source_id": {id}, "server": {"beta"}}
	for _, path := range []string{"/backups/run", "/backups/verify", "/restore/preview", "/restore/start", "/jobs/" + itoa(g.betaJob) + "/cancel", "/settings/sources/" + id} {
		if res, _ := user.postForm(path, form); res.StatusCode != http.StatusNotImplemented {
			t.Errorf("POST %s for another server = %d, want 501", path, res.StatusCode)
		}
	}
	if res, _ := user.get("/explorer?server=beta&source=" + id); res.StatusCode != http.StatusNotImplemented {
		t.Errorf("the live view of another server = %d, want 501", res.StatusCode)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/backups?server=beta"}, {"POST", "/api/v1/restores/preview?server=beta"}, {"POST", "/api/v1/restores?server=beta"},
		{"PATCH", "/api/v1/sources/" + id + "?server=beta"}, {"GET", "/api/v1/files?server=beta&source_id=" + id}, {"GET", "/api/v1/search?server=beta&source_id=" + id + "&q=a"},
	} {
		var payload any
		if c.method != "GET" {
			payload = map[string]any{"source_id": g.betaSource}
		}
		status, out := user.api(c.method, c.path, payload)
		if status != http.StatusNotImplemented {
			t.Errorf("%s %s = %d %v, want 501", c.method, c.path, status, out)
		}
	}
	if after := localJobs(); after != before {
		t.Fatalf("a request for another server queued %d job(s) on this one", after-before)
	}

	// The name of this server is this server: the same requests are not refused.
	if res, _ := user.get("/explorer?server=alpha&source=" + id); res.StatusCode != http.StatusOK {
		t.Errorf("the live view with the name of this server = %d, want 200", res.StatusCode)
	}
	if status, _ := user.api("POST", "/api/v1/backups?server=alpha", map[string]any{"source_id": g.env.Src.ID}); status != http.StatusAccepted && status != http.StatusOK {
		t.Errorf("a backup started with the name of this server = %d", status)
	}

	// A server that is not in the register is not found, on a page and in the API.
	if res, _ := user.get("/history?server=nobody"); res.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown server = %d, want 404", res.StatusCode)
	}
	if status, _ := user.api("GET", "/api/v1/sources?server=nobody", nil); status != http.StatusNotFound {
		t.Errorf("an unknown server in the API = %d, want 404", status)
	}
}

func TestGroupShowsServersThatAreOfflineOrNotRead(t *testing.T) {
	g := newGroup(t)
	seed(g.harness)
	user := g.visitor()
	user.loginAs(userID)

	// Offline: it has not reported for a while. The history is read all the same.
	g.sql(`UPDATE REG.servers SET last_seen = now() - interval '10 minutes' WHERE name = 'beta'`)
	_, body := user.get("/servers")
	mustContain(t, "/servers with beta offline", body, "Offline", "tv-chip-server-offline")
	if res, body := user.get(g.betaPath("/history")); res.StatusCode != http.StatusOK || !strings.Contains(body, "Beta files") {
		t.Errorf("history of an offline server = %d", res.StatusCode)
	}

	// Not reported yet, or another read contract: not read, and said so.
	for _, c := range []struct{ set, label string }{{"contract = 0", "Not reported yet"}, {"contract = 99", "Version not compatible"}} {
		g.sql(`UPDATE REG.servers SET ` + c.set + ` WHERE name = 'beta'`)
		_, body := user.get("/servers")
		mustContain(t, "/servers with "+c.set, body, c.label)
		res, body := user.get(g.betaPath("/history"))
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("history of a server with %s = %d, want 503", c.set, res.StatusCode)
		}
		mustContain(t, "history of a server with "+c.set, body, "history of that server is not available")
		if status, out := user.api("GET", "/api/v1/sources?server=beta", nil); status != http.StatusServiceUnavailable {
			t.Errorf("sources of a server with %s = %d %v, want 503", c.set, status, out)
		} else if errObj, _ := out["error"].(map[string]any); errObj["code"] != "server_not_readable" {
			t.Errorf("error code = %v", errObj)
		}
		// The tab of that server leads nowhere, and the page of this server still works.
		res, body = user.get("/backups")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("backups of this server = %d", res.StatusCode)
		}
		mustContain(t, "backups with the tab of a server that is not read", body, `tv-tab is-disabled`)
		// The folder selector skips it, without a word about it.
		_, body = user.get("/history")
		mustNotContain(t, "history with a server that is not read", body, `<optgroup label="beta">`)
	}
}

func TestGroupAPIReadsTheOtherServers(t *testing.T) {
	g := newGroup(t)
	seed(g.harness)
	user := g.visitor()
	user.loginAs(userID)

	status, out := user.api("GET", "/api/v1/servers", nil)
	if status != http.StatusOK || out["group"] != true || out["this_server"] != "alpha" {
		t.Fatalf("servers = %d %v", status, out)
	}
	servers := out["servers"].([]any)
	if len(servers) != 2 || servers[0].(map[string]any)["name"] != "alpha" || servers[0].(map[string]any)["this_server"] != true {
		t.Fatalf("this server must come first: %v", servers)
	}
	beta := servers[1].(map[string]any)
	if beta["name"] != "beta" || beta["state"] != "online" || beta["version"] != "1.1.0" || num(beta["folders"]) != 2 {
		t.Errorf("beta = %v", beta)
	}
	if _, ok := beta["schema"]; ok {
		t.Error("the schema of a server is for administrators")
	}
	admin := g.visitor()
	admin.loginAs(adminID)
	_, out = admin.api("GET", "/api/v1/servers", nil)
	if b := out["servers"].([]any)[1].(map[string]any); b["schema"] != g.betaSchema || b["repository_root"] != g.beta.Repo.Root() {
		t.Errorf("beta for an administrator = %v", b)
	}

	// Sources, points, the tree and the versions of a folder of beta.
	_, out = user.api("GET", "/api/v1/sources?server=beta", nil)
	if out["server"] != "beta" || len(out["sources"].([]any)) != 2 {
		t.Fatalf("sources of beta = %v", out)
	}
	_, out = user.api("GET", "/api/v1/recovery-points?server=beta&source_id="+itoa(g.betaSource), nil)
	if pts := out["recovery_points"].([]any); out["server"] != "beta" || len(pts) != 2 {
		t.Fatalf("points of beta = %v", out)
	}
	_, out = user.api("GET", "/api/v1/history/tree?server=beta&source_id="+itoa(g.betaSource)+"&point_id="+itoa(g.betaPoints[1]), nil)
	names := map[string]bool{}
	for _, e := range out["entries"].([]any) {
		names[e.(map[string]any)["name"].(string)] = true
	}
	if out["server"] != "beta" || !names["a.txt"] || !names["docs"] {
		t.Fatalf("tree of beta = %v", out)
	}
	_, out = user.api("GET", "/api/v1/history/versions?server=beta&source_id="+itoa(g.betaSource)+"&path=a.txt", nil)
	if vs := out["versions"].([]any); out["server"] != "beta" || out["content_checked"] != false || len(vs) != 2 {
		t.Fatalf("versions of beta = %v", out)
	} else if !vs[0].(map[string]any)["object_present"].(bool) {
		t.Error("a recorded version is reported as present when the content was not checked")
	}
	_, out = user.api("GET", "/api/v1/jobs/"+itoa(g.betaJob)+"?server=beta", nil)
	if job := out["job"].(map[string]any); out["server"] != "beta" || job["state"] != "failed" || strings.Contains(job["error"].(string), g.beta.SrcDir) {
		t.Fatalf("job of beta = %v", out)
	}
	_, out = user.api("GET", "/api/v1/audit?server=beta", nil)
	if evs := out["events"].([]any); out["server"] != "beta" || len(evs) != 2 {
		t.Fatalf("audit of beta = %v", out)
	}
	// This server's own answers carry no server, as they always did.
	_, out = user.api("GET", "/api/v1/sources", nil)
	if _, ok := out["server"]; ok {
		t.Errorf("an answer about this server names a server: %v", out)
	}
}

func TestAServerOnItsOwnHasNoServersPage(t *testing.T) {
	h := newHarness(t)
	seed(h)
	user := h.visitor()
	user.loginAs(userID)
	if res, _ := user.get("/servers"); res.StatusCode != http.StatusNotFound {
		t.Errorf("/servers on a server that is on its own = %d, want 404", res.StatusCode)
	}
	if status, out := user.api("GET", "/api/v1/servers", nil); status != http.StatusOK || out["group"] != false {
		t.Errorf("api servers = %d %v", status, out)
	}
	_, body := user.get("/backups")
	mustNotContain(t, "backups of a server on its own", body, `href="/servers"`, "tv-scope")
	if res, _ := user.get("/history?server=beta"); res.StatusCode != http.StatusNotFound {
		t.Errorf("a server name on a server that is on its own = %d, want 404", res.StatusCode)
	}
	// The name that would be this server's own is not special without a group.
	if res, _ := user.get("/explorer?server=beta"); res.StatusCode != http.StatusNotImplemented {
		t.Errorf("explorer with a server name = %d, want 501", res.StatusCode)
	}
}

func countRows(pool *pgxpool.Pool, table string) (int, error) {
	var n int
	err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n)
	return n, err
}
