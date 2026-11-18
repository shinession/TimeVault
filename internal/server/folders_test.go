package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// folderWorld is a server with four monitored folders that give different people different rights:
//
//	Team Files  guests browse, everybody who is signed in may back up (what every folder allowed before levels)
//	Staff       everybody who is signed in browses, Carol has full control
//	Named       only Alice, who may back it up
//	Secret      nobody but the administrators
//
// Every folder has been backed up once.
type folderWorld struct {
	*harness
	admin, alice, carol, guest *harness
	team, staff, named, secret int64
	// point and job are the recovery point and the backup job of each folder, by folder id; root is its folder on
	// the server's disk.
	point, job map[int64]int64
	root       map[int64]string
}

func newFolderWorld(t *testing.T) *folderWorld {
	t.Helper()
	h := newHarness(t)
	seed(h)
	w := &folderWorld{harness: h, team: h.env.Src.ID, point: map[int64]int64{}, job: map[int64]int64{}, root: map[int64]string{h.env.Src.ID: h.env.SrcDir}}
	w.admin, w.alice, w.carol, w.guest = h.visitor(), h.visitor(), h.visitor(), h.visitor()
	w.admin.loginAs(adminID)
	w.alice.loginAs(userID)
	w.carol.loginAs(otherID)

	add := func(name string, grants map[string]any) int64 {
		t.Helper()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, strings.ToLower(name)+"-notes.txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"name": name, "root_path": root}
		if grants != nil {
			body["access"] = grants
		}
		status, out := w.admin.api("POST", "/api/v1/sources", body)
		if status != http.StatusCreated {
			t.Fatalf("add %s = %d %v", name, status, out)
		}
		w.root[num(out["id"])] = root
		return num(out["id"])
	}
	w.staff = add("Staff", map[string]any{"browse": []string{"everyone"}, "full": []string{otherID}})
	w.named = add("Named", map[string]any{"backup": []string{userID}})
	w.secret = add("Secret", nil)

	for _, id := range []int64{w.team, w.staff, w.named, w.secret} {
		status, out := w.admin.api("POST", "/api/v1/backups", map[string]any{"source_id": id})
		if status != http.StatusAccepted && status != http.StatusOK {
			t.Fatalf("back up folder %d = %d %v", id, status, out)
		}
		job := num(out["job"].(map[string]any)["id"])
		w.admin.waitJob(job)
		w.job[id] = job
		_, pts := w.admin.api("GET", "/api/v1/recovery-points?source_id="+itoa(id), nil)
		list, _ := pts["recovery_points"].([]any)
		if len(list) == 0 {
			t.Fatalf("folder %d has no recovery point: %v", id, pts)
		}
		w.point[id] = num(list[0].(map[string]any)["id"])
	}
	return w
}

// sourcesOf lists "name=level" of the folders the visitor sees, by name.
func sourcesOf(t *testing.T, v *harness) []string {
	t.Helper()
	status, out := v.api("GET", "/api/v1/sources", nil)
	if status != http.StatusOK {
		t.Fatalf("sources = %d %v", status, out)
	}
	var got []string
	for _, s := range out["sources"].([]any) {
		m := s.(map[string]any)
		got = append(got, fmt.Sprintf("%v=%v", m["name"], m["level"]))
	}
	sort.Strings(got)
	return got
}

func TestEachVisitorSeesOnlyTheFoldersTheyMayBrowse(t *testing.T) {
	w := newFolderWorld(t)
	cases := []struct {
		who  string
		v    *harness
		want string
	}{
		{"guest", w.guest, "Team Files=browse"},
		{"Alice", w.alice, "Named=backup Staff=browse Team Files=backup"},
		{"Carol", w.carol, "Staff=full Team Files=backup"},
		{"admin", w.admin, "Named=full Secret=full Staff=full Team Files=full"},
	}
	for _, c := range cases {
		if got := strings.Join(sourcesOf(t, c.v), " "); got != c.want {
			t.Errorf("%s sees %q, want %q", c.who, got, c.want)
		}
	}

	// Who may do what with a folder, and the folder on the server's disk, only go to those who may use them.
	_, out := w.carol.api("GET", "/api/v1/sources", nil)
	for _, s := range out["sources"].([]any) {
		m := s.(map[string]any)
		if _, has := m["root_path"]; has {
			t.Errorf("an account that is not an administrator is told the folder on the server's disk: %v", m)
		}
		spec, hasSpec := m["access"].(map[string]any)
		switch m["name"] {
		case "Staff":
			if !hasSpec || fmt.Sprint(spec["full"]) != "["+otherID+"]" || fmt.Sprint(spec["browse"]) != "[everyone]" {
				t.Errorf("full control includes seeing who has access: %v", m)
			}
		default:
			if hasSpec {
				t.Errorf("who may do what with %v is told to somebody who may not change it: %v", m["name"], m)
			}
		}
	}
	_, out = w.admin.api("GET", "/api/v1/sources", nil)
	for _, s := range out["sources"].([]any) {
		if m := s.(map[string]any); m["root_path"] == nil || m["access"] == nil {
			t.Errorf("an administrator sees the folder on the disk and the access of every folder: %v", m)
		}
	}

	// The pages show the same folders: the selector of the Explorer and the Backups page.
	for _, c := range cases {
		_, explorer := c.v.get("/explorer")
		_, backups := c.v.get("/backups")
		for name, visible := range map[string]bool{
			"Team Files": true, "Staff": c.who != "guest", "Named": c.who == "Alice" || c.who == "admin", "Secret": c.who == "admin",
		} {
			if listed := cardOf(backups, name); listed != visible {
				t.Errorf("%s: the Backups page lists %s = %v, want %v", c.who, name, listed, visible)
			}
			if inSelector := strings.Contains(explorer, `>`+name+`</option>`); inSelector != visible {
				t.Errorf("%s: the Explorer selector lists %s = %v, want %v", c.who, name, inSelector, visible)
			}
		}
	}
}

// cardOf reports whether the Backups page has a card for the folder: its name in a heading, after the folder icon.
func cardOf(page, name string) bool {
	return strings.Contains(page, "</svg> "+name+" <") || strings.Contains(page, "</svg> "+name+"</h2>")
}

var headline = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)

func TestAFolderThatCannotBeSeenIsAnsweredLikeOneThatDoesNotExist(t *testing.T) {
	w := newFolderWorld(t)
	const missing = "987654"
	sec, pt, job := itoa(w.secret), itoa(w.point[w.secret]), itoa(w.job[w.secret])

	pages := func(source, point, job string) []string {
		return []string{
			"/explorer?source=" + source,
			"/history?source=" + source + "&point=" + point,
			"/versions?source=" + source + "&path=secret-notes.txt",
			"/jobs/" + job,
		}
	}
	apis := func(source, point, job string) []string {
		return []string{
			"/api/v1/files?source_id=" + source,
			"/api/v1/search?source_id=" + source + "&q=notes",
			"/api/v1/recovery-points?source_id=" + source,
			"/api/v1/history/tree?source_id=" + source + "&point_id=" + point,
			"/api/v1/history/versions?source_id=" + source + "&path=secret-notes.txt",
			"/api/v1/jobs/" + job,
		}
	}
	for who, v := range map[string]*harness{"guest": w.guest, "Alice": w.alice, "Carol": w.carol} {
		hidden, none := pages(sec, pt, job), pages(missing, pt, missing)
		for i := range hidden {
			resHidden, bodyHidden := v.get(hidden[i])
			resNone, bodyNone := v.get(none[i])
			if resHidden.StatusCode != http.StatusNotFound || resNone.StatusCode != http.StatusNotFound {
				t.Errorf("%s: %s = %d, %s = %d; both must be 404", who, hidden[i], resHidden.StatusCode, none[i], resNone.StatusCode)
				continue
			}
			if a, b := headline.FindString(bodyHidden), headline.FindString(bodyNone); a == "" || a != b {
				t.Errorf("%s: %s and %s answer differently: %q / %q", who, hidden[i], none[i], a, b)
			}
			// Only the way back to the page the visitor asked for is allowed to carry the address they typed.
			if strings.Contains(bodyHidden, "Secret") {
				t.Errorf("%s: %s names the hidden folder:\n%s", who, hidden[i], bodyHidden)
			}
			if signInHint := strings.Contains(bodyHidden, "try signing in"); signInHint != (who == "guest") || signInHint != strings.Contains(bodyNone, "try signing in") {
				t.Errorf("%s: %s tells %v to sign in, a missing folder tells %v", who, hidden[i], signInHint, strings.Contains(bodyNone, "try signing in"))
			}
		}
		hiddenAPI, noneAPI := apis(sec, pt, job), apis(missing, pt, missing)
		for i := range hiddenAPI {
			sh, oh := v.api("GET", hiddenAPI[i], nil)
			sn, on := v.api("GET", noneAPI[i], nil)
			if sh != http.StatusNotFound || sn != http.StatusNotFound || fmt.Sprint(oh) != fmt.Sprint(on) {
				t.Errorf("%s: %s = %d %v, %s = %d %v; they must be the same 404", who, hiddenAPI[i], sh, oh, noneAPI[i], sn, on)
			}
		}
	}

	// The administrator, who may see it, gets the folder.
	for _, p := range pages(sec, pt, job) {
		if res, _ := w.admin.get(p); res.StatusCode != http.StatusOK {
			t.Errorf("admin: %s = %d", p, res.StatusCode)
		}
	}
	for i, p := range apis(sec, pt, job) {
		if i == 1 {
			continue // the search engine is not set up in these tests; the answer for a folder that may be seen is its 503
		}
		if status, out := w.admin.api("GET", p, nil); status != http.StatusOK {
			t.Errorf("admin: %s = %d %v", p, status, out)
		}
	}
	// A folder somebody may browse is theirs to look at, whatever else they may not do with it.
	if status, _ := w.guest.api("GET", "/api/v1/files?source_id="+itoa(w.team), nil); status != http.StatusOK {
		t.Errorf("a guest browses a folder that lets guests browse: %d", status)
	}
	if status, _ := w.guest.api("GET", "/api/v1/files?source_id="+itoa(w.staff), nil); status != http.StatusNotFound {
		t.Errorf("a guest browses a folder that needs an account: %d", status)
	}
}

func TestBackingUpNeedsTheBackupLevelOfTheFolder(t *testing.T) {
	w := newFolderWorld(t)
	backup := func(v *harness, id int64, rehash bool) int {
		status, _ := v.api("POST", "/api/v1/backups", map[string]any{"source_id": id, "rehash": rehash})
		return status
	}
	ok := func(status int) bool { return status == http.StatusAccepted || status == http.StatusOK }

	// Alice may back up Team Files and Named, only browse Staff, and does not know Secret.
	if s := backup(w.alice, w.team, false); !ok(s) {
		t.Errorf("Alice backs up Team Files = %d", s)
	}
	if s := backup(w.alice, w.named, true); !ok(s) {
		t.Errorf("Alice backs up Named with a full re-read = %d", s)
	}
	if s := backup(w.alice, w.staff, false); s != http.StatusForbidden {
		t.Errorf("Alice backs up Staff, which she may only browse = %d, want 403", s)
	}
	if s := backup(w.alice, w.staff, true); s != http.StatusForbidden {
		t.Errorf("Alice re-reads Staff completely = %d, want 403", s)
	}
	if s := backup(w.alice, w.secret, false); s != http.StatusNotFound {
		t.Errorf("Alice backs up Secret = %d, want 404", s)
	}
	// Full control includes backing up. Carol does not know Named.
	if s := backup(w.carol, w.staff, true); !ok(s) {
		t.Errorf("Carol backs up Staff = %d", s)
	}
	if s := backup(w.carol, w.named, false); s != http.StatusNotFound {
		t.Errorf("Carol backs up Named = %d, want 404", s)
	}
	// Somebody who has not signed in is asked to; the administrator may back up everything.
	if s := backup(w.guest, w.team, false); s != http.StatusUnauthorized {
		t.Errorf("a guest backs up Team Files = %d, want 401", s)
	}
	if s := backup(w.admin, w.secret, false); !ok(s) {
		t.Errorf("the administrator backs up Secret = %d", s)
	}

	// The same through the forms of the Backups page, and for the verification of a recovery point.
	if res, _ := w.alice.postForm("/backups/run", url.Values{"source_id": {itoa(w.staff)}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("form: Alice backs up Staff = %d", res.StatusCode)
	}
	if res, _ := w.alice.postForm("/backups/run", url.Values{"source_id": {itoa(w.secret)}}); res.StatusCode != http.StatusNotFound {
		t.Errorf("form: Alice backs up Secret = %d", res.StatusCode)
	}
	verify := func(v *harness, id int64) int {
		res, _ := v.postForm("/backups/verify", url.Values{"source_id": {itoa(id)}, "point_id": {itoa(w.point[id])}, "deep": {"1"}})
		return res.StatusCode
	}
	if s := verify(w.alice, w.staff); s != http.StatusForbidden {
		t.Errorf("Alice verifies a point of Staff = %d, want 403", s)
	}
	if s := verify(w.alice, w.named); s != http.StatusSeeOther {
		t.Errorf("Alice verifies a point of Named = %d, want 303", s)
	}
	if s := verify(w.carol, w.named); s != http.StatusNotFound {
		t.Errorf("Carol verifies a point of Named = %d, want 404", s)
	}
	// A point id of another folder is no way in: the point must belong to the folder that was checked.
	res, _ := w.alice.postForm("/backups/verify", url.Values{"source_id": {itoa(w.named)}, "point_id": {itoa(w.point[w.secret])}})
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("Alice verifies the point of Secret through Named = %d, want 404", res.StatusCode)
	}

	// A refusal is audited with its folder, so the people of that folder see it and nobody else does.
	_, out := w.admin.api("GET", "/api/v1/audit?action=access.denied&actor="+userID, nil)
	events, _ := out["events"].([]any)
	var onStaff int
	for _, e := range events {
		m := e.(map[string]any)
		if num(m["source_id"]) == w.staff && m["detail"].(map[string]any)["needs"] == "backup" {
			onStaff++
		}
	}
	if onStaff < 3 {
		t.Errorf("Alice's refused backups of Staff are in the audit log with their folder, got %d of %v", onStaff, events)
	}
}

func TestWhoeverMayBrowseAFolderMayRestoreFromItButNeedsAnAccount(t *testing.T) {
	w := newFolderWorld(t)
	req := func(id int64) map[string]any {
		return map[string]any{"source_id": id, "point_id": w.point[id], "paths": []string{""}, "dest_name": "restore-test"}
	}
	// Alice may only browse Staff, and that is enough.
	status, out := w.alice.api("POST", "/api/v1/restores/preview", req(w.staff))
	if status != http.StatusOK || out["confirmation_token"] == nil {
		t.Fatalf("Alice previews a restore of Staff = %d %v", status, out)
	}
	start := req(w.staff)
	start["confirmation_token"], start["idempotency_key"] = out["confirmation_token"], "alice-staff-1"
	if status, out := w.alice.api("POST", "/api/v1/restores", start); status != http.StatusAccepted {
		t.Fatalf("Alice restores from Staff = %d %v", status, out)
	}
	// Not Secret, which she does not know, and not as a guest, who has no session to carry the confirmation.
	if status, _ := w.alice.api("POST", "/api/v1/restores/preview", req(w.secret)); status != http.StatusNotFound {
		t.Errorf("Alice previews a restore of Secret = %d, want 404", status)
	}
	if status, _ := w.guest.api("POST", "/api/v1/restores/preview", req(w.team)); status != http.StatusUnauthorized {
		t.Errorf("a guest previews a restore of a folder they may browse = %d, want 401", status)
	}
	// A confirmation for a folder cannot be used for another one.
	_, prev := w.carol.api("POST", "/api/v1/restores/preview", req(w.staff))
	cross := req(w.named)
	cross["confirmation_token"], cross["idempotency_key"] = prev["confirmation_token"], "carol-cross-1"
	if status, _ := w.carol.api("POST", "/api/v1/restores", cross); status != http.StatusNotFound {
		t.Errorf("Carol starts a restore of Named with a confirmation for Staff = %d, want 404", status)
	}
	// The pages behave the same way.
	form := url.Values{"source_id": {itoa(w.secret)}, "point_id": {itoa(w.point[w.secret])}, "scope": {"folder"}, "current_path": {""}, "dest_name": {"x"}}
	if res, _ := w.alice.postForm("/restore/preview", form); res.StatusCode != http.StatusNotFound {
		t.Errorf("page: Alice previews a restore of Secret = %d, want 404", res.StatusCode)
	}
	form.Set("source_id", itoa(w.staff))
	form.Set("point_id", itoa(w.point[w.staff]))
	if res, _ := w.alice.postForm("/restore/preview", form); res.StatusCode != http.StatusOK {
		t.Errorf("page: Alice previews a restore of Staff = %d, want 200", res.StatusCode)
	}
}

func TestJobsAndAuditEventsOfAHiddenFolderStayHidden(t *testing.T) {
	w := newFolderWorld(t)
	secretJob := itoa(w.job[w.secret])

	// The job page, the job API and the cancel button do not exist for those who cannot see the folder.
	if res, _ := w.alice.get("/jobs/" + secretJob); res.StatusCode != http.StatusNotFound {
		t.Errorf("Alice opens the job of Secret = %d", res.StatusCode)
	}
	if res, _ := w.alice.postForm("/jobs/"+secretJob+"/cancel", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("Alice cancels the job of Secret = %d", res.StatusCode)
	}
	// The Backups page lists the jobs of the folders she may see; her own show, Secret's do not.
	_, backups := w.alice.get("/backups")
	if strings.Contains(backups, "Secret") || strings.Contains(backups, `href="/jobs/`+secretJob+`"`) {
		t.Errorf("the Backups page of Alice shows the job of Secret:\n%s", backups)
	}
	_, adminBackups := w.admin.get("/backups")
	if !strings.Contains(adminBackups, `href="/jobs/`+secretJob+`"`) {
		t.Errorf("the Backups page of the administrator lacks the job of Secret")
	}

	// The audit log: what happened to Secret is for administrators; sign-ins belong to no folder and stay for everybody.
	events := func(v *harness) (ofFolder map[int64]int, withoutFolder int) {
		_, out := v.api("GET", "/api/v1/audit?limit=500", nil)
		ofFolder = map[int64]int{}
		for _, e := range out["events"].([]any) {
			m := e.(map[string]any)
			if m["source_id"] == nil {
				withoutFolder++
			} else {
				ofFolder[num(m["source_id"])]++
			}
		}
		return ofFolder, withoutFolder
	}
	adminSees, _ := events(w.admin)
	if adminSees[w.secret] == 0 || adminSees[w.named] == 0 {
		t.Fatalf("the administrator sees the events of every folder: %v", adminSees)
	}
	for who, v := range map[string]*harness{"Alice": w.alice, "Carol": w.carol} {
		sees, without := events(v)
		if sees[w.secret] != 0 {
			t.Errorf("%s sees %d audit events of Secret", who, sees[w.secret])
		}
		if without == 0 {
			t.Errorf("%s does not see the events that belong to no folder (sign-ins)", who)
		}
	}
	if sees, _ := events(w.carol); sees[w.named] != 0 || sees[w.staff] == 0 {
		t.Errorf("Carol sees the events of the folders she may see only: %v", sees)
	}
	if sees, _ := events(w.alice); sees[w.named] == 0 {
		t.Errorf("Alice sees the events of Named: %v", sees)
	}
	for _, v := range []*harness{w.alice, w.carol} {
		if _, page := v.get("/audit"); strings.Contains(page, "Secret") {
			t.Errorf("the audit page names a folder that may not be seen:\n%s", page)
		}
	}
}

func TestAJobIsStoppedByWhoMayBackUpItsFolderOrStartedTheRestore(t *testing.T) {
	w := newFolderWorld(t)
	w.dir.put(fakeAccount{ID: "E7007", Name: "Dan Ho", Password: "dan-secret", Role: "user"})
	dan := w.visitor()
	dan.loginAs("E7007")

	// Alice and Dan may only browse Staff. Alice restores from it.
	body := map[string]any{"source_id": w.staff, "point_id": w.point[w.staff], "paths": []string{""}, "dest_name": "restore-alice"}
	_, prev := w.alice.api("POST", "/api/v1/restores/preview", body)
	body["confirmation_token"], body["idempotency_key"] = prev["confirmation_token"], "alice-own-1"
	status, out := w.alice.api("POST", "/api/v1/restores", body)
	if status != http.StatusAccepted {
		t.Fatalf("restore = %d %v", status, out)
	}
	restoreID := num(out["job"].(map[string]any)["id"])
	w.alice.waitJob(restoreID)
	restore := itoa(restoreID)

	// Everybody who may browse Staff sees the job; those who may not do not know it.
	for who, v := range map[string]*harness{"Alice": w.alice, "Dan": dan, "Carol": w.carol} {
		if res, _ := v.get("/jobs/" + restore); res.StatusCode != http.StatusOK {
			t.Errorf("%s opens the restore job = %d", who, res.StatusCode)
		}
	}
	if res, _ := w.guest.get("/jobs/" + restore); res.StatusCode != http.StatusNotFound {
		t.Errorf("a guest opens the restore job of a folder that needs an account = %d", res.StatusCode)
	}
	// The one who started it stops it, and so does whoever may back the folder up; somebody who only browses may not.
	if res, _ := dan.postForm("/jobs/"+restore+"/cancel", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("Dan stops Alice's restore = %d, want 403", res.StatusCode)
	}
	if res, _ := w.alice.postForm("/jobs/"+restore+"/cancel", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("Alice stops her own restore = %d, want 303", res.StatusCode)
	}
	if res, _ := w.carol.postForm("/jobs/"+restore+"/cancel", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("Carol, who has full control of Staff, stops the restore = %d, want 303", res.StatusCode)
	}
	// A backup is only stopped by those who may back the folder up.
	backup := itoa(w.job[w.staff])
	if res, _ := w.alice.postForm("/jobs/"+backup+"/cancel", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("Alice stops a backup of Staff = %d, want 403", res.StatusCode)
	}
	if res, _ := w.carol.postForm("/jobs/"+backup+"/cancel", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("Carol stops a backup of Staff = %d, want 303", res.StatusCode)
	}
}
