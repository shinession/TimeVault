package server_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"timevault/internal/auth"
)

// The accounts of the fake directory: an ordinary one, an administrator and a second ordinary one.
const (
	userID  = "E1001"
	adminID = "E2002"
	otherID = "E5005"
)

type fakeAccount struct {
	ID, Name, Password string
	Role               auth.Role
	Inactive           bool
}

func (a fakeAccount) identity() auth.Identity {
	return auth.Identity{ID: a.ID, Name: a.Name, Role: a.Role, Ref: "app_user_" + strings.ToLower(a.ID)}
}

// fakeDirectory is the company user directory of the tests. It answers the way
// the real one does: wrong passwords and deactivated accounts are refused, and
// it can stop answering.
type fakeDirectory struct {
	mu       sync.Mutex
	accounts map[string]fakeAccount
	down     bool
}

func newFakeDirectory() *fakeDirectory {
	d := &fakeDirectory{accounts: map[string]fakeAccount{}}
	d.put(fakeAccount{ID: userID, Name: "Alice Wong", Password: "alice-secret", Role: auth.RoleUser})
	d.put(fakeAccount{ID: adminID, Name: "Bob Chen", Password: "bob-secret", Role: auth.RoleAdmin})
	d.put(fakeAccount{ID: otherID, Name: "Carol Diaz", Password: "carol-secret", Role: auth.RoleUser})
	return d
}

func (d *fakeDirectory) put(a fakeAccount) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accounts[a.ID] = a
}

func (d *fakeDirectory) change(id string, f func(*fakeAccount)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a := d.accounts[id]
	f(&a)
	d.accounts[id] = a
}

func (d *fakeDirectory) setDown(down bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.down = down
}

func (d *fakeDirectory) password(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.accounts[id].Password
}

// find accepts the employee ID in any case or the name, like the real directory.
func (d *fakeDirectory) find(login string) (fakeAccount, bool) {
	if login == "" {
		return fakeAccount{}, false
	}
	for _, a := range d.accounts {
		if strings.EqualFold(a.ID, login) || a.Name == login {
			return a, true
		}
	}
	return fakeAccount{}, false
}

func (d *fakeDirectory) Authenticate(_ context.Context, login, pw string) (auth.Identity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.down {
		return auth.Identity{}, auth.ErrUnavailable
	}
	a, ok := d.find(login)
	if !ok || a.Inactive || a.Password != pw {
		return auth.Identity{}, auth.ErrInvalidCredentials
	}
	return a.identity(), nil
}

func (d *fakeDirectory) Refresh(_ context.Context, id auth.Identity) (auth.Identity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.down {
		return auth.Identity{}, auth.ErrUnavailable
	}
	a, ok := d.accounts[id.ID]
	if !ok || a.Inactive {
		return auth.Identity{}, auth.ErrInvalidCredentials
	}
	return a.identity(), nil
}

func (d *fakeDirectory) Ping(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.down {
		return auth.ErrUnavailable
	}
	return nil
}

func (d *fakeDirectory) Addr() string { return "directory.test:6379" }

// testClock is the time the sign-in manager sees, so a test can let a minute pass.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// forwarded lets a test pretend to be another client address: the sign-in throttle counts by it.
func forwarded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.Header.Get("X-Test-Client"); c != "" {
			r.RemoteAddr = c + ":50000"
		}
		next.ServeHTTP(w, r)
	})
}

func TestAnonymousVisitorsBrowseButCannotTouchData(t *testing.T) {
	h := newHarness(t)
	seed(h)
	srcID := h.env.Src.ID
	src := itoa(srcID)

	alice := h.visitor()
	alice.loginAs(userID)
	status, out := alice.api("POST", "/api/v1/backups", map[string]any{"source_id": srcID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	backupJob := num(out["job"].(map[string]any)["id"])
	alice.waitJob(backupJob)
	_, out = alice.api("GET", "/api/v1/recovery-points?source_id="+src, nil)
	pointID := num(out["recovery_points"].([]any)[0].(map[string]any)["id"])

	// An ordinary account restores too; the restore job is for signed-in accounts to look at.
	req := map[string]any{"source_id": srcID, "point_id": pointID, "paths": []string{"a.txt"}, "dest_name": "by-alice", "policy": "skip"}
	status, preview := alice.api("POST", "/api/v1/restores/preview", req)
	if status != http.StatusOK {
		t.Fatalf("restore preview by an ordinary account = %d %v", status, preview)
	}
	req["confirmation_token"], req["idempotency_key"] = preview["confirmation_token"], "alice-1"
	status, started := alice.api("POST", "/api/v1/restores", req)
	if status != http.StatusAccepted {
		t.Fatalf("restore by an ordinary account = %d %v", status, started)
	}
	restoreJob := num(started["job"].(map[string]any)["id"])
	if job := alice.waitJob(restoreJob); job["state"] != "succeeded" {
		t.Fatalf("restore job = %v", job)
	}
	if got := h.env.ReadRestored("by-alice/a.txt"); got != "alpha" {
		t.Fatalf("restored content = %q", got)
	}

	anon := h.visitor()

	// Everything that only shows data is open, without offering what needs an account.
	for _, page := range []string{
		"/explorer?source=" + src, "/history?source=" + src + "&point=" + itoa(pointID),
		"/versions?source=" + src + "&path=a.txt", "/backups", "/jobs/" + itoa(backupJob),
	} {
		res, body := anon.get(page)
		if res.StatusCode != http.StatusOK {
			t.Errorf("anonymous %s = %d, want 200", page, res.StatusCode)
			continue
		}
		if !strings.Contains(body, "tv-signin") {
			t.Errorf("%s offers no way to sign in", page)
		}
		for _, offered := range []string{`href="/audit"`, `href="/settings"`, `href="/status"`, `action="/logout"`, `action="/backups/run"`,
			`action="/backups/verify"`, `action="/restore/preview"`, `name="paths"`, userID,
			h.env.SrcDir,                  // the folders on the server's disk are for accounts to see
			`/jobs/` + itoa(restoreJob)} { // so are the restores
			if strings.Contains(body, offered) {
				t.Errorf("%s shows %q to an anonymous visitor", page, offered)
			}
		}
		assertCSPClean(t, page, body)
	}
	if _, body := anon.get("/history?source=" + src); !strings.Contains(body, "Sign in to restore files") || !strings.Contains(body, "a.txt") {
		t.Errorf("the history of an anonymous visitor shows the files and a way to sign in:\n%s", body)
	}
	if res, body := anon.get("/"); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/explorer" {
		t.Errorf("anonymous / = %d %q %s", res.StatusCode, res.Header.Get("Location"), body)
	}
	for _, path := range []string{
		"/api/v1/sources", "/api/v1/files?source_id=" + src, "/api/v1/recovery-points?source_id=" + src,
		"/api/v1/history/tree?source_id=" + src + "&point_id=" + itoa(pointID), "/api/v1/history/versions?source_id=" + src + "&path=a.txt",
		"/api/v1/jobs/" + itoa(backupJob),
	} {
		if status, out := anon.api("GET", path, nil); status != http.StatusOK {
			t.Errorf("anonymous GET %s = %d %v, want 200", path, status, out)
		}
	}
	if status, out := anon.api("GET", "/api/v1/session", nil); status != http.StatusOK || out["authenticated"] != false || out["csrf_token"] != nil {
		t.Errorf("anonymous session = %d %v", status, out)
	}
	if _, raw := anon.get("/api/v1/sources"); strings.Contains(raw, "root_path") {
		t.Errorf("the folders on the server's disk are not for anonymous visitors: %s", raw)
	}
	if _, raw := alice.get("/api/v1/sources"); !strings.Contains(raw, "root_path") {
		t.Errorf("a signed-in account sees the folder of a source: %s", raw)
	}
	if _, body := alice.get("/backups"); !strings.Contains(body, h.env.SrcDir) || !strings.Contains(body, "/jobs/"+itoa(restoreJob)) {
		t.Errorf("a signed-in account sees the folders and the restores on the Backups page")
	}

	// Who asked for a backup is for signed-in accounts to know.
	_, job := anon.api("GET", "/api/v1/jobs/"+itoa(backupJob), nil)
	if by := job["job"].(map[string]any)["requested_by"]; by != "" {
		t.Errorf("an anonymous visitor learns who requested the job: %v", by)
	}
	if _, job := alice.api("GET", "/api/v1/jobs/"+itoa(backupJob), nil); job["job"].(map[string]any)["requested_by"] != userID {
		t.Errorf("a signed-in account does not see who requested the job: %v", job)
	}

	// A restore is not for passers-by to look at.
	if res, _ := anon.get("/jobs/" + itoa(restoreJob)); res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login?next=") {
		t.Errorf("anonymous restore job page = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if status, out := anon.api("GET", "/api/v1/jobs/"+itoa(restoreJob), nil); status != http.StatusUnauthorized {
		t.Errorf("anonymous restore job API = %d %v, want 401", status, out)
	}

	// What shows the inside of the service needs an account.
	for _, page := range []string{"/audit", "/status", "/settings"} {
		res, _ := anon.get(page)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next="+url.QueryEscape(page) {
			t.Errorf("anonymous %s = %d %q", page, res.StatusCode, res.Header.Get("Location"))
		}
	}
	for _, path := range []string{"/api/v1/audit", "/api/v1/health", "/api/v1/settings"} {
		if status, _ := anon.api("GET", path, nil); status != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", path, status)
		}
	}

	// Anything that changes something needs an account, whatever way it is asked.
	for _, path := range []string{"/backups/run", "/backups/verify", "/jobs/" + itoa(backupJob) + "/cancel", "/restore/preview", "/restore/start",
		"/settings/sources", "/settings/sources/" + src, "/logout"} {
		res, _ := anon.postForm(path, url.Values{"source_id": {src}, "name": {"Evil"}, "root_path": {t.TempDir()}})
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
			t.Errorf("anonymous POST %s = %d %q, want a redirect to /login", path, res.StatusCode, res.Header.Get("Location"))
		}
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/backups", map[string]any{"source_id": srcID}},
		{"POST", "/api/v1/restores/preview", req},
		{"POST", "/api/v1/restores", req},
		{"POST", "/api/v1/sources", map[string]any{"name": "Evil", "root_path": t.TempDir()}},
		{"PATCH", "/api/v1/sources/" + src, map[string]any{"enabled": false}},
	} {
		if status, out := anon.api(c.method, c.path, c.body); status != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d %v, want 401", c.method, c.path, status, out)
		}
	}

	// None of it did anything.
	_, out = alice.api("GET", "/api/v1/sources", nil)
	sources := out["sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["enabled"] != true {
		t.Errorf("sources after the anonymous attempts = %v", sources)
	}
	if status, _ := alice.api("GET", "/api/v1/jobs/"+itoa(restoreJob+1), nil); status != http.StatusNotFound {
		t.Errorf("an anonymous attempt created a job (%d)", status)
	}
}

func TestWhyABackupFailedIsForSignedInAccountsOnly(t *testing.T) {
	h := newHarness(t)
	seed(h)
	alice, anon := h.visitor(), h.visitor()
	alice.loginAs(userID)

	// A folder that has gone missing makes the next backup fail with a message that names it.
	moved := h.env.SrcDir + "-moved"
	if err := os.Rename(h.env.SrcDir, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(moved, h.env.SrcDir) })
	status, out := alice.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	id := num(out["job"].(map[string]any)["id"])
	if job := alice.waitJob(id); job["state"] != "failed" || job["error"] == "" {
		t.Fatalf("the backup of a missing folder = %v", job)
	}

	_, seen := alice.api("GET", "/api/v1/jobs/"+itoa(id), nil)
	if reason, _ := seen["job"].(map[string]any)["error"].(string); reason == "" || !strings.Contains(reason, h.env.SrcDir) {
		t.Errorf("a signed-in account sees the reason: %v", seen)
	}
	_, hidden := anon.api("GET", "/api/v1/jobs/"+itoa(id), nil)
	hiddenJob := hidden["job"].(map[string]any)
	if events, _ := hidden["events"].([]any); len(events) != 0 || hiddenJob["error"] != nil || hiddenJob["state"] != "failed" {
		t.Errorf("an anonymous visitor sees the state, not the reason or the log: %v", hidden)
	}
	_, raw := anon.get("/api/v1/jobs/" + itoa(id))
	for _, leak := range []string{strings.ReplaceAll(h.env.SrcDir, `\`, `\\`), "not accessible", "GetFileAttributesEx", userID} {
		if strings.Contains(raw, leak) {
			t.Errorf("the job an anonymous visitor gets still contains %q: %s", leak, raw)
		}
	}

	_, body := alice.get("/jobs/" + itoa(id))
	if !strings.Contains(body, `data-job-field="error"`) || !strings.Contains(body, "data-job-log") {
		t.Errorf("job page of a signed-in account lacks the reason or the log:\n%s", body)
	}
	_, body = anon.get("/jobs/" + itoa(id))
	if strings.Contains(body, `data-job-field="error"`) || strings.Contains(body, "data-job-log") || !strings.Contains(body, "Sign in to see the reason") {
		t.Errorf("job page of an anonymous visitor:\n%s", body)
	}
}

func TestADeactivatedAccountLosesTheDetailsOfPublicPages(t *testing.T) {
	h := newHarness(t)
	seed(h)
	alice := h.visitor()
	alice.loginAs(userID)
	status, out := alice.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	id := num(out["job"].(map[string]any)["id"])
	alice.waitJob(id)
	if _, job := alice.api("GET", "/api/v1/jobs/"+itoa(id), nil); job["job"].(map[string]any)["requested_by"] != userID {
		t.Fatalf("a signed-in account sees who asked: %v", job)
	}

	// The cookie is still in the browser, but the pages open to everyone ask the directory before they show more than anyone sees.
	h.dir.change(userID, func(a *fakeAccount) { a.Inactive = true })
	h.clock.Advance(2 * time.Minute)
	if _, job := alice.api("GET", "/api/v1/jobs/"+itoa(id), nil); job["job"].(map[string]any)["requested_by"] != "" {
		t.Errorf("a deactivated account still sees who asked: %v", job)
	}
	if _, body := alice.get("/backups"); strings.Contains(body, userID) || strings.Contains(body, h.env.SrcDir) {
		t.Errorf("a deactivated account still sees what only accounts see:\n%s", body)
	}
	if _, body := alice.get("/backups"); !strings.Contains(body, "tv-signin") {
		t.Errorf("the page of an ended session offers the sign-in button")
	}
}

func TestRolesDecideWhoMayManageMonitoredFolders(t *testing.T) {
	h := newHarness(t)
	seed(h)
	src := itoa(h.env.Src.ID)
	alice, bob := h.visitor(), h.visitor()
	alice.loginAs(userID)
	bob.loginAs(adminID)

	// An ordinary account may look at the settings and at the audit log, but not change the folders.
	res, body := alice.get("/settings")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Only an administrator can change the monitored folders.") ||
		strings.Contains(body, `action="/settings/sources`) || !strings.Contains(body, "Alice Wong") {
		t.Errorf("settings page of an ordinary account = %d\n%s", res.StatusCode, body)
	}
	for _, page := range []string{"/audit", "/status"} {
		if res, _ := alice.get(page); res.StatusCode != http.StatusOK {
			t.Errorf("an ordinary account gets %s = %d", page, res.StatusCode)
		}
	}
	res, body = alice.postForm("/settings/sources", url.Values{"name": {"Evil"}, "root_path": {t.TempDir()}})
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "You are not allowed to do this.") {
		t.Errorf("an ordinary account adds a folder: %d %s", res.StatusCode, body)
	}
	if res, _ := alice.postForm("/settings/sources/"+src, url.Values{"name": {"Evil"}, "root_path": {h.env.SrcDir}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("an ordinary account edits a folder: %d", res.StatusCode)
	}
	if status, out := alice.api("POST", "/api/v1/sources", map[string]any{"name": "Evil", "root_path": t.TempDir()}); status != http.StatusForbidden || out["error"].(map[string]any)["code"] != "forbidden" {
		t.Errorf("an ordinary account adds a folder through the API: %d %v", status, out)
	}
	if status, _ := alice.api("PATCH", "/api/v1/sources/"+src, map[string]any{"enabled": false}); status != http.StatusForbidden {
		t.Errorf("an ordinary account edits a folder through the API: %d", status)
	}
	_, out := bob.api("GET", "/api/v1/audit?action=access.denied&actor="+userID, nil)
	if events, _ := out["events"].([]any); len(events) != 4 {
		t.Errorf("the four refused changes belong in the audit log, got %v", out)
	}
	if _, out := bob.api("GET", "/api/v1/sources", nil); len(out["sources"].([]any)) != 1 {
		t.Errorf("a refused account changed the folders: %v", out)
	}

	// Backing up and restoring are for every signed-in account.
	if status, out := alice.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID}); status != http.StatusAccepted {
		t.Errorf("an ordinary account starts a backup: %d %v", status, out)
	}

	// An administrator changes the list.
	res, body = bob.get("/settings")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `action="/settings/sources"`) || !strings.Contains(body, `action="/settings/sources/`+src+`"`) {
		t.Errorf("settings page of an administrator = %d", res.StatusCode)
	}
	other := t.TempDir()
	if res, _ := bob.postForm("/settings/sources", url.Values{"name": {"Docs"}, "root_path": {other}, "schedule": {"1"}, "schedule_minutes": {"15"}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("an administrator adds a folder: %d", res.StatusCode)
	}
	_, out = bob.api("GET", "/api/v1/sources", nil)
	var docs map[string]any
	for _, s := range out["sources"].([]any) {
		if m := s.(map[string]any); m["name"] == "Docs" {
			docs = m
		}
	}
	if docs == nil || docs["schedule_minutes"] != float64(15) || docs["root_path"] != other {
		t.Fatalf("the folder an administrator added = %v", out)
	}
	_, out = alice.api("GET", "/api/v1/audit?action=source.create", nil)
	if events, _ := out["events"].([]any); len(events) != 1 || events[0].(map[string]any)["actor"] != adminID {
		t.Errorf("the new folder is attributed to the administrator: %v", out)
	}
}

func TestAdministratorsEditMonitoredFoldersAndSchedules(t *testing.T) {
	h := newHarness(t)
	seed(h)
	bob, anon := h.visitor(), h.visitor()
	bob.loginAs(adminID)
	srcID := h.env.Src.ID
	id := itoa(srcID)

	_, body := bob.get("/settings")
	for _, want := range []string{"Team Files", h.env.Repo.Root(), h.env.RestoreRoot, "127.0.0.1:8660", "db.test:5432/TimeVault", "directory.test:6379/0", "Manual only"} {
		if !strings.Contains(body, want) {
			t.Errorf("the settings page is missing %q", want)
		}
	}
	assertCSPClean(t, "/settings", body)

	// The edit form: switch the schedule on, then off again.
	form := url.Values{"name": {"Team Files"}, "root_path": {h.env.SrcDir}, "excludes": {"*.tmp\n"}, "enabled": {"1"}, "schedule": {"1"}, "schedule_minutes": {"15"}}
	if res, _ := bob.postForm("/settings/sources/"+id, form); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings" {
		t.Fatalf("edit = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	_, body = bob.get("/settings")
	if !strings.Contains(body, "Changes saved.") || !strings.Contains(body, "Every 15 minute(s)") {
		t.Errorf("settings page after the edit:\n%s", body)
	}
	if _, body := anon.get("/backups"); !strings.Contains(body, "every 15 min") || !strings.Contains(body, "next backup") {
		t.Errorf("everybody sees the schedule on the backups page:\n%s", body)
	}
	delete(form, "schedule")
	if res, _ := bob.postForm("/settings/sources/"+id, form); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit = %d", res.StatusCode)
	}
	if _, body := bob.get("/settings"); !strings.Contains(body, "Manual only") || strings.Contains(body, "Every 15 minute(s)") {
		t.Errorf("a source with no schedule is manual only:\n%s", body)
	}

	// A rejected edit shows the form again with the reason and what was typed.
	form.Set("schedule", "1")
	form.Set("schedule_minutes", "0")
	form.Set("name", "Renamed")
	res, body := bob.postForm("/settings/sources/"+id, form)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "tv-alert-error") || !strings.Contains(body, `value="Renamed"`) {
		t.Errorf("a bad interval is answered with %d\n%s", res.StatusCode, body)
	}
	if res, _ := bob.postForm("/settings/sources/999", form); res.StatusCode != http.StatusNotFound {
		t.Errorf("editing a folder that does not exist = %d", res.StatusCode)
	}

	// The JSON API changes only what it is given.
	patch := func(body map[string]any) (int, map[string]any) { return bob.api("PATCH", "/api/v1/sources/"+id, body) }
	if status, out := patch(map[string]any{"schedule_minutes": 30}); status != http.StatusOK || out["schedule_minutes"] != float64(30) || out["name"] != "Team Files" || out["enabled"] != true {
		t.Errorf("setting a schedule = %d %v", status, out)
	}
	if status, out := patch(map[string]any{"name": "Team"}); status != http.StatusOK || out["schedule_minutes"] != float64(30) || out["name"] != "Team" {
		t.Errorf("a rename must keep the schedule: %d %v", status, out)
	}
	if status, out := patch(map[string]any{"schedule_minutes": nil}); status != http.StatusOK || out["schedule_minutes"] != nil {
		t.Errorf("an explicit null removes the schedule: %d %v", status, out)
	}
	for name, body := range map[string]map[string]any{
		"interval of zero": {"schedule_minutes": 0}, "interval beyond a month": {"schedule_minutes": 43201}, "interval that is not a number": {"schedule_minutes": "often"},
		"unknown field": {"surprise": true}, "empty name": {"name": " "},
	} {
		if status, out := patch(body); status != http.StatusBadRequest {
			t.Errorf("%s = %d %v, want 400", name, status, out)
		}
	}
	if status, _ := bob.api("PATCH", "/api/v1/sources/999", map[string]any{"enabled": false}); status != http.StatusNotFound {
		t.Errorf("patching a folder that does not exist = %d", status)
	}

	// Once a folder has history, its path is fixed; the rest can still change.
	h.env.Backup()
	elsewhere := t.TempDir()
	if status, out := patch(map[string]any{"root_path": elsewhere}); status != http.StatusBadRequest {
		t.Errorf("moving a folder that has backups = %d %v, want 400", status, out)
	}
	form.Set("root_path", elsewhere)
	form.Set("schedule_minutes", "20")
	if res, body := bob.postForm("/settings/sources/"+id, form); res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "tv-alert-error") {
		t.Errorf("moving a folder that has backups through the form = %d", res.StatusCode)
	}
	if status, out := patch(map[string]any{"root_path": h.env.SrcDir, "enabled": false}); status != http.StatusOK || out["enabled"] != false {
		t.Errorf("switching a folder off keeps its path: %d %v", status, out)
	}
	if status, out := bob.api("POST", "/api/v1/backups", map[string]any{"source_id": srcID}); status != http.StatusConflict || out["error"].(map[string]any)["code"] != "source_disabled" {
		t.Errorf("backing up a switched-off folder through the API = %d %v, want 409", status, out)
	}
	if res, body := bob.postForm("/backups/run", url.Values{"source_id": {id}}); res.StatusCode != http.StatusConflict || !strings.Contains(body, "is switched off") {
		t.Errorf("backing up a switched-off folder through the form = %d\n%s", res.StatusCode, body)
	}
	if _, body := anon.get("/explorer?source=" + id); !strings.Contains(body, "a.txt") {
		t.Errorf("a switched-off folder keeps its history for everybody to browse:\n%s", body)
	}

	_, out := bob.api("GET", "/api/v1/audit?action=source.update", nil)
	events, _ := out["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("source changes are not audited: %v", out)
	}
	newest := events[0].(map[string]any)
	if newest["actor"] != adminID || newest["detail"].(map[string]any)["enabled"] == nil {
		t.Errorf("the newest change should name the administrator and what changed: %v", newest)
	}
}

func TestSignInThroughTheUserDirectory(t *testing.T) {
	h := newHarness(t)
	h.dir.put(fakeAccount{ID: "E3003", Name: "Gone Guy", Password: "gone-secret", Role: auth.RoleUser, Inactive: true})
	h.dir.put(fakeAccount{ID: "E4004", Name: "admin", Password: "directory-admin", Role: auth.RoleAdmin})

	// The employee ID in any case, or the name, opens the same account.
	for _, login := range []string{userID, strings.ToLower(userID), "Alice Wong"} {
		v := h.visitor()
		if status := v.signIn(login, "alice-secret"); status != http.StatusSeeOther {
			t.Errorf("sign-in as %q = %d", login, status)
			continue
		}
		_, out := v.api("GET", "/api/v1/session", nil)
		if out["user"] != userID || out["name"] != "Alice Wong" || out["role"] != "user" {
			t.Errorf("session of %q = %v", login, out)
		}
		if _, body := v.get("/backups"); !strings.Contains(body, "Alice Wong") || strings.Contains(body, "tv-signin") {
			t.Errorf("the header must show who is signed in")
		}
	}
	v := h.visitor()
	if status := v.signIn(adminID, "bob-secret"); status != http.StatusSeeOther {
		t.Fatalf("sign-in of an administrator = %d", status)
	}
	if _, out := v.api("GET", "/api/v1/session", nil); out["role"] != "admin" {
		t.Errorf("an administrator of the directory is an administrator here: %v", out)
	}

	// Wrong password, unknown account and deactivated account are told apart by nobody.
	for name, c := range map[string][2]string{"wrong password": {userID, "nope"}, "unknown account": {"E9999", "alice-secret"}, "deactivated account": {"E3003", "gone-secret"}} {
		res, body := h.visitor().postForm("/login", url.Values{"username": {c[0]}, "password": {c[1]}})
		if res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Invalid account or password") {
			t.Errorf("%s = %d", name, res.StatusCode)
		}
	}

	// The local administrator's name belongs to the local administrator, whatever the directory holds under that name.
	if status := h.visitor().signIn("admin", "directory-admin"); status != http.StatusUnauthorized {
		t.Errorf("the directory answered for the name of the local administrator: %d", status)
	}
	if status := h.visitor().signIn("admin", password); status != http.StatusSeeOther {
		t.Errorf("the local administrator cannot sign in: %d", status)
	}

	// Failures are on record, but a long login is not kept whole: it may be a password typed into the wrong box.
	long := strings.Repeat("x", 200)
	if res, _ := h.visitor().postForm("/login", url.Values{"username": {long}, "password": {"nope"}}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a very long login = %d", res.StatusCode)
	}
	admin := h.visitor()
	admin.login()
	_, out := admin.api("GET", "/api/v1/audit?action=auth.login_failed&limit=500", nil)
	var seen bool
	for _, e := range out["events"].([]any) {
		actor := e.(map[string]any)["actor"].(string)
		if len(actor) > 64 {
			t.Errorf("an audit record keeps a login of %d characters", len(actor))
		}
		seen = seen || strings.HasPrefix(actor, "xxxx")
	}
	if !seen {
		t.Errorf("the failed sign-in with a long login is not audited: %v", out)
	}
}

func TestOneAccountCannotBeGuessedAtFromManyAddresses(t *testing.T) {
	h := newHarness(t)
	// Each failure comes from an address the per-address limit has not seen, as a guesser with many addresses would.
	var last int
	for i := 0; i < 12; i++ {
		res, _ := h.visitor().postForm("/login", url.Values{"username": {userID}, "password": {"guess-" + itoa(int64(i))}}, map[string]string{"X-Test-Client": "10.0.0." + itoa(int64(i))})
		last = res.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("the twelfth failed attempt on one account = %d, want 429", last)
	}
	res, _ := h.visitor().postForm("/login", url.Values{"username": {userID}, "password": {h.dir.password(userID)}}, map[string]string{"X-Test-Client": "10.9.9.9"})
	if res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the right password during the lock-out = %d, want 429", res.StatusCode)
	}
	// The lock-out is about that one account.
	if status := h.visitor().signIn(adminID, "bob-secret"); status != http.StatusSeeOther {
		t.Errorf("another account is locked out too: %d", status)
	}
}

func TestSessionsFollowTheAccountInTheDirectory(t *testing.T) {
	h := newHarness(t)
	srcID := h.env.Src.ID
	alice, bob, carol := h.visitor(), h.visitor(), h.visitor()
	alice.loginAs(userID)
	bob.loginAs(adminID)
	carol.loginAs(otherID)

	// Probes that change nothing: one any signed-in account may make, and one only an administrator may.
	signedIn := func(v *harness) int { status, _ := v.api("GET", "/api/v1/settings", nil); return status }
	manage := func(v *harness) int {
		status, _ := v.api("PATCH", "/api/v1/sources/"+itoa(srcID), map[string]any{})
		return status
	}

	// A deactivated account is noticed when the directory is asked again, which is once a minute.
	if got := signedIn(alice); got != http.StatusOK {
		t.Fatalf("an active account = %d", got)
	}
	h.dir.change(userID, func(a *fakeAccount) { a.Inactive = true })
	if got := signedIn(alice); got != http.StatusOK {
		t.Errorf("the directory's answer holds for a minute, got %d", got)
	}
	h.clock.Advance(2 * time.Minute)
	if got := signedIn(alice); got != http.StatusUnauthorized {
		t.Errorf("a deactivated account after the answer expired = %d, want 401", got)
	}
	if res, _ := alice.get("/login"); res.StatusCode != http.StatusOK {
		t.Errorf("the session of a deactivated account must be ended so the sign-in form shows, got %d", res.StatusCode)
	}
	if status := h.visitor().signIn(userID, "alice-secret"); status != http.StatusUnauthorized {
		t.Errorf("a deactivated account signs in again: %d", status)
	}

	// A demotion and a promotion take effect the same way.
	if got := manage(bob); got != http.StatusOK {
		t.Fatalf("an administrator = %d", got)
	}
	if got := manage(carol); got != http.StatusForbidden {
		t.Fatalf("an ordinary account = %d", got)
	}
	h.dir.change(adminID, func(a *fakeAccount) { a.Role = auth.RoleUser })
	h.dir.change(otherID, func(a *fakeAccount) { a.Role = auth.RoleAdmin })
	h.clock.Advance(2 * time.Minute)
	if got := manage(bob); got != http.StatusForbidden {
		t.Errorf("a demoted administrator = %d, want 403", got)
	}
	if got := manage(carol); got != http.StatusOK {
		t.Errorf("a promoted account = %d, want 200", got)
	}
}

func TestDirectoryOutageKeepsBrowsingAndOpenSessionsWorking(t *testing.T) {
	h := newHarness(t)
	seed(h)
	alice, bob := h.visitor(), h.visitor()
	alice.loginAs(userID)
	bob.login() // the local administrator
	h.dir.setDown(true)

	// Signing in as a company account fails with the reason. Nobody's account is blamed for it, so it
	// can be tried again as soon as the directory is back; the address that keeps trying is held back.
	for i := 0; i < 12; i++ {
		res, body := h.visitor().postForm("/login", url.Values{"username": {userID}, "password": {"alice-secret"}})
		if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "Browsing still works") {
			t.Fatalf("sign-in while the directory is down = %d", res.StatusCode)
		}
	}
	patient := h.visitor()
	for i := 0; i < 5; i++ {
		if res, _ := patient.postForm("/login", url.Values{"username": {userID}, "password": {"alice-secret"}}); res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("attempt %d from one address while the directory is down = %d", i, res.StatusCode)
		}
	}
	if res, _ := patient.postForm("/login", url.Values{"username": {userID}, "password": {"alice-secret"}}); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("an address that keeps asking the silent directory = %d, want 429", res.StatusCode)
	}
	if status := h.visitor().signIn("admin", password); status != http.StatusSeeOther {
		t.Errorf("the local administrator cannot sign in while the directory is down: %d", status)
	}

	// Everybody can still browse, and the status page says what is wrong.
	if res, _ := h.visitor().get("/backups"); res.StatusCode != http.StatusOK {
		t.Errorf("browsing while the directory is down = %d", res.StatusCode)
	}
	if _, body := bob.get("/status"); !strings.Contains(body, "Does not answer") {
		t.Errorf("the status page does not report the silent directory:\n%s", body)
	}
	if status, out := bob.api("GET", "/api/v1/health", nil); status != http.StatusOK {
		t.Errorf("a silent user directory must not make the service unhealthy: %d %v", status, out)
	}

	// An account that is already signed in keeps working, because the directory cannot say otherwise.
	h.clock.Advance(2 * time.Minute)
	status, out := alice.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("a signed-in account while the directory is down: %d %v", status, out)
	}
	alice.waitJob(num(out["job"].(map[string]any)["id"]))

	// Once the directory is back, the account that was asked for during the outage signs in at once, and what the directory says counts again.
	h.dir.setDown(false)
	if status := h.visitor().signIn(userID, "alice-secret"); status != http.StatusSeeOther {
		t.Errorf("the account was blamed for the outage: sign-in = %d", status)
	}
	h.dir.change(userID, func(a *fakeAccount) { a.Inactive = true })
	h.clock.Advance(time.Minute)
	if status, _ := alice.api("GET", "/api/v1/settings", nil); status != http.StatusUnauthorized {
		t.Errorf("a deactivated account once the directory is back = %d, want 401", status)
	}
	if status := h.visitor().signIn(adminID, "bob-secret"); status != http.StatusSeeOther {
		t.Errorf("sign-in after the directory is back = %d", status)
	}
}
