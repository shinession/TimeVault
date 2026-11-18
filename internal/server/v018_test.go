package server_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"timevault/internal/audit"
	"timevault/internal/server"
	"timevault/internal/update"
)

// fakeUpdater is the automatic update the pages ask about.
type fakeUpdater struct {
	mu     sync.Mutex
	status update.Status
	checks int
}

var _ server.Updater = (*fakeUpdater)(nil)

func (f *fakeUpdater) Status() update.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeUpdater) CheckNow() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
}

func (f *fakeUpdater) set(st update.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = st
}

func (f *fakeUpdater) checked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

func withUpdater(f *fakeUpdater) func(*server.Options) {
	return func(o *server.Options) { o.Update = f }
}

func withPictures(o *server.Options) { o.AvatarURL = "/api/static/hr/{id}.webp" }

var partialHeader = map[string]string{"X-TV-Partial": "1"}

// releaseAddress and releaseProblem stand for places on the server's disk that only administrators may see.
const (
	releaseAddress = `\\release.test\TimeVault\release\timevault.json`
	releaseProblem = `open the program: open \\release.test\TimeVault\release\timevault.exe: access denied`
)

func waitingStatus() update.Status {
	return update.Status{
		Address: releaseAddress, Interval: 10 * time.Minute, Current: "0.1.8", Latest: "0.1.9", State: update.StateWaiting,
		CheckedAt: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), Waiting: 2, LastError: releaseProblem,
	}
}

func TestTheSettingsPageTellsAboutTheAutomaticUpdateAndOnlyAdministratorsMayAskForALook(t *testing.T) {
	up := &fakeUpdater{status: waitingStatus()}
	h := newHarnessOpts(t, nil, withUpdater(up))
	admin := h.visitor()
	admin.login()

	_, page := admin.get("/settings")
	for _, want := range []string{`id="updates"`, "v0.1.9", "v0.1.8", "Waiting for the running jobs", "2 running", releaseAddress, "access denied",
		`action="/settings/update/check"`, "Check now", "2026-03-04 05:06:07"} {
		if !strings.Contains(page, want) {
			t.Errorf("the administrator's settings page lacks %q", want)
		}
	}

	alice := h.visitor()
	alice.loginAs(userID)
	_, page = alice.get("/settings")
	for _, want := range []string{`id="updates"`, "v0.1.9", "Waiting for the running jobs"} {
		if !strings.Contains(page, want) {
			t.Errorf("an ordinary account's settings page lacks %q", want)
		}
	}
	for _, leak := range []string{"release.test", "access denied", "/settings/update/check", "Check now"} {
		if strings.Contains(page, leak) {
			t.Errorf("an ordinary account's settings page shows %q, which is for administrators", leak)
		}
	}

	if res, _ := alice.postForm("/settings/update/check", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("an ordinary account asked for a look: %d", res.StatusCode)
	}
	if res, _ := h.visitor().postForm("/settings/update/check", nil); res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "/login") {
		t.Errorf("a visitor was not sent to sign in: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	noToken := *admin
	noToken.csrf = ""
	if res, _ := noToken.postForm("/settings/update/check", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("a request without the CSRF token was accepted: %d", res.StatusCode)
	}
	if got := up.checked(); got != 0 {
		t.Fatalf("the updater was asked %d times by requests that must not count", got)
	}

	res, _ := admin.postForm("/settings/update/check", nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings#updates" {
		t.Fatalf("asking for a look: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if got := up.checked(); got != 1 {
		t.Fatalf("the updater was asked %d times, want 1", got)
	}
	if _, page = admin.get("/settings"); !strings.Contains(page, "Looking for a new version") {
		t.Error("the page does not say that a look was asked for")
	}
}

func TestWithoutAnUpdateAddressNothingIsConfiguredAndNoLookCanBeAsked(t *testing.T) {
	h := newHarness(t)
	h.login()

	_, page := h.get("/settings")
	card := regexp.MustCompile(`(?s)id="updates".*?</section>`).FindString(page)
	if card == "" || !strings.Contains(card, "not configured") {
		t.Error("the settings page does not say that automatic updates are off")
	}
	if strings.Contains(page, "Check now") {
		t.Error("the settings page offers a look that cannot happen")
	}
	if res, _ := h.postForm("/settings/update/check", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("the form post = %d", res.StatusCode)
	}
	if _, page = h.get("/settings"); !strings.Contains(page, "Automatic updates are not turned on.") {
		t.Error("the page does not say why nothing was looked at")
	}

	status, out := h.api("POST", "/api/v1/update/check", nil)
	if status != http.StatusConflict || out["error"].(map[string]any)["code"] != "updates_off" {
		t.Errorf("API check = %d %v", status, out)
	}
	status, out = h.api("GET", "/api/v1/settings", nil)
	u, _ := out["update"].(map[string]any)
	if status != http.StatusOK || u["enabled"] != false || len(u) != 1 {
		t.Errorf("API settings update = %d %v", status, u)
	}
}

func TestTheAPIDescribesTheAutomaticUpdateToEveryAccountAndHidesPathsFromThoseWhoAreNotAdministrators(t *testing.T) {
	up := &fakeUpdater{status: waitingStatus()}
	h := newHarnessOpts(t, nil, withUpdater(up))
	admin, alice := h.visitor(), h.visitor()
	admin.login()
	alice.loginAs(userID)

	_, out := alice.api("GET", "/api/v1/settings", nil)
	u, _ := out["update"].(map[string]any)
	if u["enabled"] != true || u["state"] != "waiting" || u["latest_version"] != "0.1.9" || num(u["waiting_jobs"]) != 2 || u["check_interval"] != "10m0s" || u["checked_at"] == nil {
		t.Errorf("update as an ordinary account = %v", u)
	}
	if _, ok := u["manifest"]; ok {
		t.Error("an ordinary account got the address of the release")
	}
	if _, ok := u["error"]; ok {
		t.Error("an ordinary account got the text of the last problem")
	}

	_, out = admin.api("GET", "/api/v1/settings", nil)
	u, _ = out["update"].(map[string]any)
	if u["manifest"] != releaseAddress || u["error"] != releaseProblem {
		t.Errorf("update as an administrator = %v", u)
	}

	if status, _ := alice.api("POST", "/api/v1/update/check", nil); status != http.StatusForbidden {
		t.Errorf("an ordinary account asked for a look through the API: %d", status)
	}
	if status, out := admin.api("POST", "/api/v1/update/check", nil); status != http.StatusAccepted || out["requested"] != true {
		t.Errorf("API check = %d %v", status, out)
	}
	if got := up.checked(); got != 1 {
		t.Errorf("the updater was asked %d times, want 1", got)
	}
}

func TestTheNoticeIsOnlyForSignedInVisitorsWhileTheServiceWaitsToRestart(t *testing.T) {
	up := &fakeUpdater{status: update.Status{State: update.StateWaiting, Current: "0.1.8", Latest: "0.1.9"}}
	h := newHarnessOpts(t, nil, withUpdater(up))
	const notice = "Version 0.1.9 will be installed as soon as the running jobs have finished. New jobs wait until then."
	shown := regexp.MustCompile(`<div class="tv-notice" data-tv-notice role="status">` + regexp.QuoteMeta(notice) + `</div>`)
	hiddenDiv := regexp.MustCompile(`<div class="tv-notice" data-tv-notice role="status" hidden></div>`)

	guest := h.visitor()
	_, page := guest.get("/explorer")
	if strings.Contains(page, "will be installed") || !hiddenDiv.MatchString(page) {
		t.Error("a visitor who is not signed in was told about the update")
	}
	if res, _ := guest.get("/explorer", partialHeader); res.Header.Get("X-TV-Notice") != "" {
		t.Errorf("a visitor got the notice in the header: %q", res.Header.Get("X-TV-Notice"))
	}

	alice := h.visitor()
	alice.loginAs(userID)
	if _, page = alice.get("/explorer"); !shown.MatchString(page) {
		t.Error("a signed-in account is not told that the service is about to restart")
	}
	res, _ := alice.get("/explorer", partialHeader)
	if got, err := url.PathUnescape(res.Header.Get("X-TV-Notice")); err != nil || got != notice {
		t.Errorf("the notice in the header of a partial page = %q, %v", got, err)
	}
	res, _ = alice.get("/explorer", map[string]string{"X-TV-Partial": "1", "Accept-Language": "zh-CN"})
	if got, _ := url.PathUnescape(res.Header.Get("X-TV-Notice")); !strings.Contains(got, "0.1.9") || !strings.Contains(got, "版本") {
		t.Errorf("the notice in Chinese = %q", got)
	}

	for _, state := range []update.State{update.StateIdle, update.StateChecking, update.StateDownloading, update.StateError} {
		up.set(update.Status{State: state, Current: "0.1.8", Latest: "0.1.9"})
		if _, page = alice.get("/explorer"); strings.Contains(page, "will be installed") {
			t.Errorf("the notice is shown while the updater is %s", state)
		}
		if res, _ = alice.get("/explorer", partialHeader); res.Header.Get("X-TV-Notice") != "" {
			t.Errorf("the notice header is set while the updater is %s", state)
		}
	}
}

var (
	pictureMeta = `<meta name="tv-avatar" content="/api/static/hr/{id}.webp">`
	headerChip  = regexp.MustCompile(`<span class="tv-user"[^>]*>(<span class="tv-avatar"[^>]*>)`)
)

func TestPicturesAreAskedForOnlyForAccountsThatCanHaveOne(t *testing.T) {
	h := newHarnessOpts(t, nil, withPictures)
	alice := h.visitor()
	alice.loginAs(userID)

	_, page := alice.get("/explorer")
	if !strings.Contains(page, pictureMeta) {
		t.Error("the page does not say where the pictures are")
	}
	if m := headerChip.FindStringSubmatch(page); m == nil || m[1] != `<span class="tv-avatar" data-avatar="`+userID+`">` {
		t.Errorf("the header chip of a company account = %v", m)
	}
	if !strings.Contains(page, `<svg class="icon"`) || strings.Contains(page, "data-avatar-seen") {
		t.Error("the page must carry the default picture, and nothing the script sets")
	}

	local := h.visitor()
	local.login()
	_, page = local.get("/explorer")
	if m := headerChip.FindStringSubmatch(page); m == nil || m[1] != `<span class="tv-avatar">` {
		t.Errorf("the header chip of the local administrator must show the default picture and ask for none: %v", m)
	}
	if strings.Contains(page, `data-avatar="ADMIN"`) {
		t.Error("a picture was asked for the local administrator")
	}
}

func TestWithPicturesTurnedOffNoPageAsksForOne(t *testing.T) {
	h := newHarness(t)
	alice := h.visitor()
	alice.loginAs(userID)
	for _, path := range []string{"/explorer", "/audit", "/backups", "/settings"} {
		_, page := alice.get(path)
		// The only picture of a page is the default one in the header.
		if n := strings.Count(page, `class="tv-avatar"`); n != 1 {
			t.Errorf("%s has %d pictures although they are off, want only the default one of the header", path, n)
		}
		if strings.Contains(page, "data-avatar") || strings.Contains(page, `name="tv-avatar"`) {
			t.Errorf("%s asks for a picture although they are off", path)
		}
	}
}

func TestPicturesAppearWhereAnAccountIsNamed(t *testing.T) {
	h := newHarnessOpts(t, nil, withPictures)
	seed(h)
	local := h.visitor()
	local.login()
	src := itoa(h.env.Src.ID)
	if status, out := local.api("PATCH", "/api/v1/sources/"+src, map[string]any{"access": map[string]any{"browse": []string{"guest"}, "backup": []string{userID}, "full": []string{adminID}}}); status != http.StatusOK {
		t.Fatalf("grant = %d %v", status, out)
	}

	// The audit log: the actor of a sign-in, and the names the service gives itself, which have no picture.
	if err := audit.New(h.env.Pool, slog.New(slog.DiscardHandler)).Record(context.Background(), audit.Event{
		Actor: audit.ActorUpdater, Action: audit.ActionAppUpdate, TargetType: "app", TargetID: "0.1.9", Detail: map[string]any{"stage": "staged"},
	}); err != nil {
		t.Fatal(err)
	}
	alice := h.visitor()
	alice.loginAs(userID)
	_, page := local.get("/audit")
	if !strings.Contains(page, `data-avatar="`+userID+`"`) {
		t.Error("the actor of a sign-in has no picture in the audit log")
	}
	for _, name := range []string{"ADMIN", "UPDATER", "SCHEDULER"} {
		if strings.Contains(page, `data-avatar="`+name+`"`) {
			t.Errorf("the audit log asks for a picture of %s, which is not an account", name)
		}
	}
	if !strings.Contains(page, "Program update") || !strings.Contains(page, ">updater</td>") {
		t.Error("the audit log does not show the update event")
	}
	// Narrowed to the program updates, the sign-in of the account is gone and so is its picture.
	_, page = local.get("/audit?action=app.update")
	if !strings.Contains(page, ">updater</td>") || strings.Contains(page, `data-avatar="`+userID+`"`) {
		t.Error("the audit log cannot be narrowed to the program updates")
	}

	// The job page and the list of jobs: who asked for the backup.
	status, out := alice.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	job := num(out["job"].(map[string]any)["id"])
	alice.waitJob(job)
	for _, path := range []string{"/jobs/" + itoa(job), "/backups"} {
		if _, page = alice.get(path); !strings.Contains(page, `data-avatar="`+userID+`"`) {
			t.Errorf("%s has no picture of who asked for the backup", path)
		}
	}
	// Nobody who is not signed in learns who asked.
	res, page := h.visitor().get("/jobs/" + itoa(job))
	if res.StatusCode != http.StatusOK || strings.Contains(page, userID) || strings.Contains(page, "data-avatar") {
		t.Errorf("a visitor who is not signed in learned who asked for the backup: %d", res.StatusCode)
	}

	// The settings page: the summary of who has access, and the list of people of the editor.
	_, page = local.get("/settings?edit=" + src)
	for _, id := range []string{userID, adminID, otherID} {
		if !strings.Contains(page, `data-avatar="`+id+`"`) {
			t.Errorf("the settings page shows no picture of %s", id)
		}
	}
	if !strings.Contains(page, `<span class="tv-chip"><span class="tv-avatar" data-avatar="`+userID+`">`) {
		t.Error("the summary of who has access shows no picture on the chip of the person")
	}
}
