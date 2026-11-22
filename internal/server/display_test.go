package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"timevault/internal/config"
	"timevault/internal/service"
)

const (
	testHost       = "tv.example.test"
	testSourceUNC  = `\\tv.example.test\TimeVault\monitor`
	testRestoreUNC = `\\tv.example.test\TimeVault\restore`
)

// strictHarness is a server that knows its own host name, so every monitored folder needs a display folder on it,
// and that has a display folder for the restore folder.
func strictHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, func(c *service.Config, d *config.Deployment) {
		c.ServerHost, c.RestoreDisplayRoot = testHost, testRestoreUNC
		d.ServerHost, d.RestoreDisplayRoot = testHost, testRestoreUNC
	})
}

func errorMessage(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	m, _ := e["message"].(string)
	return m
}

func sourceNamed(t *testing.T, h *harness, name string) map[string]any {
	t.Helper()
	_, out := h.api("GET", "/api/v1/sources", nil)
	for _, s := range out["sources"].([]any) {
		if m := s.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("no monitored folder named %q in %v", name, out)
	return nil
}

func TestTheDisplayFolderIsRequiredAndCheckedWhenTheServerHostIsSet(t *testing.T) {
	h := strictHarness(t)
	bob := h.visitor()
	bob.loginAs(adminID)

	add := func(display string) (*http.Response, string) {
		return bob.postForm("/settings/sources", url.Values{"name": {"Docs"}, "display_path": {display}, "root_path": {t.TempDir()}})
	}
	for _, c := range []struct{ name, display, want string }{
		{"no display folder", "", "Enter the display folder"},
		{"a folder on another server", `\\other.example.test\share\docs`, "other.example.test"},
		{"a drive path", `D:\Docs`, "is not a valid display folder"},
		{"an administrative share", `\\tv.example.test\C$\docs`, "is not a valid display folder"},
	} {
		res, body := add(c.display)
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, c.want) || !strings.Contains(body, `value="Docs"`) {
			t.Errorf("%s = %d, want 400 with %q and the typed name kept\n%s", c.name, res.StatusCode, c.want, body)
		}
	}
	// The message for another server names both hosts and says why.
	if _, body := add(`\\other.example.test\share\docs`); !strings.Contains(body, testHost) || !strings.Contains(body, "cannot be monitored here") {
		t.Errorf("the message about another server:\n%s", body)
	}

	// The host is compared without regard to case and stored the way the settings spell it.
	if res, _ := add(`\\TV.Example.Test\TimeVault\docs`); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("a display folder on this server = %d", res.StatusCode)
	}
	if got := sourceNamed(t, bob, "Docs")["display_path"]; got != `\\`+testHost+`\TimeVault\docs` {
		t.Errorf("stored display folder = %v", got)
	}

	// The same rules hold for the API.
	apiAdd := func(body map[string]any) (int, map[string]any) {
		body["name"], body["root_path"] = "Api", t.TempDir()
		return bob.api("POST", "/api/v1/sources", body)
	}
	if status, out := apiAdd(map[string]any{}); status != http.StatusBadRequest || !strings.Contains(errorMessage(out), "display path is required") {
		t.Errorf("no display folder through the API = %d %v", status, out)
	}
	if status, out := apiAdd(map[string]any{"display_path": `\\other.example.test\share\api`}); status != http.StatusBadRequest || !strings.Contains(errorMessage(out), `\\`+testHost+`\`) {
		t.Errorf("another server through the API = %d %v", status, out)
	}
	if status, out := apiAdd(map[string]any{"display_path": `\\tv.example.test\TimeVault\api`}); status != http.StatusCreated || out["display_path"] != `\\`+testHost+`\TimeVault\api` {
		t.Errorf("a display folder through the API = %d %v", status, out)
	}

	// A folder that was added before the host was set has none; it cannot be edited until it gets one.
	id := itoa(h.env.Src.ID)
	if status, out := bob.api("PATCH", "/api/v1/sources/"+id, map[string]any{"enabled": false}); status != http.StatusBadRequest || !strings.Contains(errorMessage(out), "display path is required") {
		t.Errorf("editing a folder without a display folder = %d %v", status, out)
	}
	if status, out := bob.api("PATCH", "/api/v1/sources/"+id, map[string]any{"display_path": testSourceUNC, "enabled": false}); status != http.StatusOK || out["display_path"] != testSourceUNC || out["enabled"] != false {
		t.Errorf("giving it one = %d %v", status, out)
	}
	if status, out := bob.api("PATCH", "/api/v1/sources/"+id, map[string]any{"display_path": ""}); status != http.StatusBadRequest {
		t.Errorf("removing the display folder = %d %v, want 400", status, out)
	}
	_, out := bob.api("GET", "/api/v1/audit?action=source.update", nil)
	if events, _ := out["events"].([]any); len(events) == 0 || events[0].(map[string]any)["detail"].(map[string]any)["display"] == nil {
		t.Errorf("a new display folder belongs in the audit record of the change: %v", out)
	}

	// The settings form offers the field, and asks for it.
	_, body := bob.get("/settings?add=1")
	if !strings.Contains(body, `name="display_path"`) || !strings.Contains(body, `required`) || !strings.Contains(body, `\\`+testHost+`\share\folder`) {
		t.Errorf("the settings page lacks the display folder field:\n%s", body)
	}
}

func TestTheDisplayFolderIsOptionalWithoutAServerHost(t *testing.T) {
	h := newHarness(t)
	bob := h.visitor()
	bob.loginAs(adminID)
	add := func(name, display string) (int, map[string]any) {
		body := map[string]any{"name": name, "root_path": t.TempDir()}
		if display != "" {
			body["display_path"] = display
		}
		return bob.api("POST", "/api/v1/sources", body)
	}
	if status, out := add("Plain", ""); status != http.StatusCreated || out["display_path"] != nil {
		t.Errorf("a folder without a display folder = %d %v", status, out)
	}
	if status, out := add("Shown", `//any.host/Share/Docs/`); status != http.StatusCreated || out["display_path"] != `\\any.host\Share\Docs` {
		t.Errorf("any host is accepted and the path is normalised = %d %v", status, out)
	}
	if status, out := add("Broken", `not a path`); status != http.StatusBadRequest {
		t.Errorf("a display folder that is not a network path = %d %v", status, out)
	}
	id := itoa(num(sourceNamed(t, bob, "Shown")["id"]))
	if status, out := bob.api("PATCH", "/api/v1/sources/"+id, map[string]any{"display_path": ""}); status != http.StatusOK || out["display_path"] != nil {
		t.Errorf("removing the display folder = %d %v", status, out)
	}
	if status, out := bob.api("PATCH", "/api/v1/sources/"+id, map[string]any{"name": "Renamed"}); status != http.StatusOK || out["name"] != "Renamed" {
		t.Errorf("a rename = %d %v", status, out)
	}
	_, body := bob.get("/settings")
	if !strings.Contains(body, "No display folder yet") {
		t.Errorf("the settings page without a host name:\n%s", body)
	}
}

func TestOnlyAdministratorsSeeTheFoldersOfTheServer(t *testing.T) {
	h := strictHarness(t)
	seed(h)
	alice, bob, anon := h.visitor(), h.visitor(), h.visitor()
	alice.loginAs(userID)
	bob.loginAs(adminID)
	srcID := h.env.Src.ID
	src := itoa(srcID)
	if status, out := bob.api("PATCH", "/api/v1/sources/"+src, map[string]any{"display_path": testSourceUNC}); status != http.StatusOK {
		t.Fatalf("setting the display folder = %d %v", status, out)
	}

	// An ordinary account backs up and restores; the texts of these jobs name the folders of the server.
	status, out := alice.api("POST", "/api/v1/backups", map[string]any{"source_id": srcID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	backupJob := num(out["job"].(map[string]any)["id"])
	alice.waitJob(backupJob)
	_, out = alice.api("GET", "/api/v1/recovery-points?source_id="+src, nil)
	pointID := num(out["recovery_points"].([]any)[0].(map[string]any)["id"])
	req := map[string]any{"source_id": srcID, "point_id": pointID, "paths": []string{"a.txt"}, "dest_name": "by-alice", "policy": "skip"}
	_, preview := alice.api("POST", "/api/v1/restores/preview", req)
	if preview["destination"] != testRestoreUNC+`\by-alice` || preview["destination_real"] != nil {
		t.Errorf("an ordinary account is told where the restore goes = %v / %v", preview["destination"], preview["destination_real"])
	}
	if _, adminPreview := bob.api("POST", "/api/v1/restores/preview", req); adminPreview["destination"] != testRestoreUNC+`\by-alice` ||
		adminPreview["destination_real"] != h.env.RestoreRoot+`\by-alice` {
		t.Errorf("an administrator is told both = %v / %v", adminPreview["destination"], adminPreview["destination_real"])
	}
	req["confirmation_token"], req["idempotency_key"] = preview["confirmation_token"], "alice-1"
	_, started := alice.api("POST", "/api/v1/restores", req)
	restoreJob := num(started["job"].(map[string]any)["id"])
	if job := alice.waitJob(restoreJob); job["state"] != "succeeded" {
		t.Fatalf("restore job = %v", job)
	}

	physical := []string{h.env.SrcDir, h.env.Repo.Root(), h.env.RestoreRoot, "test.env"}
	hidden := func(what, text string, who ...string) {
		t.Helper()
		for _, p := range append(physical, who...) {
			for _, form := range []string{p, strings.ReplaceAll(p, `\`, `\\`), strings.ReplaceAll(p, `\`, `/`)} {
				if strings.Contains(text, form) {
					t.Errorf("%s shows %q to somebody who is not an administrator", what, form)
				}
			}
		}
	}

	// What an ordinary account and an anonymous visitor see: the display folders, never the real ones.
	pages := map[string][]string{
		"/explorer?source=" + src:                            {testSourceUNC},
		"/explorer?source=" + src + "&path=docs":             {testSourceUNC + `\docs`},
		"/history?source=" + src + "&point=" + itoa(pointID): {testSourceUNC},
		"/versions?source=" + src + "&path=docs/b.txt":       {testSourceUNC + `\docs\b.txt`},
		"/backups":                 {testSourceUNC},
		"/jobs/" + itoa(backupJob): {},
	}
	for page, want := range pages {
		for who, v := range map[string]*harness{"an ordinary account": alice, "an anonymous visitor": anon} {
			res, body := v.get(page)
			if res.StatusCode != http.StatusOK {
				t.Errorf("%s gets %s = %d", who, page, res.StatusCode)
				continue
			}
			hidden(who+" on "+page, body)
			for _, w := range want {
				if !strings.Contains(body, w) {
					t.Errorf("%s on %s does not see %q", who, page, w)
				}
			}
		}
	}
	for page, want := range map[string][]string{
		"/backups":                  {testSourceUNC, testRestoreUNC},
		"/jobs/" + itoa(restoreJob): {testRestoreUNC + `\by-alice`},
		"/settings":                 {testHost, testRestoreUNC, testSourceUNC, "% free"},
		"/status":                   {"% free"},
		// The audit page shows the details as JSON, which doubles every backslash.
		"/audit": {strings.ReplaceAll(testRestoreUNC+`\by-alice`, `\`, `\\`)},
	} {
		res, body := alice.get(page)
		if res.StatusCode != http.StatusOK {
			t.Errorf("an ordinary account gets %s = %d", page, res.StatusCode)
			continue
		}
		hidden("an ordinary account on "+page, body)
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("an ordinary account on %s does not see %q", page, w)
			}
		}
	}
	_, body := alice.postForm("/restore/preview", url.Values{"source_id": {src}, "point_id": {itoa(pointID)}, "paths": {"a.txt"}, "dest_name": {"preview-page"}, "policy": {"skip"}})
	hidden("the restore preview of an ordinary account", body)
	if !strings.Contains(body, testRestoreUNC+`\preview-page`) {
		t.Errorf("the restore preview names the destination by its display folder:\n%s", body)
	}
	for _, path := range []string{"/api/v1/sources", "/api/v1/settings", "/api/v1/health", "/api/v1/audit?limit=500", "/api/v1/jobs/" + itoa(restoreJob),
		"/api/v1/files?source_id=" + src, "/api/v1/history/tree?source_id=" + src + "&point_id=" + itoa(pointID)} {
		res, raw := alice.get(path)
		if res.StatusCode != http.StatusOK {
			t.Errorf("an ordinary account gets %s = %d", path, res.StatusCode)
			continue
		}
		hidden("an ordinary account on "+path, raw)
	}
	_, settings := alice.api("GET", "/api/v1/settings", nil)
	if settings["server_host"] != testHost || settings["restore_display_root"] != testRestoreUNC || settings["repository"] != nil || settings["restore_root"] != nil || settings["env_file"] != nil {
		t.Errorf("the settings an ordinary account gets = %v", settings)
	}
	_, sources := alice.api("GET", "/api/v1/sources", nil)
	if first := sources["sources"].([]any)[0].(map[string]any); first["display_path"] != testSourceUNC || first["root_path"] != nil {
		t.Errorf("the monitored folder an ordinary account gets = %v", first)
	}
	_, events := alice.api("GET", "/api/v1/jobs/"+itoa(restoreJob), nil)
	named := false
	for _, e := range events["events"].([]any) {
		named = named || strings.Contains(e.(map[string]any)["message"].(string), testRestoreUNC+`\by-alice`)
	}
	if !named {
		t.Errorf("the log of the restore names the display folder: %v", events)
	}

	// An administrator sees the display folder and the folder on the server's disk.
	for page, want := range map[string][]string{
		"/explorer?source=" + src:   {testSourceUNC, h.env.SrcDir},
		"/backups":                  {testSourceUNC, h.env.SrcDir, testRestoreUNC, h.env.RestoreRoot},
		"/settings":                 {testSourceUNC, h.env.SrcDir, h.env.Repo.Root(), h.env.RestoreRoot, testRestoreUNC},
		"/jobs/" + itoa(restoreJob): {testRestoreUNC + `\by-alice`, h.env.RestoreRoot + `\by-alice`},
	} {
		res, body := bob.get(page)
		if res.StatusCode != http.StatusOK {
			t.Errorf("an administrator gets %s = %d", page, res.StatusCode)
			continue
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("an administrator on %s does not see %q", page, w)
			}
		}
	}
	_, adminSettings := bob.api("GET", "/api/v1/settings", nil)
	if adminSettings["repository"] != h.env.Repo.Root() || adminSettings["restore_root"] != h.env.RestoreRoot || adminSettings["env_file"] != "test.env" {
		t.Errorf("the settings an administrator gets = %v", adminSettings)
	}
	_, adminSources := bob.api("GET", "/api/v1/sources", nil)
	if first := adminSources["sources"].([]any)[0].(map[string]any); first["root_path"] != h.env.SrcDir || first["display_path"] != testSourceUNC {
		t.Errorf("the monitored folder an administrator gets = %v", first)
	}

	// The location line has a button that copies the path.
	if _, body := alice.get("/explorer?source=" + src + "&path=docs"); !strings.Contains(body, `data-copy="`+testSourceUNC+`\docs"`) {
		t.Errorf("the explorer offers to copy the display folder:\n%s", body)
	}
}
