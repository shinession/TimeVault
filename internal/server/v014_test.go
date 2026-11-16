package server_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"timevault/internal/search"
)

// waitUntil polls a condition of the back end, whose jobs run in the background.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// threeVersions backs up the source three times, changing a.txt each time, so that the file has three versions.
func threeVersions(h *harness) {
	seed(h)
	h.env.Backup()
	h.env.Write("a.txt", "alpha two")
	h.env.Backup()
	h.env.Write("a.txt", "alpha three")
	h.env.Backup()
}

func (h *harness) versionsOfFile(name string) int {
	h.t.Helper()
	var n int
	if err := h.env.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM node_versions v JOIN nodes n ON n.id = v.node_id WHERE n.source_id = $1 AND n.name = $2`, h.env.Src.ID, name).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// cleanupsEnded waits until no deletion of old versions is queued or running. The limit of a folder cannot change
// while one runs, and the cleanup that a change queues starts at once.
func (h *harness) cleanupsEnded() {
	h.t.Helper()
	waitUntil(h.t, "the cleanups of old versions to end", func() bool {
		var n int
		if err := h.env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE kind = 'prune' AND state IN ('queued', 'running')`).Scan(&n); err != nil {
			h.t.Fatal(err)
		}
		return n == 0
	})
}

func TestSettingsShowAndConfirmTheVersionLimit(t *testing.T) {
	h := newHarness(t)
	threeVersions(h)
	bob := h.visitor()
	bob.loginAs(adminID)
	id := itoa(h.env.Src.ID)

	_, body := bob.get("/settings")
	if !strings.Contains(body, `name="max_versions"`) || !strings.Contains(body, "All versions of every file") || !strings.Contains(body, `value="0"`) {
		t.Fatalf("the settings page offers no version limit:\n%s", body)
	}
	assertCSPClean(t, "/settings", body)

	form := url.Values{"name": {"Team Files"}, "root_path": {h.env.SrcDir}, "excludes": {"*.tmp\n"}, "enabled": {"1"}, "max_versions": {"2"}}
	// A limit that would delete history is not applied before it has been confirmed.
	res, body := bob.postForm("/settings/sources/"+id, form)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "permanently deletes 1 older version(s) of 1 file(s)") ||
		!strings.Contains(body, `name="confirm_prune"`) || !strings.Contains(body, `name="max_versions" value="2"`) {
		t.Fatalf("lowering the limit without confirmation = %d\n%s", res.StatusCode, body)
	}
	if _, out := bob.api("GET", "/api/v1/sources", nil); out["sources"].([]any)[0].(map[string]any)["max_versions"] != nil || h.versionsOfFile("a.txt") != 3 {
		t.Fatalf("a refused change was applied: %v, %d versions", out, h.versionsOfFile("a.txt"))
	}

	form.Set("confirm_prune", "1")
	if res, _ := bob.postForm("/settings/sources/"+id, form); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings" {
		t.Fatalf("confirmed change = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if _, out := bob.api("GET", "/api/v1/sources", nil); out["sources"].([]any)[0].(map[string]any)["max_versions"] != float64(2) {
		t.Fatalf("the limit was not stored: %v", out)
	}
	waitUntil(t, "the old versions to be deleted", func() bool { return h.versionsOfFile("a.txt") == 2 })

	_, body = bob.get("/settings")
	if !strings.Contains(body, "The newest 2 of each file") || !strings.Contains(body, `name="max_versions" value="2"`) {
		t.Errorf("the settings page does not show the limit:\n%s", body)
	}
	// The points that lost a version say so, for everybody.
	waitUntil(t, "the partial point", func() bool {
		_, page := h.visitor().get("/backups")
		return strings.Contains(page, ">Partial<")
	})

	// What is not a number from 0 to 10000 is refused with a reason.
	for _, bad := range []string{"abc", "-1", "10001", "2.5"} {
		form.Set("max_versions", bad)
		res, body := bob.postForm("/settings/sources/"+id, form)
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "whole number from 0 to 10000") {
			t.Errorf("max_versions=%q = %d\n%s", bad, res.StatusCode, body)
		}
	}
	// An empty field and 0 both keep every version.
	h.cleanupsEnded()
	for _, none := range []string{"", "0"} {
		form.Set("max_versions", none)
		if res, _ := bob.postForm("/settings/sources/"+id, form); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("max_versions=%q = %d", none, res.StatusCode)
		}
		if _, out := bob.api("GET", "/api/v1/sources", nil); out["sources"].([]any)[0].(map[string]any)["max_versions"] != nil {
			t.Errorf("max_versions=%q must keep every version: %v", none, out)
		}
	}

	// The page is translated.
	bob.get("/lang?set=zh")
	if _, body := bob.get("/settings"); !strings.Contains(body, "每个文件保留的版本数") || !strings.Contains(body, "上次耗时") {
		t.Errorf("the Chinese settings page lacks the version limit:\n%s", body)
	}
}

func TestAPIAppliesTheVersionLimitWithConfirmation(t *testing.T) {
	h := newHarness(t)
	threeVersions(h)
	bob := h.visitor()
	bob.loginAs(adminID)
	path := "/api/v1/sources/" + itoa(h.env.Src.ID)

	status, out := bob.api("PATCH", path, map[string]any{"max_versions": 2})
	errObj, _ := out["error"].(map[string]any)
	if status != http.StatusConflict || errObj["code"] != "prune_not_confirmed" || !strings.Contains(errObj["message"].(string), "1 older version(s) of 1 file(s)") {
		t.Fatalf("lowering the limit without confirmation = %d %v", status, out)
	}
	if status, out := bob.api("PATCH", path, map[string]any{"max_versions": 2, "confirm_prune": true}); status != http.StatusOK || out["max_versions"] != float64(2) {
		t.Fatalf("with confirmation = %d %v", status, out)
	}
	waitUntil(t, "the old versions to be deleted", func() bool { return h.versionsOfFile("a.txt") == 2 })
	h.cleanupsEnded()

	// The recovery points that lost versions say so; the newest never does.
	_, pts := bob.api("GET", "/api/v1/recovery-points?source_id="+itoa(h.env.Src.ID), nil)
	points := pts["recovery_points"].([]any)
	newest, oldest := points[0].(map[string]any), points[len(points)-1].(map[string]any)
	if newest["partial"] != false || oldest["partial"] != true {
		t.Errorf("partial flags: newest %v, oldest %v", newest["partial"], oldest["partial"])
	}
	if status, out := bob.api("PATCH", path, map[string]any{"name": "Team"}); status != http.StatusOK || out["max_versions"] != float64(2) || out["name"] != "Team" {
		t.Errorf("a rename must keep the limit: %d %v", status, out)
	}
	// Raising it needs no confirmation, and null or 0 keeps everything.
	if status, out := bob.api("PATCH", path, map[string]any{"max_versions": 5}); status != http.StatusOK || out["max_versions"] != float64(5) {
		t.Errorf("raising the limit = %d %v", status, out)
	}
	if status, out := bob.api("PATCH", path, map[string]any{"max_versions": nil}); status != http.StatusOK || out["max_versions"] != nil {
		t.Errorf("null = %d %v", status, out)
	}
	if status, out := bob.api("PATCH", path, map[string]any{"max_versions": 3}); status != http.StatusOK || out["max_versions"] != float64(3) {
		t.Errorf("a limit above what the file has needs no confirmation: %d %v", status, out)
	}
	h.cleanupsEnded()
	if status, out := bob.api("PATCH", path, map[string]any{"max_versions": 0}); status != http.StatusOK || out["max_versions"] != nil {
		t.Errorf("0 = %d %v", status, out)
	}
	for _, bad := range []any{-1, 10001, "many"} {
		if status, out := bob.api("PATCH", path, map[string]any{"max_versions": bad}); status != http.StatusBadRequest {
			t.Errorf("max_versions=%v = %d %v, want 400", bad, status, out)
		}
	}

	// A new folder can have a limit from the start; it has no history to lose.
	status, out = bob.api("POST", "/api/v1/sources", map[string]any{"name": "Docs", "root_path": t.TempDir(), "max_versions": 4})
	if status != http.StatusCreated || out["max_versions"] != float64(4) {
		t.Errorf("creating a folder with a limit = %d %v", status, out)
	}
}

func TestAuditShowsHowLongOperationsRan(t *testing.T) {
	h := newHarness(t)
	seed(h)
	h.login()
	status, out := h.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	h.waitJob(num(out["job"].(map[string]any)["id"]))

	_, out = h.api("GET", "/api/v1/audit?action=backup.finish", nil)
	events, _ := out["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("backup.finish events = %v", out)
	}
	finish := events[0].(map[string]any)
	if ms, _ := finish["duration_ms"].(float64); ms < 1 || ms > 60_000 {
		t.Errorf("duration_ms = %v, want how long the run took", finish["duration_ms"])
	}
	if d := finish["detail"].(map[string]any); d["bytes"] == nil || d["new_bytes"] == nil || d["new_objects"] == nil {
		t.Errorf("the backup record lacks the data to judge its speed: %v", d)
	}
	for _, action := range []string{"auth.login", "backup.request"} {
		_, out = h.api("GET", "/api/v1/audit?action="+action, nil)
		for _, e := range out["events"].([]any) {
			if e.(map[string]any)["duration_ms"] != nil {
				t.Errorf("%s has a duration: %v", action, e)
			}
		}
	}

	_, body := h.get("/audit")
	if !strings.Contains(body, "<th>Duration</th>") {
		t.Errorf("the audit page has no duration column:\n%s", body)
	}
	if !regexp.MustCompile(`<td class="num">[0-9.]+(ms|s)<br><span class="tv-muted tv-small">[0-9.]+ ?[KMG]?B/s</span></td>`).MatchString(body) {
		t.Errorf("the backup is shown without its duration and speed:\n%s", body)
	}
	if !strings.Contains(body, `<td class="num">-</td>`) {
		t.Error("events without a duration must show a dash")
	}
	h.get("/lang?set=zh")
	if _, body := h.get("/audit"); !strings.Contains(body, "<th>耗时</th>") {
		t.Errorf("the Chinese audit page has no duration column:\n%s", body)
	}
}

func TestExplorerShowsFolderSizesFromTheSearchEngine(t *testing.T) {
	h := newHarness(t)
	seed(h)
	h.env.Write("zeta/z.txt", "z")
	h.env.Backup() // everything is protected, so every row of the list is a link
	mock := search.NewMock([]search.Result{
		{Name: "docs", Path: h.env.SrcDir + `\docs`, IsDir: true, Size: 4096},
		{Name: "unsized", Path: h.env.SrcDir + `\unsized`, IsDir: true, Size: -1},
	})
	mock.SetSubfolders(h.env.SrcDir, []search.FolderInfo{{Name: "docs", Size: 2048}, {Name: "zeta", Size: 3 << 20}})
	h.search.set(mock)
	anon := h.visitor()
	src := itoa(h.env.Src.ID)

	_, body := anon.get("/explorer?source=" + src)
	row := func(name string) string {
		for _, chunk := range strings.Split(body, `<a class="tv-row"`) {
			if strings.Contains(chunk, `<span class="tv-name">`+name+`</span>`) {
				return chunk
			}
		}
		t.Fatalf("no row for %s in\n%s", name, body)
		return ""
	}
	if r := row("docs"); !strings.Contains(r, `<span class="c-size">2.0 KB</span>`) {
		t.Errorf("docs = %s", r)
	}
	if r := row("zeta"); !strings.Contains(r, `<span class="c-size">3.0 MB</span>`) {
		t.Errorf("zeta = %s", r)
	}
	if r := row("a.txt"); !strings.Contains(r, `<span class="c-size">5 B</span>`) {
		t.Errorf("a.txt = %s", r)
	}

	// Sorting by size orders the folders by what is in them.
	_, out := anon.api("GET", "/api/v1/files?source_id="+src+"&sort=size&order=desc", nil)
	var names []string
	sizes := map[string]float64{}
	for _, e := range out["entries"].([]any) {
		m := e.(map[string]any)
		names = append(names, m["name"].(string))
		sizes[m["name"].(string)] = m["size"].(float64)
	}
	if strings.Join(names, ",") != "zeta,docs,a.txt" || sizes["zeta"] != 3<<20 || sizes["docs"] != 2048 {
		t.Errorf("entries by size = %v %v", names, sizes)
	}

	// Search hits that are folders carry the size the engine has.
	_, body = anon.get("/explorer?source=" + src + "&q=docs")
	if !strings.Contains(body, `<span class="c-size">4.0 KB</span>`) {
		t.Errorf("a folder in the search results has no size:\n%s", body)
	}
	_, body = anon.get("/explorer?source=" + src + "&q=unsized")
	if !strings.Contains(body, `<span class="c-size">-</span>`) {
		t.Errorf("a folder the engine has no size of shows a dash:\n%s", body)
	}

	// With the engine down the page still works, without the sizes.
	mock.SetUnavailable(true)
	other := h.visitor()
	if res, body := other.get("/explorer?source=" + src); res.StatusCode != http.StatusOK || !strings.Contains(body, `<span class="tv-name">docs</span>`) {
		t.Fatalf("the explorer while the engine is down = %d", res.StatusCode)
	}
}
