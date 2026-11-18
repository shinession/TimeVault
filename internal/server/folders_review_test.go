package server_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
)

// The cases of this file came out of the review of the permission system: each is a way in that the first version
// of it left open.

func TestAnIdempotencyKeyDoesNotHandOutAJobOfAnotherAccountOrFolder(t *testing.T) {
	w := newFolderWorld(t)
	start := func(v *harness, id int64, dest, key string) (int, map[string]any) {
		_, prev := v.api("POST", "/api/v1/restores/preview", map[string]any{"source_id": id, "point_id": w.point[id], "paths": []string{""}, "dest_name": dest})
		return v.api("POST", "/api/v1/restores", map[string]any{
			"source_id": id, "point_id": w.point[id], "paths": []string{""}, "dest_name": dest,
			"confirmation_token": prev["confirmation_token"], "idempotency_key": key,
		})
	}
	status, first := start(w.alice, w.staff, "restore-a", "shared-key")
	if status != http.StatusAccepted {
		t.Fatalf("Alice restores from Staff = %d %v", status, first)
	}
	id := num(first["job"].(map[string]any)["id"])
	w.alice.waitJob(id)

	// The same account repeating the same request gets its own job back.
	if status, again := start(w.alice, w.staff, "restore-a", "shared-key"); status != http.StatusAccepted || num(again["job"].(map[string]any)["id"]) != id {
		t.Errorf("Alice repeats the restore = %d %v; want her job %d again", status, again, id)
	}
	// Another account that happens to use the key (a client that numbers its keys) does not get Alice's job, and
	// learns nothing of it.
	status, carol := start(w.carol, w.team, "restore-c", "shared-key")
	if status != http.StatusConflict || carol["job"] != nil || strings.Contains(errorMessage(carol), itoa(id)) {
		t.Errorf("Carol uses Alice's key = %d %v, want 409 without the job", status, carol)
	}
	// The same account with another folder does not either.
	if status, other := start(w.alice, w.team, "restore-a2", "shared-key"); status != http.StatusConflict || other["job"] != nil {
		t.Errorf("Alice uses her key for another folder = %d %v, want 409 without the job", status, other)
	}
	// Nothing was started by the refused requests.
	var n int
	if err := w.env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE kind = 'restore'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d restore jobs, %v; want only Alice's", n, err)
	}
}

func TestABrowsingPageChecksTheAccountBeforeItShowsWhatOnlyTheAccountMaySee(t *testing.T) {
	w := newFolderWorld(t)
	sees := func(v *harness, folder string) bool {
		got := strings.Join(sourcesOf(t, v), " ")
		return strings.Contains(got, folder+"=")
	}
	canBrowse := func(v *harness, id int64) bool {
		status, _ := v.api("GET", "/api/v1/files?source_id="+itoa(id), nil)
		return status == http.StatusOK
	}
	if !sees(w.alice, "Named") || !canBrowse(w.alice, w.named) {
		t.Fatal("Alice can browse Named")
	}

	// While the directory cannot be asked, a session is trusted as issued: browsing keeps working for the people
	// who were signed in (this is how the sign-in has always behaved; see ADR 0005).
	w.dir.setDown(true)
	w.clock.Advance(2 * time.Minute)
	if !canBrowse(w.alice, w.named) {
		t.Error("during an outage of the user directory Alice, who is signed in, cannot browse")
	}
	w.dir.setDown(false)
	w.clock.Advance(time.Minute)

	// A deactivated account is a guest on the pages that read the folders, not only on the ones that change something.
	w.dir.change(userID, func(a *fakeAccount) { a.Inactive = true })
	w.clock.Advance(2 * time.Minute)
	if canBrowse(w.alice, w.named) {
		t.Error("a deactivated account still browses a folder that only it may see")
	}
	if got := strings.Join(sourcesOf(t, w.alice), " "); got != "Team Files=browse" {
		t.Errorf("a deactivated account sees %q; it is a guest now", got)
	}
	if _, page := w.alice.get("/explorer"); strings.Contains(page, ">Named</option>") || strings.Contains(page, ">Staff</option>") {
		t.Errorf("the Explorer of a deactivated account lists folders that are not a guest's")
	}

	// An administrator who is made an ordinary account is no administrator on the next check.
	if !canBrowse(w.admin, w.secret) {
		t.Fatal("the administrator can browse Secret")
	}
	w.dir.change(adminID, func(a *fakeAccount) { a.Role = auth.RoleUser })
	w.clock.Advance(2 * time.Minute)
	if canBrowse(w.admin, w.secret) {
		t.Error("an account that is no administrator any more still browses a folder that only administrators may see")
	}
	if _, body := w.admin.get("/explorer?source=" + itoa(w.secret)); strings.Contains(body, w.root[w.secret]) {
		t.Errorf("the page shows the folder on the server's disk to an account that is no administrator any more")
	}
}

func TestAnEmployeeSpelledLikeAGroupIsNotTheGroup(t *testing.T) {
	w := newFolderWorld(t)
	w.dir.put(fakeAccount{ID: "EVERYONE", Name: "Odd Person", Password: "odd-secret", Role: auth.RoleUser})
	staff := itoa(w.staff)

	// The editor writes every person as user:<ID>, also this one.
	_, form := w.admin.get("/settings?edit=" + staff)
	if !strings.Contains(form, `value="user:EVERYONE" data-access-person`) || !strings.Contains(form, `value="user:`+userID+`" data-access-person`) {
		t.Fatalf("the editor does not write the people as user:<ID>:\n%s", form)
	}
	res, body := w.admin.postForm("/settings/sources/"+staff, url.Values{
		"name": {"Staff"}, "enabled": {"1"}, "access_set": {"1"}, "access_full": {"user:EVERYONE"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", res.StatusCode, body)
	}
	spec := w.folder(t, w.staff)["access"].(map[string]any)
	if got := strings.Join(toStrings(spec["full"]), ","); got != "user:EVERYONE" || len(toStrings(spec["browse"])) != 0 {
		t.Fatalf("the lists = %v; Full is for the one employee called EVERYONE", spec)
	}
	// Alice is nobody on Staff now. Had the name been taken for the group she would have Full.
	if got := strings.Join(sourcesOf(t, w.alice), " "); strings.Contains(got, "Staff=") {
		t.Errorf("Alice sees %q: the employee called EVERYONE was taken for everybody", got)
	}
	odd := w.visitor()
	odd.loginAs("EVERYONE")
	if got := strings.Join(sourcesOf(t, odd), " "); !strings.Contains(got, "Staff=full") {
		t.Errorf("the employee called EVERYONE sees %q, want Staff=full", got)
	}
	// The API says the same, and takes the same spelling.
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"backup": []string{"user:everyone"}}}); status != http.StatusOK {
		t.Fatalf("PATCH = %d %v", status, out)
	}
	if got := strings.Join(sourcesOf(t, w.alice), " "); strings.Contains(got, "Staff=") {
		t.Errorf("Alice sees %q after the API change", got)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, e := range v.([]any) {
		out = append(out, e.(string))
	}
	return out
}

func TestTheAuditRecordOfARefusalNamesTheRouteNotTheTypedAddress(t *testing.T) {
	w := newFolderWorld(t)
	// A request that is refused (here: no CSRF token) with an address that quotes a folder of the server that Alice may
	// not see, in the hope that the audit log shows what it stands for. The record carries the route, not her text.
	hostile := w.root[w.secret]
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if res, _ := w.alice.do(http.MethodPost, "/settings/sources/"+url.PathEscape(hostile), strings.NewReader(""), form); res.StatusCode != http.StatusForbidden {
		t.Fatalf("the refused request = %d", res.StatusCode)
	}
	if res, _ := w.alice.do(http.MethodPost, "/jobs/"+itoa(w.job[w.team])+"/cancel", strings.NewReader(""), form); res.StatusCode != http.StatusForbidden {
		t.Fatalf("the second refused request = %d", res.StatusCode)
	}
	_, out := w.admin.api("GET", "/api/v1/audit?action=access.denied&actor="+userID, nil)
	routes := map[string]bool{}
	for _, e := range out["events"].([]any) {
		d := e.(map[string]any)["detail"].(map[string]any)
		routes[d["path"].(string)] = true
		if strings.Contains(d["path"].(string), "Temp") || strings.Contains(d["path"].(string), `\`) {
			t.Errorf("the record quotes the address that was typed: %v", d)
		}
	}
	if !routes["/settings/sources/{id}"] || !routes["/jobs/"+itoa(w.job[w.team])+"/cancel"] {
		t.Errorf("the records name the routes, with the numbers that were captured: %v", routes)
	}
	for _, v := range []*harness{w.alice, w.carol} {
		if _, page := v.get("/audit"); strings.Contains(page, "Secret") {
			t.Errorf("the audit page names a folder that may not be seen:\n%s", page)
		}
	}
}

func TestTheStatusCountsOnlyTheJobsOfFoldersTheVisitorMayBrowse(t *testing.T) {
	w := newFolderWorld(t)
	insert := func(query string, source int64) {
		t.Helper()
		if _, err := w.env.Pool.Exec(context.Background(), query, source); err != nil {
			t.Fatal(err)
		}
	}
	// A backup of Secret that is running. Nothing owns it, so it stays that way for the test.
	insert(`INSERT INTO jobs (kind, source_id, state, params, requested_by, started_at) VALUES ('backup', $1, 'running', '{}', 'admin', now())`, w.secret)
	// A deletion of old versions of Staff that is running, and a backup of Staff that waits for it: jobs of a folder
	// that Alice and Carol see, so that a count that is always zero cannot pass.
	insert(`INSERT INTO jobs (kind, source_id, state, params, requested_by, started_at) VALUES ('prune', $1, 'running', '{}', 'admin', now())`, w.staff)
	insert(`INSERT INTO jobs (kind, source_id, state, params, requested_by) VALUES ('backup', $1, 'queued', '{}', 'admin')`, w.staff)
	counts := func(v *harness) (running, queued float64) {
		_, out := v.api("GET", "/api/v1/health", nil)
		running, _ = out["jobs_running"].(float64)
		queued, _ = out["jobs_queued"].(float64)
		return running, queued
	}
	if running, queued := counts(w.admin); running != 2 || queued != 1 {
		t.Fatalf("the administrator sees %v running and %v queued jobs, want 2 and 1", running, queued)
	}
	for who, v := range map[string]*harness{"Alice": w.alice, "Carol": w.carol} {
		if running, queued := counts(v); running != 1 || queued != 1 {
			t.Errorf("%s is told about %v running and %v queued jobs, want the 1 and 1 of Staff and none of Secret", who, running, queued)
		}
		if _, page := v.get("/status"); !strings.Contains(page, "1 running, 1 queued") {
			t.Errorf("%s: the status page does not count the jobs of the folders she sees:\n%s", who, page)
		}
	}
}

func TestOldCancellationsBelongToTheFolderOfTheirJob(t *testing.T) {
	w := newFolderWorld(t)
	// Version 0.1.6 recorded the cancellation of a job without its folder.
	log := audit.New(w.env.Pool, slog.New(slog.DiscardHandler))
	for _, id := range []string{itoa(w.job[w.named]), itoa(w.job[w.secret]), "987654"} {
		if err := log.Record(context.Background(), audit.Event{Actor: "someone", Action: audit.ActionJobCancel, TargetType: "job", TargetID: id, Result: audit.ResultOK}); err != nil {
			t.Fatal(err)
		}
	}
	targets := func(v *harness) string {
		_, out := v.api("GET", "/api/v1/audit?action=job.cancel&limit=100", nil)
		var ids []string
		for _, e := range out["events"].([]any) {
			ids = append(ids, e.(map[string]any)["target_id"].(string))
		}
		return strings.Join(ids, ",")
	}
	if got := targets(w.admin); !strings.Contains(got, itoa(w.job[w.secret])) || !strings.Contains(got, "987654") || !strings.Contains(got, itoa(w.job[w.named])) {
		t.Fatalf("the administrator sees %q, want all three", got)
	}
	if got := targets(w.alice); got != itoa(w.job[w.named]) {
		t.Errorf("Alice sees %q, want only the cancellation of a job of Named", got)
	}
	if got := targets(w.carol); got != "" {
		t.Errorf("Carol sees %q; the jobs are of folders that she may not see, or are gone", got)
	}
}

// The cases below came out of the second review.

var accessRev = regexp.MustCompile(`name="access_rev" value="([0-9a-f]{32})"`)

// revOf finds the revision of the grants that an edit form carries.
func revOf(t *testing.T, page string) string {
	t.Helper()
	m := accessRev.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the form carries no access_rev:\n%s", page)
	}
	return m[1]
}

func TestASavedFormDoesNotBringBackGrantsThatWereTakenAwayMeanwhile(t *testing.T) {
	w := newFolderWorld(t)
	staff := itoa(w.staff)

	// Carol opens the form of Staff, which everybody who is signed in browses and she controls.
	_, opened := w.carol.get("/settings?edit=" + staff)
	rev := revOf(t, opened)
	if _, add := w.admin.get("/settings?add=1"); strings.Contains(add, `name="access_rev"`) {
		t.Error("the form that adds a folder carries the revision of grants that do not exist yet")
	}

	// Meanwhile an administrator takes "everybody" away and names Alice instead.
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"browse": []string{userID}, "full": []string{otherID}}}); status != http.StatusOK {
		t.Fatalf("the administrator changes the lists = %d %v", status, out)
	}

	// What Carol's browser posts back when she only changed the schedule: the editor is as it was built.
	form := func(rev string) url.Values {
		return url.Values{
			"name": {"Staff"}, "enabled": {"1"}, "schedule": {"1"}, "schedule_minutes": {"30"}, "max_versions": {"0"},
			"access_set": {"1"}, "access_rev": {rev}, "access_browse": {"everyone"}, "access_full": {"user:" + otherID},
		}
	}
	if res, body := w.carol.postForm("/settings/sources/"+staff, form(rev)); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("Carol saves the schedule = %d\n%s", res.StatusCode, body)
	}
	lists := func() string {
		spec := w.folder(t, w.staff)["access"].(map[string]any)
		return fmt.Sprint(spec["browse"], spec["backup"], spec["full"])
	}
	if got := w.folder(t, w.staff); got["schedule_minutes"] != float64(30) || lists() != "["+userID+"] [] ["+otherID+"]" {
		t.Fatalf("after Carol saved the schedule: %v %s; the schedule is hers, and the lists are still the administrator's", got["schedule_minutes"], lists())
	}
	_, events := w.admin.api("GET", "/api/v1/audit?action=source.update&actor="+otherID, nil)
	if changed := events["events"].([]any)[0].(map[string]any)["detail"].(map[string]any); changed["schedule_minutes"] == nil || changed["access"] != nil {
		t.Errorf("the record of Carol's save says %v; it changed the schedule and nothing else", changed)
	}

	// Had she changed the lists as well, her form would be refused: it was made from lists that are gone. Nothing
	// is saved, not even the name, and the form comes back with the lists as they are now and with what she typed.
	stale := form(rev)
	stale.Set("name", "Staff Renamed")
	stale.Set("access_backup", "user:"+userID)
	res, body := w.carol.postForm("/settings/sources/"+staff, stale)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "Somebody else changed who may do what") {
		t.Fatalf("a stale form that changes the lists = %d\n%s", res.StatusCode, body)
	}
	if got := w.folder(t, w.staff); got["name"] != "Staff" || lists() != "["+userID+"] [] ["+otherID+"]" {
		t.Errorf("a refused form changed something: %v %s", got["name"], lists())
	}
	now := revOf(t, body)
	if _, fresh := w.carol.get("/settings?edit=" + staff); now == rev || now != revOf(t, fresh) {
		t.Errorf("the form that comes back carries the revision %s; it should be the current one, not %s", now, rev)
	}
	if !strings.Contains(body, `value="Staff Renamed"`) || strings.Contains(body, `value="everyone" data-access-group checked`) ||
		strings.Count(body, `value="user:`+userID+`" data-access-person checked`) != 1 {
		t.Errorf("the form that comes back does not show what she typed with the lists as they are now:\n%s", body)
	}

	// With the revision she was given, the same change goes through.
	again := form(now)
	again.Set("name", "Staff Renamed")
	again.Del("access_browse")
	again.Set("access_backup", "user:"+userID)
	if res, body := w.carol.postForm("/settings/sources/"+staff, again); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("the form with the current revision = %d\n%s", res.StatusCode, body)
	}
	if got := w.folder(t, w.staff); got["name"] != "Staff Renamed" || lists() != "[] ["+userID+"] ["+otherID+"]" {
		t.Errorf("after the change that was repeated: %v %s", got["name"], lists())
	}

	// A form that says nothing of the revision (an old page, a script) replaces the lists as it always did.
	old := form("")
	old.Del("access_rev")
	if res, body := w.carol.postForm("/settings/sources/"+staff, old); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("a form without a revision = %d\n%s", res.StatusCode, body)
	}
	if got := lists(); got != "[everyone] [] ["+otherID+"]" {
		t.Errorf("lists after a form without a revision: %s", got)
	}
}

func TestAnAccessListThatIsSentWithItsRevisionIsRefusedWhenTheGrantsChangedSince(t *testing.T) {
	w := newFolderWorld(t)
	staff := itoa(w.staff)
	staffFor := func(v *harness) map[string]any {
		t.Helper()
		_, out := v.api("GET", "/api/v1/sources", nil)
		for _, s := range out["sources"].([]any) {
			if m := s.(map[string]any); num(m["id"]) == w.staff {
				return m
			}
		}
		t.Fatalf("Staff is not in the list of the sources: %v", out)
		return nil
	}
	patch := func(v *harness, body map[string]any) (int, map[string]any) {
		return v.api("PATCH", "/api/v1/sources/"+staff, body)
	}
	code := func(out map[string]any) any {
		e, _ := out["error"].(map[string]any)
		return e["code"]
	}

	rev, _ := staffFor(w.carol)["access_rev"].(string)
	if len(rev) != 32 {
		t.Fatalf("Carol controls Staff and is given no access_rev: %v", staffFor(w.carol))
	}
	if got := staffFor(w.alice); got["access"] != nil || got["access_rev"] != nil {
		t.Errorf("Alice only browses Staff and is told who may do what with it: %v", got)
	}
	if staffFor(w.admin)["access_rev"] != rev {
		t.Errorf("the administrator is given another revision than Carol for the same lists")
	}

	// An administrator changes the lists after Carol read them.
	if status, out := patch(w.admin, map[string]any{"access": map[string]any{"browse": []string{"everyone"}, "backup": []string{userID}, "full": []string{otherID}}}); status != http.StatusOK {
		t.Fatalf("the administrator changes the lists = %d %v", status, out)
	}
	if staffFor(w.carol)["access_rev"] == rev {
		t.Fatal("the revision did not change with the lists")
	}
	before := fmt.Sprint(staffFor(w.carol)["access"])

	mine := map[string]any{"access": map[string]any{"browse": []string{"everyone"}, "full": []string{otherID, adminID}}, "access_rev": rev}
	if status, out := patch(w.carol, mine); status != http.StatusConflict || code(out) != "access_changed" {
		t.Fatalf("lists made from an old read = %d %v, want 409 access_changed", status, out)
	}
	if got := fmt.Sprint(staffFor(w.carol)["access"]); got != before {
		t.Errorf("a refused change replaced the lists: %s -> %s", before, got)
	}

	// Read again, the same change goes through, and its answer names the new state for the next one.
	current := staffFor(w.carol)["access_rev"].(string)
	mine["access_rev"] = current
	status, out := patch(w.carol, mine)
	if status != http.StatusOK || out["access_rev"] == current || out["access_rev"] == nil {
		t.Fatalf("the change with the current revision = %d %v", status, out)
	}
	if out["access_rev"] != staffFor(w.carol)["access_rev"] {
		t.Errorf("the answer names the revision %v, the list of the sources %v", out["access_rev"], staffFor(w.carol)["access_rev"])
	}
	// The old revision is no good any more.
	if status, out := patch(w.carol, mine); status != http.StatusConflict || code(out) != "access_changed" {
		t.Errorf("the revision that was used = %d %v, want 409", status, out)
	}

	// Without a revision the lists are replaced, as the API has always done; and a revision that comes with no lists
	// is not looked at, because nothing is replaced.
	if status, out := patch(w.carol, map[string]any{"access": map[string]any{"browse": []string{"everyone"}, "full": []string{otherID}}}); status != http.StatusOK {
		t.Errorf("lists without a revision = %d %v", status, out)
	}
	if status, out := patch(w.carol, map[string]any{"name": "Staff 2", "access_rev": strings.Repeat("0", 32)}); status != http.StatusOK || out["name"] != "Staff 2" {
		t.Errorf("a revision without lists = %d %v", status, out)
	}
	if status, out := patch(w.carol, map[string]any{"access": map[string]any{"browse": []string{"everyone"}, "full": []string{otherID}}, "access_rev": "not a revision"}); status != http.StatusConflict || code(out) != "access_changed" {
		t.Errorf("lists with a revision that is none = %d %v, want 409", status, out)
	}
}

func TestWhoMayDoWhatIsOnlyShownInTheAuditToThoseWhoMayChangeIt(t *testing.T) {
	w := newFolderWorld(t)
	staff, team := itoa(w.staff), itoa(w.team)
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"browse": []string{"everyone"}, "backup": []string{userID}, "full": []string{otherID}}}); status != http.StatusOK {
		t.Fatalf("the administrator changes the lists = %d %v", status, out)
	}
	// Team Files already has these lists but one: naming the administrator is what makes it a change that is recorded.
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+team, map[string]any{"access": map[string]any{"browse": []string{"guest"}, "backup": []string{"everyone"}, "full": []string{adminID}}}); status != http.StatusOK {
		t.Fatalf("the administrator changes the lists of Team Files = %d %v", status, out)
	}
	// accessOf is what the audit API says about the lists in an event of a folder: the creation of Staff, and the
	// changes of both folders. (Team Files was made by the test set-up, not through the service, so it has no creation.)
	accessOf := func(v *harness, action, folder string) any {
		t.Helper()
		_, out := v.api("GET", "/api/v1/audit?action="+action+"&limit=100", nil)
		for _, e := range out["events"].([]any) {
			if m := e.(map[string]any); m["target_id"] == folder {
				return m["detail"].(map[string]any)["access"]
			}
		}
		t.Fatalf("no %s event of folder %s in what the audit log gives: %v", action, folder, out)
		return nil
	}
	const hidden = "[hidden]"
	for _, event := range []struct{ action, folder string }{{"source.create", staff}, {"source.update", staff}, {"source.update", team}} {
		for _, c := range []struct {
			who   string
			v     *harness
			shown bool
		}{
			{"the administrator", w.admin, true},
			// Carol controls Staff and may only back up Team Files; Alice only browses Staff and backs up Team Files.
			{"Carol", w.carol, event.folder == staff},
			{"Alice", w.alice, false},
		} {
			got := accessOf(c.v, event.action, event.folder)
			if (got != hidden) != c.shown || (c.shown && got == nil) {
				t.Errorf("%s: %s of folder %s shows %v; the lists are for those who may change them (shown: %v)", c.who, event.action, event.folder, got, c.shown)
			}
		}
	}
	// The page does not show them either. The lists of Staff name Carol as the one with full control; that is what
	// the page of Carol shows and the page of Alice does not (a sign-in of Carol is an event of no folder, and is
	// shown to both).
	lists := "&#34;full&#34;:[&#34;" + otherID
	if _, page := w.alice.get("/audit"); !strings.Contains(page, hidden) || strings.Contains(page, lists) {
		t.Errorf("the audit page of Alice shows who may do what:\n%s", page)
	}
	if _, page := w.carol.get("/audit"); !strings.Contains(page, lists) {
		t.Errorf("the audit page of Carol does not show the lists of the folder she controls:\n%s", page)
	}
	// The event is still there for Alice, so that she can tell the permissions were changed.
	_, out := w.alice.api("GET", "/api/v1/audit?action=source.update&limit=100", nil)
	found := false
	for _, e := range out["events"].([]any) {
		if m := e.(map[string]any); m["target_id"] == staff {
			found = true
		}
	}
	if !found {
		t.Error("Alice is not shown that the permissions of a folder she browses were changed")
	}
}

func TestAPointOfAnotherFolderIsAnsweredLikeOneThatDoesNotExist(t *testing.T) {
	w := newFolderWorld(t)
	const missing = 987654
	request := func(point int64) map[string]any {
		return map[string]any{"source_id": w.staff, "point_id": point, "paths": []string{""}, "dest_name": "probe"}
	}
	// What Alice, who browses Staff, is told when she names a point of a folder she may not see, of one that she may see,
	// and of none: it must be the same.
	points := map[string]int64{"a point of a hidden folder": w.point[w.secret], "a point of another folder she sees": w.point[w.team], "a point that is not there": missing}
	preview := map[string]string{}
	for name, point := range points {
		status, out := w.alice.api("POST", "/api/v1/restores/preview", request(point))
		preview[name] = fmt.Sprint(status, " ", out)
	}
	if !strings.HasPrefix(preview["a point that is not there"], "404 ") {
		t.Fatalf("a point that does not exist = %q, want 404", preview["a point that is not there"])
	}
	for name, got := range preview {
		if got != preview["a point that is not there"] {
			t.Errorf("preview with %s = %q; a point that does not exist = %q", name, got, preview["a point that is not there"])
		}
	}

	// The same on the page.
	page := map[string]string{}
	for name, point := range points {
		res, body := w.alice.postForm("/restore/preview", url.Values{"source_id": {itoa(w.staff)}, "point_id": {itoa(point)}, "scope": {"folder"}, "current_path": {""}, "dest_name": {"probe"}})
		page[name] = fmt.Sprint(res.StatusCode, " ", headline.FindString(body))
	}
	for name, got := range page {
		if got != page["a point that is not there"] || !strings.HasPrefix(got, "404 ") {
			t.Errorf("page: preview with %s = %q; a point that does not exist = %q", name, got, page["a point that is not there"])
		}
	}

	// Starting a restore with a confirmation that is good for Staff does not tell them apart either.
	_, prev := w.alice.api("POST", "/api/v1/restores/preview", request(w.point[w.staff]))
	if prev["confirmation_token"] == nil {
		t.Fatalf("Alice cannot preview a restore of Staff: %v", prev)
	}
	start := map[string]string{}
	for name, point := range points {
		body := request(point)
		body["confirmation_token"], body["idempotency_key"] = prev["confirmation_token"], "probe-"+strings.ReplaceAll(name, " ", "-")
		status, out := w.alice.api("POST", "/api/v1/restores", body)
		start[name] = fmt.Sprint(status, " ", out)
	}
	for name, got := range start {
		if got != start["a point that is not there"] || !strings.HasPrefix(got, "404 ") {
			t.Errorf("start with %s = %q; a point that does not exist = %q", name, got, start["a point that is not there"])
		}
	}
}
