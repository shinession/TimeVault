package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGivingUpFullControlNeedsAConfirmation(t *testing.T) {
	w := newFolderWorld(t)
	staff := itoa(w.staff)

	// Carol hands Staff to Alice and forgets to keep herself in the Full list.
	away := url.Values{"name": {"Staff"}, "enabled": {"1"}, "access_set": {"1"}, "access_browse": {"everyone"}, "access_full": {"user:" + userID}}
	res, body := w.carol.postForm("/settings/sources/"+staff, away)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "takes away your own full control") ||
		!strings.Contains(body, `name="confirm_lockout"`) || !strings.Contains(body, `value="user:`+userID+`" data-access-person checked`) {
		t.Fatalf("giving up full control without confirmation = %d\n%s", res.StatusCode, body)
	}
	if got := strings.Join(sourcesOf(t, w.carol), " "); !strings.Contains(got, "Staff=full") {
		t.Fatalf("the refused form changed what Carol may do: %q", got)
	}

	// With the box ticked it goes through.
	away.Set("confirm_lockout", "1")
	if res, body := w.carol.postForm("/settings/sources/"+staff, away); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("confirmed hand-over = %d\n%s", res.StatusCode, body)
	}
	if got := strings.Join(sourcesOf(t, w.carol), " "); !strings.Contains(got, "Staff=browse") {
		t.Errorf("Carol sees %q after giving up full control", got)
	}
	if got := strings.Join(sourcesOf(t, w.alice), " "); !strings.Contains(got, "Staff=full") {
		t.Errorf("Alice sees %q after being given full control", got)
	}

	// Whoever still has full control after the change is not asked, and neither is an administrator, who always has it.
	if status, out := w.alice.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"browse": []string{"everyone"}, "full": []string{userID, otherID}}}); status != http.StatusOK {
		t.Errorf("a change that keeps Alice's full control = %d %v", status, out)
	}
	if status, out := w.admin.api("PATCH", "/api/v1/sources/"+staff, map[string]any{"access": map[string]any{"browse": []string{"everyone"}}}); status != http.StatusOK {
		t.Errorf("an administrator empties the Full list = %d %v", status, out)
	}
}
