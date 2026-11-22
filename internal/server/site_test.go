package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"timevault/internal/config"
	"timevault/internal/server"
)

// siteStub is the backend of the Site settings page for the tests: the stored values live in memory.
type siteStub struct {
	mu         sync.Mutex
	stored     map[string]string
	base       map[string]string
	saves      int
	restarts   int
	restartErr error
	by         string
}

func (s *siteStub) backend() *server.SiteBackend {
	return &server.SiteBackend{
		Base: func(key string) (string, bool) { v, ok := s.base[key]; return v, ok },
		Stored: func(context.Context) (map[string]string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			out := map[string]string{}
			for k, v := range s.stored {
				out[k] = v
			}
			return out, nil
		},
		Save: func(_ context.Context, set map[string]string, reset []string, by string) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.saves++
			s.by = by
			for k, v := range set {
				s.stored[k] = v
			}
			for _, k := range reset {
				delete(s.stored, k)
			}
			return nil
		},
		Running: config.SiteEffective(s.stored),
		Restart: func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.restartErr != nil {
				return s.restartErr
			}
			s.restarts++
			return nil
		},
	}
}

func (s *siteStub) value(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.stored[key]
	return v, ok
}

func siteHarness(t *testing.T, stub *siteStub) *harness {
	t.Helper()
	if stub.stored == nil {
		stub.stored = map[string]string{}
	}
	return newHarnessOpts(t, nil, func(o *server.Options) { o.Site = stub.backend() })
}

func siteForm(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set("s_"+kv[i], kv[i+1])
	}
	return v
}

func TestSiteSettingsAreForTheLocalAdministratorOnly(t *testing.T) {
	stub := &siteStub{}
	h := siteHarness(t, stub)
	seed(h)
	anon, user, dirAdmin, local := h.visitor(), h.visitor(), h.visitor(), h.visitor()
	user.loginAs(userID)
	dirAdmin.loginAs(adminID)
	local.login()

	// A visitor is sent to sign in; the API says 401.
	if res, _ := anon.get("/settings/site"); res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Errorf("anonymous /settings/site = %d %q, want the sign-in page", res.StatusCode, res.Header.Get("Location"))
	}
	if st, _ := anon.api("GET", "/api/v1/site-settings", nil); st != http.StatusUnauthorized {
		t.Errorf("anonymous API = %d, want 401", st)
	}

	// Accounts of the user directory, administrators too, are told there is no such page or endpoint.
	for name, who := range map[string]*harness{"user": user, "directory administrator": dirAdmin} {
		if res, body := who.get("/settings/site"); res.StatusCode != http.StatusNotFound || strings.Contains(body, "s_job_workers") {
			t.Errorf("%s: GET /settings/site = %d", name, res.StatusCode)
		}
		if res, _ := who.postForm("/settings/site", siteForm("job_workers", "9")); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: POST /settings/site = %d, want 404", name, res.StatusCode)
		}
		if res, _ := who.postForm("/settings/site/restart", nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: POST /settings/site/restart = %d, want 404", name, res.StatusCode)
		}
		for _, call := range []struct{ method, path string }{
			{"GET", "/api/v1/site-settings"}, {"PATCH", "/api/v1/site-settings"}, {"POST", "/api/v1/site-settings/restart"},
		} {
			if st, _ := who.api(call.method, call.path, map[string]any{"settings": map[string]any{"job_workers": "9"}}); st != http.StatusNotFound {
				t.Errorf("%s: %s %s = %d, want 404", name, call.method, call.path, st)
			}
		}
		if _, body := who.get("/settings"); strings.Contains(body, "/settings/site") {
			t.Errorf("%s: the Settings page links to the Site settings", name)
		}
		if st, out := who.api("GET", "/api/v1/settings", nil); st != http.StatusOK || out["listen"] != nil || out["database"] != nil || out["user_directory"] != nil {
			t.Errorf("%s: /api/v1/settings tells how the service is set up: %v", name, out)
		}
	}
	if stub.saves != 0 || len(stub.stored) != 0 {
		t.Fatalf("a refused request stored something: %v", stub.stored)
	}
	// The attempts are audited, for the local administrator to see.
	_, out := local.api("GET", "/api/v1/audit?action=access.denied&limit=100", nil)
	routes := 0
	for _, e := range out["events"].([]any) {
		if d, _ := e.(map[string]any)["detail"].(map[string]any); d["path"] == "/settings/site" {
			routes++
		}
	}
	if routes == 0 {
		t.Errorf("the refused attempts are not in the audit log: %v", out)
	}

	// The local administrator gets the page with a field for every Site setting, and what the environment holds.
	res, body := local.get("/settings/site")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("local administrator: GET /settings/site = %d", res.StatusCode)
	}
	for _, s := range config.SiteSettings() {
		if !strings.Contains(body, `name="s_`+s.Key+`"`) {
			t.Errorf("the page has no field for %s", s.Key)
		}
	}
	for _, want := range []string{"127.0.0.1:8660", "db.test:5432/TimeVault", "directory.test:6379/0", h.env.Repo.Root(), "test.env", `action="/settings/site"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	assertCSPClean(t, "/settings/site", body)
	if _, page := local.get("/settings"); !strings.Contains(page, `href="/settings/site"`) {
		t.Error("the Settings page of the local administrator does not link to the Site settings")
	}
	if st, out := local.api("GET", "/api/v1/settings", nil); st != http.StatusOK || out["listen"] != "127.0.0.1:8660" || out["database"] != "db.test:5432/TimeVault" {
		t.Errorf("the local administrator gets the running configuration: %v", out)
	}
}

func TestSiteSettingsAreNotThereWithoutABackend(t *testing.T) {
	h := newHarness(t)
	h.login()
	if res, _ := h.get("/settings/site"); res.StatusCode != http.StatusNotFound {
		t.Errorf("GET /settings/site without a backend = %d, want 404", res.StatusCode)
	}
}

func TestSavingSiteSettings(t *testing.T) {
	stub := &siteStub{stored: map[string]string{"log_level": "debug"}, base: map[string]string{"TIMEVAULT_REDIS_URL": "redis://:p@127.0.0.1:6379/0"}}
	h := siteHarness(t, stub)
	local, dirAdmin := h.visitor(), h.visitor()
	local.login()
	dirAdmin.loginAs(adminID)

	// What is stored shows in the form, and nothing is waiting for a restart yet.
	_, body := local.get("/settings/site")
	if !strings.Contains(body, `<option value="debug" selected>`) || strings.Contains(body, "not in use yet") {
		t.Errorf("the page of the stored settings:\n%s", body)
	}

	// A change is stored, and the page says that it waits for a restart and offers one.
	res, _ := local.postForm("/settings/site", siteForm("job_workers", "4", "cookie_secure", "true", "log_level", "debug"))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/site" {
		t.Fatalf("save = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if v, _ := stub.value("job_workers"); v != "4" {
		t.Errorf("job_workers = %q", v)
	}
	if v, _ := stub.value("cookie_secure"); v != "true" {
		t.Errorf("cookie_secure = %q", v)
	}
	if stub.by != "admin" {
		t.Errorf("saved by %q, want the local administrator", stub.by)
	}
	_, body = local.get("/settings/site")
	if !strings.Contains(body, "Saved. Restart the service") || !strings.Contains(body, "not in use yet") || !strings.Contains(body, `action="/settings/site/restart"`) {
		t.Errorf("the page after a save:\n%s", body)
	}

	// The audit log names the settings, never their values, and only the local administrator sees the event.
	_, out := local.api("GET", "/api/v1/audit?action=site.update", nil)
	events := out["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("site.update events for the local administrator = %v", out)
	}
	detail := events[0].(map[string]any)["detail"].(map[string]any)
	if changed, _ := detail["changed"].([]any); len(changed) != 2 || changed[0] != "cookie_secure" || changed[1] != "job_workers" {
		t.Errorf("the audit detail = %v", detail)
	}
	if raw := events[0].(map[string]any)["detail"]; strings.Contains(toJSON(raw), `"4"`) || strings.Contains(toJSON(raw), "true") {
		t.Errorf("the audit detail repeats a value: %v", raw)
	}
	if _, out := dirAdmin.api("GET", "/api/v1/audit?action=site.update", nil); len(out["events"].([]any)) != 0 {
		t.Errorf("an administrator of the user directory sees the Site settings in the audit log: %v", out)
	}
	if _, page := dirAdmin.get("/audit"); strings.Contains(page, "Site settings changed") {
		t.Error("the Audit page of an administrator of the user directory lists the change of the Site settings")
	}
	if _, page := local.get("/audit"); !strings.Contains(page, "Site settings changed") {
		t.Error("the Audit page of the local administrator does not list the change")
	}

	// A value that equals the default is not stored: the setting goes back to the default.
	if res, _ := local.postForm("/settings/site", siteForm("job_workers", "2")); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save of the default = %d", res.StatusCode)
	}
	if _, ok := stub.value("job_workers"); ok {
		t.Error("a value equal to the default was stored")
	}
	if v, _ := stub.value("cookie_secure"); v != "true" {
		t.Errorf("a setting that the form did not carry changed: cookie_secure = %q", v)
	}

	// Nothing changed: no save, and a message that says so.
	before := stub.saves
	res, _ = local.postForm("/settings/site", siteForm("cookie_secure", "true"))
	if res.StatusCode != http.StatusSeeOther || stub.saves != before {
		t.Errorf("unchanged save = %d, saves %d -> %d", res.StatusCode, before, stub.saves)
	}
	if _, page := local.get("/settings/site"); !strings.Contains(page, "Nothing was changed.") {
		t.Errorf("no message for an unchanged form:\n%s", page)
	}

	// A form that is not valid is shown again as it was typed, says what is wrong, and stores nothing.
	before = stub.saves
	res, body = local.postForm("/settings/site", siteForm("job_workers", "99", "session_ttl", "1s", "log_level", "info"))
	if res.StatusCode != http.StatusBadRequest || stub.saves != before {
		t.Fatalf("invalid save = %d, saves %d -> %d", res.StatusCode, before, stub.saves)
	}
	for _, want := range []string{"The settings were not saved", "job_workers must be an integer between 1 and 16", "session_ttl must be between 5m and 720h", `value="99"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the rejected form lacks %q", want)
		}
	}
	if strings.Contains(body, "TIMEVAULT_JOB_WORKERS") {
		t.Error("the problems name the environment variable instead of the setting")
	}
	if v, _ := stub.value("log_level"); v != "debug" {
		t.Errorf("a rejected form changed log_level to %q", v)
	}

	// Settings that depend on the environment: the web log needs the Redis, an update address needs migrations.
	res, body = local.postForm("/settings/site", siteForm("update_url", "https://example.test/tv/"))
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "update_url needs TIMEVAULT_AUTO_MIGRATE=true") {
		t.Errorf("an update address without automatic migrations = %d:\n%s", res.StatusCode, body)
	}
	stub.base["TIMEVAULT_AUTO_MIGRATE"] = "true"
	if res, _ = local.postForm("/settings/site", siteForm("update_url", "https://example.test/tv/")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("an update address with automatic migrations = %d", res.StatusCode)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestSiteSettingsThroughTheAPI(t *testing.T) {
	stub := &siteStub{stored: map[string]string{"log_level": "debug"}}
	h := siteHarness(t, stub)
	local := h.visitor()
	local.login()

	st, out := local.api("GET", "/api/v1/site-settings", nil)
	if st != http.StatusOK || out["restart_required"] != false || out["can_restart"] != true {
		t.Fatalf("GET = %d %v", st, out)
	}
	list := out["settings"].([]any)
	if len(list) != len(config.SiteSettings()) {
		t.Fatalf("%d settings listed, want %d", len(list), len(config.SiteSettings()))
	}
	byKey := map[string]map[string]any{}
	for _, s := range list {
		m := s.(map[string]any)
		byKey[m["key"].(string)] = m
	}
	if m := byKey["log_level"]; m["value"] != "debug" || m["default"] != "info" || m["stored"] != true || m["kind"] != "choice" {
		t.Errorf("log_level = %v", m)
	}
	if m := byKey["job_workers"]; m["value"] != "2" || m["stored"] != false {
		t.Errorf("job_workers = %v", m)
	}

	// Set one, reset another (null), leave the rest.
	st, out = local.api("PATCH", "/api/v1/site-settings", map[string]any{"settings": map[string]any{"job_workers": "5", "log_level": nil}})
	if st != http.StatusOK || out["restart_required"] != true {
		t.Fatalf("PATCH = %d %v", st, out)
	}
	if changed, _ := out["changed"].([]any); len(changed) != 1 || changed[0] != "job_workers" {
		t.Errorf("changed = %v", out["changed"])
	}
	if reset, _ := out["reset"].([]any); len(reset) != 1 || reset[0] != "log_level" {
		t.Errorf("reset = %v", out["reset"])
	}
	if v, _ := stub.value("job_workers"); v != "5" {
		t.Errorf("job_workers = %q", v)
	}
	if _, ok := stub.value("log_level"); ok {
		t.Error("log_level was not reset")
	}
	if _, out := local.api("GET", "/api/v1/site-settings", nil); out["restart_required"] != true {
		t.Errorf("the API does not say that a restart is needed: %v", out)
	}

	for name, c := range map[string]struct {
		body map[string]any
		code string
	}{
		"unknown":    {map[string]any{"settings": map[string]any{"colour": "blue"}}, "unknown_setting"},
		"invalid":    {map[string]any{"settings": map[string]any{"job_workers": "99"}}, "invalid_setting"},
		"not a bool": {map[string]any{"settings": map[string]any{"cookie_secure": "perhaps"}}, "invalid_setting"},
		"empty":      {map[string]any{"settings": map[string]any{}}, "invalid_request"},
		"no body":    {map[string]any{"other": 1}, "invalid_json"},
	} {
		saves := stub.saves
		st, out := local.api("PATCH", "/api/v1/site-settings", c.body)
		errBody, _ := out["error"].(map[string]any)
		if st != http.StatusBadRequest || errBody["code"] != c.code || stub.saves != saves {
			t.Errorf("%s: PATCH = %d %v (saves %d -> %d)", name, st, out, saves, stub.saves)
		}
	}
}

func TestRestartForTheSiteSettings(t *testing.T) {
	stub := &siteStub{}
	h := siteHarness(t, stub)
	local := h.visitor()
	local.login()

	// A restart that is possible shows a page that says so, and is audited.
	res, body := local.postForm("/settings/site/restart", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Restarting") || !strings.Contains(body, `http-equiv="refresh"`) || stub.restarts != 1 {
		t.Fatalf("restart = %d, restarts %d:\n%s", res.StatusCode, stub.restarts, body)
	}
	_, out := local.api("GET", "/api/v1/audit?action=site.restart", nil)
	if len(out["events"].([]any)) != 1 {
		t.Errorf("the restart is not audited: %v", out)
	}

	// While jobs run it is refused, on the page and in the API.
	stub.restartErr = server.ErrJobsRunning
	res, _ = local.postForm("/settings/site/restart", nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/site" || stub.restarts != 1 {
		t.Errorf("restart while jobs run = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if _, page := local.get("/settings/site"); !strings.Contains(page, "Jobs are running") {
		t.Errorf("no message that jobs are running:\n%s", page)
	}
	if st, out := local.api("POST", "/api/v1/site-settings/restart", nil); st != http.StatusConflict || out["error"].(map[string]any)["code"] != "jobs_running" {
		t.Errorf("API restart while jobs run = %d %v", st, out)
	}
	stub.restartErr = nil
	if st, out := local.api("POST", "/api/v1/site-settings/restart", nil); st != http.StatusAccepted || out["restarting"] != true || stub.restarts != 2 {
		t.Errorf("API restart = %d %v (restarts %d)", st, out, stub.restarts)
	}
}

func TestTheSiteSettingsPageIsTranslated(t *testing.T) {
	stub := &siteStub{}
	h := siteHarness(t, stub)
	local := h.visitor()
	local.login()
	_, zh := local.get("/settings/site", map[string]string{"Cookie": "tv_lang=zh"})
	for _, want := range []string{"站点设置", "地址与显示", "服务器名称", "在环境变量或 .env 文件中设置"} {
		if !strings.Contains(zh, want) {
			t.Errorf("the Chinese page lacks %q", want)
		}
	}
	if strings.Contains(zh, "site.f.") || strings.Contains(zh, "site.h.") || strings.Contains(zh, "site.g.") {
		t.Error("the Chinese page shows a message key instead of a text")
	}
}
