package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// folder returns what the administrator's API says about a folder.
func (w *folderWorld) folder(t *testing.T, id int64) map[string]any {
	t.Helper()
	_, out := w.admin.api("GET", "/api/v1/sources", nil)
	for _, s := range out["sources"].([]any) {
		if m := s.(map[string]any); num(m["id"]) == id {
			return m
		}
	}
	t.Fatalf("there is no folder %d: %v", id, out)
	return nil
}

func TestFullControlEditsAFolderThroughTheSettingsPageButNotItsRealPath(t *testing.T) {
	w := newFolderWorld(t)
	staff, team := itoa(w.staff), itoa(w.team)

	// The list offers editing where there is full control, and holds no list of people: that comes with the form.
	_, page := w.carol.get("/settings")
	if !strings.Contains(page, `href="/settings?edit=`+staff+`"`) || strings.Contains(page, `href="/settings?edit=`+team+`"`) {
		t.Errorf("Carol is offered to edit exactly the folder she has full control of:\n%s", page)
	}
	if strings.Contains(page, `action="/settings/sources`) || strings.Contains(page, `settings?add=1`) || strings.Contains(page, `name="access_`) {
		t.Errorf("the settings page of Carol holds a form it should not:\n%s", page)
	}
	_, form := w.carol.get("/settings?edit=" + staff)
	for _, want := range []string{`action="/settings/sources/` + staff + `"`, `name="access_browse"`, `name="access_full"`, `value="everyone"`, "Alice Wong", userID, "Bob Chen"} {
		if !strings.Contains(form, want) {
			t.Errorf("the edit form of Staff lacks %q:\n%s", want, form)
		}
	}
	if strings.Contains(form, `name="root_path"`) || strings.Contains(form, w.root[w.staff]) {
		t.Errorf("somebody who is not an administrator is shown the folder on the server's disk:\n%s", form)
	}
	if strings.Contains(form, `value="guest" data-access-group checked`) || !strings.Contains(form, `value="everyone" data-access-group checked`) {
		t.Errorf("the editor does not show the current access: everybody who is signed in browses Staff")
	}
	// A folder she may not change has no form, even when she asks for one.
	if _, other := w.carol.get("/settings?edit=" + team); strings.Contains(other, `action="/settings/sources/`) {
		t.Errorf("Carol is given the edit form of Team Files")
	}

	// She changes the settings and who may do what.
	save := url.Values{
		"name": {"Staff Room"}, "excludes": {"*.tmp"}, "enabled": {"1"}, "schedule": {"1"}, "schedule_minutes": {"30"}, "max_versions": {"0"},
		"access_set": {"1"}, "access_browse": {"everyone"}, "access_backup": {userID}, "access_full": {otherID},
	}
	if res, body := w.carol.postForm("/settings/sources/"+staff, save); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("Carol saves Staff = %d\n%s", res.StatusCode, body)
	}
	got := w.folder(t, w.staff)
	spec := got["access"].(map[string]any)
	if got["name"] != "Staff Room" || got["schedule_minutes"] != float64(30) || got["root_path"] != w.root[w.staff] ||
		fmt.Sprint(spec["browse"], spec["backup"], spec["full"]) != "[everyone] [E1001] [E5005]" {
		t.Errorf("Staff after Carol's change = %v", got)
	}
	_, audit := w.admin.api("GET", "/api/v1/audit?action=source.update", nil)
	changed := audit["events"].([]any)[0].(map[string]any)["detail"].(map[string]any)
	if changed["access"] == nil || changed["schedule_minutes"] == nil || changed["root"] != nil {
		t.Errorf("the audit record lists what changed, access included: %v", changed)
	}

	// The folder on the server's disk is for administrators: not through the form, and not through the API.
	save.Set("root_path", t.TempDir())
	if res, _ := w.carol.postForm("/settings/sources/"+staff, save); res.StatusCode != http.StatusForbidden {
		t.Errorf("Carol points Staff at another folder = %d, want 403", res.StatusCode)
	}
	status, out := w.carol.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"name": "Staff API"})
	if _, has := out["root_path"]; status != http.StatusOK || has || out["name"] != "Staff API" || out["level"] != "full" || out["access"] == nil {
		t.Errorf("Carol renames Staff through the API = %d %v", status, out)
	}
	if status, _ := w.carol.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"root_path": w.root[w.staff]}); status != http.StatusForbidden {
		t.Errorf("Carol sends the folder on the disk = %d, want 403", status)
	}
	if now := w.folder(t, w.staff); now["root_path"] != w.root[w.staff] || now["name"] != "Staff API" {
		t.Errorf("Staff after the refused changes = %v", now)
	}

	// Backing up is not enough, nor is not knowing the folder.
	for who, c := range map[string]struct {
		v      *harness
		folder int64
		want   int
	}{
		"Carol on Team Files": {w.carol, w.team, http.StatusForbidden},
		"Carol on Named":      {w.carol, w.named, http.StatusNotFound},
		"Alice on Staff":      {w.alice, w.staff, http.StatusForbidden},
		"Alice on Secret":     {w.alice, w.secret, http.StatusNotFound},
		"a guest on Team":     {w.guest, w.team, http.StatusUnauthorized},
	} {
		if status, _ := c.v.api("PATCH", "/api/v1/sources/"+itoa(c.folder), map[string]any{"name": "Evil"}); status != c.want {
			t.Errorf("%s: PATCH = %d, want %d", who, status, c.want)
		}
		res, _ := c.v.postForm("/settings/sources/"+itoa(c.folder), url.Values{"name": {"Evil"}})
		want := c.want
		if want == http.StatusUnauthorized {
			want = http.StatusSeeOther // the page sends a visitor to the sign-in
		}
		if res.StatusCode != want {
			t.Errorf("%s: form = %d, want %d", who, res.StatusCode, want)
		}
	}
	if w.folder(t, w.team)["name"] == "Evil" || w.folder(t, w.named)["name"] == "Evil" {
		t.Error("a refused change was applied")
	}

	// She may hand the folder over, and then she has no more than anybody else. Giving up her own full control asks
	// her to confirm it first.
	handover := map[string]any{"access": map[string]any{"browse": []string{"everyone"}, "full": []string{userID}}}
	if status, out := w.carol.api("PATCH", "/api/v1/sources/"+staff, handover); status != http.StatusConflict || out["error"].(map[string]any)["code"] != "lockout_not_confirmed" {
		t.Fatalf("hand-over without confirmation = %d %v, want 409 lockout_not_confirmed", status, out)
	}
	handover["confirm_lockout"] = true
	if status, out := w.carol.api("PATCH", "/api/v1/sources/"+staff, handover); status != http.StatusOK {
		t.Fatalf("hand-over = %d %v", status, out)
	}
	if got := strings.Join(sourcesOf(t, w.carol), " "); !strings.Contains(got, "Staff Room=browse") && !strings.Contains(got, "Staff API=browse") {
		t.Errorf("Carol after the hand-over sees %q", got)
	}
	if status, _ := w.carol.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"name": "Mine"}); status != http.StatusForbidden {
		t.Errorf("Carol edits Staff after giving up full control = %d, want 403", status)
	}
	if status, _ := w.alice.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"name": "Alice's"}); status != http.StatusOK {
		t.Errorf("Alice edits Staff after being given full control = %d, want 200", status)
	}
}

func TestTheAccessListIsCheckedBeforeItIsStored(t *testing.T) {
	w := newFolderWorld(t)
	staff := itoa(w.staff)
	before := fmt.Sprint(w.folder(t, w.staff)["access"])

	bad := []struct {
		what   string
		access map[string]any
		want   string
	}{
		{"guests may only browse", map[string]any{"backup": []string{"guest"}}, "can only be given browse"},
		{"guest with a person", map[string]any{"browse": []string{"guest", userID}}, "cannot be combined"},
		{"everyone with a person", map[string]any{"full": []string{"everyone", userID}}, "cannot be combined"},
		{"guest with everyone", map[string]any{"browse": []string{"guest", "everyone"}}, "cannot be combined"},
		{"not an employee ID", map[string]any{"browse": []string{"not an id!"}}, "not a valid employee ID"},
		{"an account that does not exist", map[string]any{"browse": []string{"E9999"}}, "not an active account"},
	}
	for _, c := range bad {
		status, out := w.carol.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": c.access})
		if status != http.StatusBadRequest || !strings.Contains(errorMessage(out), c.want) {
			t.Errorf("%s = %d %v, want 400 saying %q", c.what, status, out, c.want)
		}
	}
	// A deactivated account is no account either. (An account that already has access stays, even when it is deactivated.)
	w.dir.put(fakeAccount{ID: "E3003", Name: "Gone Guy", Password: "gone-secret", Role: "user", Inactive: true})
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"browse": []string{"E3003"}}}); status != http.StatusBadRequest || !strings.Contains(errorMessage(out), "not an active account") {
		t.Errorf("giving access to a deactivated account = %d %v", status, out)
	}
	w.dir.change(otherID, func(a *fakeAccount) { a.Inactive = true })
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"name": "Staff", "access": map[string]any{"browse": []string{"everyone"}, "full": []string{otherID}}}); status != http.StatusOK {
		t.Errorf("saving a list that names an account that was deactivated since = %d %v", status, out)
	}
	w.dir.change(otherID, func(a *fakeAccount) { a.Inactive = false })
	if after := fmt.Sprint(w.folder(t, w.staff)["access"]); after != before {
		t.Errorf("refused lists changed the access: %s -> %s", before, after)
	}

	// Employee IDs are written in any case and stored the way the directory spells them.
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"browse": []string{" e1001 ", "E1001"}, "full": []string{"e5005"}}}); status != http.StatusOK {
		t.Fatalf("a list with lower case and duplicates = %d %v", status, out)
	}
	if spec := w.folder(t, w.staff)["access"].(map[string]any); fmt.Sprint(spec["browse"], spec["full"]) != "[E1001] [E5005]" {
		t.Errorf("stored list = %v", spec)
	}

	// While the directory cannot be asked, the people who already have access stay and nobody can be added.
	w.dir.setDown(true)
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"name": "Staff", "access": map[string]any{"browse": []string{userID}, "full": []string{otherID}}}); status != http.StatusOK {
		t.Errorf("editing with the people that already have access, directory down = %d %v", status, out)
	}
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"browse": []string{userID}, "full": []string{otherID, adminID}}}); status != http.StatusBadRequest || !strings.Contains(errorMessage(out), "cannot be asked") {
		t.Errorf("adding a person while the directory is down = %d %v", status, out)
	}
	// The editor still opens, with what is stored.
	_, form := w.carol.get("/settings?edit=" + staff)
	if !strings.Contains(form, "cannot list its accounts") || !strings.Contains(form, `value="user:`+otherID+`" data-access-person checked`) {
		t.Errorf("the editor without the directory:\n%s", form)
	}
	w.dir.setDown(false)

	// The form says what is wrong in the language of the page, and shows the entry again.
	res, body := w.carol.postForm("/settings/sources/"+staff, url.Values{
		"name": {"Staff"}, "access_set": {"1"}, "access_browse": {"guest", userID}, "access_full": {otherID},
	})
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "cannot be combined") ||
		!strings.Contains(body, `value="guest" data-access-group checked`) || !strings.Contains(body, `value="user:`+userID+`" data-access-person checked`) {
		t.Errorf("a rejected form = %d\n%s", res.StatusCode, body)
	}
	w.carol.get("/lang?set=zh")
	res, body = w.carol.postForm("/settings/sources/"+staff, url.Values{"name": {"Staff"}, "access_set": {"1"}, "access_backup": {"guest"}})
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "访客") {
		t.Errorf("a rejected form in Chinese = %d\n%s", res.StatusCode, body)
	}
}

func TestANewFolderIsSeenByAdministratorsUntilItIsGivenAccess(t *testing.T) {
	w := newFolderWorld(t)

	// The page has no editor until it is asked for, because the editor lists every account.
	_, plain := w.admin.get("/settings")
	if strings.Contains(plain, `name="access_browse"`) || !strings.Contains(plain, `href="/settings?add=1"`) {
		t.Errorf("the settings page of an administrator:\n%s", plain)
	}
	_, add := w.admin.get("/settings?add=1")
	for _, want := range []string{`action="/settings/sources"`, `name="access_browse"`, `name="access_backup"`, `name="access_full"`, `name="root_path"`, "Guests (not signed in)", "administrators only"} {
		if !strings.Contains(add, want) && !strings.Contains(strings.ToLower(add), strings.ToLower(want)) {
			t.Errorf("the add form lacks %q", want)
		}
	}
	if strings.Contains(add, ` data-access-person checked`) || strings.Contains(add, ` data-access-group checked`) {
		t.Errorf("a new folder starts with nobody ticked")
	}

	// Added without touching the editor, the folder is not given to anybody.
	if res, _ := w.admin.postForm("/settings/sources", url.Values{"name": {"Fresh"}, "root_path": {t.TempDir()}, "access_set": {"1"}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("add = %d", res.StatusCode)
	}
	// Also when the form has no editor at all, as an old page in a browser has none.
	if res, _ := w.admin.postForm("/settings/sources", url.Values{"name": {"Older"}, "root_path": {t.TempDir()}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("add without editor = %d", res.StatusCode)
	}
	for _, v := range []*harness{w.guest, w.alice, w.carol} {
		for _, s := range sourcesOf(t, v) {
			if strings.HasPrefix(s, "Fresh=") || strings.HasPrefix(s, "Older=") {
				t.Errorf("a new folder is seen without having been given to anybody: %v", sourcesOf(t, v))
			}
		}
	}
	if got := strings.Join(sourcesOf(t, w.admin), " "); !strings.Contains(got, "Fresh=full") || !strings.Contains(got, "Older=full") {
		t.Errorf("the administrator sees the new folders: %s", got)
	}
	// Its jobs and its audit events are for administrators too: the first thing that happens to a folder is its creation.
	_, out := w.alice.api("GET", "/api/v1/audit?action=source.create&limit=500", nil)
	for _, e := range out["events"].([]any) {
		if d, _ := e.(map[string]any)["detail"].(map[string]any); d["name"] == "Fresh" || d["name"] == "Older" {
			t.Errorf("Alice sees the creation of a folder she may not see: %v", e)
		}
	}

	// Given through the editor, a folder is seen at once by those it was given to.
	if res, _ := w.admin.postForm("/settings/sources", url.Values{
		"name": {"Open"}, "root_path": {t.TempDir()}, "access_set": {"1"}, "access_browse": {"guest"}, "access_backup": {userID},
	}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("add with access = %d", res.StatusCode)
	}
	if got := strings.Join(sourcesOf(t, w.guest), " "); !strings.Contains(got, "Open=browse") {
		t.Errorf("a guest sees %q", got)
	}
	if got := strings.Join(sourcesOf(t, w.alice), " "); !strings.Contains(got, "Open=backup") {
		t.Errorf("Alice sees %q", got)
	}
	if got := strings.Join(sourcesOf(t, w.carol), " "); !strings.Contains(got, "Open=browse") {
		t.Errorf("Carol, who is not named, still browses what guests browse: %q", got)
	}

	// Only administrators add folders, whatever they may do with the ones they know.
	if res, _ := w.carol.postForm("/settings/sources", url.Values{"name": {"Mine"}, "root_path": {t.TempDir()}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("Carol adds a folder = %d, want 403", res.StatusCode)
	}
	if status, _ := w.carol.api("POST", "/api/v1/sources", map[string]any{"name": "Mine", "root_path": t.TempDir()}); status != http.StatusForbidden {
		t.Errorf("Carol adds a folder through the API = %d, want 403", status)
	}
	// An administrator can give a folder to people through the API as well; a list that is not valid is refused.
	if status, out := w.admin.api("POST", "/api/v1/sources", map[string]any{"name": "Api", "root_path": t.TempDir(), "access": map[string]any{"backup": []string{"guest"}}}); status != http.StatusBadRequest {
		t.Errorf("a new folder with an invalid list = %d %v", status, out)
	}
	if status, out := w.admin.api("POST", "/api/v1/sources", map[string]any{"name": "Api", "root_path": t.TempDir(), "access": map[string]any{"browse": []string{"everyone"}}}); status != http.StatusCreated || out["level"] != "full" {
		t.Errorf("a new folder with access = %d %v", status, out)
	}
	if got := strings.Join(sourcesOf(t, w.alice), " "); !strings.Contains(got, "Api=browse") {
		t.Errorf("Alice sees %q", got)
	}
}

func TestTheSettingsPageSaysWhatTheVisitorMayDoAndWhoElseMay(t *testing.T) {
	w := newFolderWorld(t)

	// Alice sees her level on each folder she can see, and nothing about who else has access or where it is.
	_, alice := w.alice.get("/settings")
	for _, name := range []string{"Team Files", "Staff", "Named"} {
		if !strings.Contains(alice, "</svg> "+name+" ") {
			t.Errorf("Alice's settings page lacks %s", name)
		}
	}
	if strings.Contains(alice, "Secret") || strings.Contains(alice, "Who may do what") || strings.Contains(alice, `href="/settings?edit=`) {
		t.Errorf("Alice's settings page shows what she may not use:\n%s", alice)
	}
	if !strings.Contains(alice, ">Browse<") || !strings.Contains(alice, ">Back up<") {
		t.Errorf("the levels of Alice are not shown")
	}
	for _, v := range []*harness{w.alice, w.carol} {
		for id, root := range w.root {
			if _, page := v.get("/settings"); strings.Contains(page, root) {
				t.Errorf("the folder %d on the server's disk is shown to somebody who is not an administrator", id)
			}
		}
	}

	// Carol sees who has access to the folder she controls, by name.
	_, carol := w.carol.get("/settings")
	if !strings.Contains(carol, "Who may do what") || !strings.Contains(carol, "Carol Diaz (E5005)") || !strings.Contains(carol, "Everyone who is signed in") || !strings.Contains(carol, ">Full control<") {
		t.Errorf("Carol's summary of Staff:\n%s", carol)
	}
	if strings.Contains(carol, "Alice Wong (E1001)") {
		t.Errorf("Carol's summary names a person who has no grant on any folder she controls")
	}

	// The administrator sees all of it.
	_, admin := w.admin.get("/settings")
	if !strings.Contains(admin, "Nobody but administrators can see this folder yet.") || !strings.Contains(admin, "Alice Wong (E1001)") || !strings.Contains(admin, w.root[w.secret]) {
		t.Errorf("the administrator's settings page:\n%s", admin)
	}
	assertCSPClean(t, "/settings?edit", func() string { _, b := w.carol.get("/settings?edit=" + itoa(w.staff)); return b }())
}

func TestNoFolderIsVisibleToAVisitorWhenNoFolderIsGivenToThem(t *testing.T) {
	w := newFolderWorld(t)
	// Take Team Files away from guests: they have no folder left.
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+itoa(w.team), map[string]any{"access": map[string]any{"backup": []string{"everyone"}}}); status != http.StatusOK {
		t.Fatalf("change = %d %v", status, out)
	}
	if got := sourcesOf(t, w.guest); len(got) != 0 {
		t.Errorf("a guest sees %v", got)
	}
	res, _ := w.guest.get("/")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/backups" {
		t.Errorf("home for a guest with no folder = %d %q, want a redirect to /backups", res.StatusCode, res.Header.Get("Location"))
	}
	for _, page := range []string{"/explorer", "/history", "/backups"} {
		res, body := w.guest.get(page)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "No monitored folder to show") {
			t.Errorf("%s for a guest with no folder = %d\n%s", page, res.StatusCode, body)
		}
		if strings.Contains(body, "Team Files") || strings.Contains(body, "Staff") {
			t.Errorf("%s names a folder that may not be seen", page)
		}
	}
	// Signing in brings them back: everybody who is signed in may back Team Files up.
	res, _ = w.alice.get("/")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/explorer" {
		t.Errorf("home for Alice = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	// A guest who asks for a page that needs an account is told that signing in may show it; a signed-in account is not.
	if _, body := w.guest.get("/jobs/" + itoa(w.job[w.team])); !strings.Contains(body, "try signing in") {
		t.Errorf("a guest is not told to sign in:\n%s", body)
	}
	if _, body := w.alice.get("/jobs/" + itoa(w.job[w.secret])); strings.Contains(body, "try signing in") {
		t.Errorf("a signed-in account is told to sign in")
	}
}
