package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"timevault/internal/access"
	"timevault/internal/audit"
	"timevault/internal/backup"
	"timevault/internal/catalog"
)

func grantsOf(t *testing.T, svc *Service, id int64) access.List {
	t.Helper()
	src, err := svc.Source(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return src.Access
}

func TestGrantsAreStoredChangedAndAudited(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()

	// A folder that is added without grants is for administrators only, and says so in its audit record.
	plain, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Plain", RootPath: t.TempDir()})
	if err != nil || len(plain.Access) != 0 {
		t.Fatalf("a folder without grants = %+v, %v", plain.Access, err)
	}
	open := access.List{{Kind: access.Everyone, Level: access.Browse}}
	shared, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Shared", RootPath: t.TempDir(), Access: &open})
	if err != nil || !shared.Access.Equal(open) {
		t.Fatalf("a folder with grants = %+v, %v", shared.Access, err)
	}
	created, err := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionSourceCreate})
	if err != nil || len(created) != 2 {
		t.Fatalf("source.create events = %d, %v", len(created), err)
	}
	spec, _ := created[0].Detail["access"].(map[string]any)
	if got := keys(spec); strings.Join(got, ",") != "backup,browse,full" || len(spec["browse"].([]any)) != 1 || spec["browse"].([]any)[0] != "everyone" {
		t.Errorf("the grants of a new folder are in its audit record: %v", created[0].Detail)
	}

	// A request without grants leaves them alone and does not mention them.
	if _, err := svc.UpdateSource(ctx, admin, shared.ID, backup.SourceRequest{Name: "Shared again", Excludes: shared.Excludes}, true); err != nil {
		t.Fatal(err)
	}
	if got := grantsOf(t, svc, shared.ID); !got.Equal(open) {
		t.Errorf("an update that says nothing about grants changed them: %+v", got)
	}
	if events := h.updateEvents(); len(events) != 1 || events[0].Detail["access"] != nil {
		t.Errorf("the record of a rename mentions the grants: %+v", events)
	}

	// A new list replaces the old one, and the record has both.
	team := access.List{{Kind: access.User, Account: "E1001", Level: access.Full}, {Kind: access.User, Account: "E1002", Level: access.Backup}}
	if _, err := svc.UpdateSource(ctx, admin, shared.ID, backup.SourceRequest{Name: "Shared again", Excludes: shared.Excludes, Access: &team}, true); err != nil {
		t.Fatal(err)
	}
	if got := grantsOf(t, svc, shared.ID); !got.Equal(team) {
		t.Errorf("grants after the change = %+v", got)
	}
	events := h.updateEvents()
	if len(events) != 2 || strings.Join(keys(events[0].Detail), ",") != "access" {
		t.Fatalf("the record of a change of grants lists only them: %+v", events)
	}
	old, now := change(t, events[0], "access")
	if len(old.(map[string]any)["browse"].([]any)) != 1 || len(now.(map[string]any)["full"].([]any)) != 1 || now.(map[string]any)["full"].([]any)[0] != "E1001" {
		t.Errorf("access change = %v -> %v", old, now)
	}

	// The same list again writes nothing; an empty one takes everything away.
	if _, err := svc.UpdateSource(ctx, admin, shared.ID, backup.SourceRequest{Name: "Shared again", Excludes: shared.Excludes, Access: &team}, true); err != nil {
		t.Fatal(err)
	}
	if n := len(h.updateEvents()); n != 2 {
		t.Errorf("saving the same grants wrote an audit record: %d events", n)
	}
	none := access.List{}
	if _, err := svc.UpdateSource(ctx, admin, shared.ID, backup.SourceRequest{Name: "Shared again", Excludes: shared.Excludes, Access: &none}, true); err != nil {
		t.Fatal(err)
	}
	if got := grantsOf(t, svc, shared.ID); len(got) != 0 {
		t.Errorf("an empty list left grants behind: %+v", got)
	}

	// A list that is not valid is refused, whole, for a new folder and for an old one.
	bad := access.List{{Kind: access.Guest, Level: access.Backup}}
	if _, err := svc.UpdateSource(ctx, admin, shared.ID, backup.SourceRequest{Name: "Shared again", Excludes: shared.Excludes, Access: &bad}, true); !errors.Is(err, backup.ErrInvalidSource) {
		t.Errorf("guests with backup rights = %v, want ErrInvalidSource", err)
	}
	if _, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Bad", RootPath: t.TempDir(), Access: &bad}); !errors.Is(err, backup.ErrInvalidSource) {
		t.Errorf("a new folder with guests that back up = %v, want ErrInvalidSource", err)
	}
	if _, err := svc.Sources(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := svc.Sources(ctx)
	for _, s := range all {
		if s.Name == "Bad" {
			t.Errorf("the refused folder was created")
		}
	}
}

func TestCancelingAJobIsAuditedWithItsFolder(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	job, created, err := svc.StartBackup(ctx, admin, h.env.Src.ID, false)
	if err != nil || !created {
		t.Fatalf("StartBackup = %v, %v", created, err)
	}
	if ok, err := svc.CancelJob(ctx, admin, job.ID); err != nil || !ok {
		t.Fatalf("CancelJob = %v, %v", ok, err)
	}
	events, err := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionJobCancel})
	if err != nil || len(events) != 1 || events[0].SourceID == nil || *events[0].SourceID != h.env.Src.ID {
		t.Errorf("the cancellation belongs to the folder of the job: %+v, %v", events, err)
	}
	if _, err := svc.CancelJob(ctx, admin, 987654); !errors.Is(err, ErrNotFound) {
		t.Errorf("canceling a job that does not exist = %v, want ErrNotFound", err)
	}
}

func TestAnUpdateIsCheckedAgainstTheGrantsStoredWhenItIsApplied(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	team := access.List{{Kind: access.User, Account: "E1001", Level: access.Full}}
	src, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Team", RootPath: t.TempDir(), Access: &team})
	if err != nil {
		t.Fatal(err)
	}
	manager := func(current access.List) error {
		if current.LevelFor(access.Who{SignedIn: true, Account: "E1001"}) < access.Full {
			return ErrForbidden
		}
		return nil
	}
	editor := Actor{User: "E1001"}

	// While the account has Full the change goes through, and the check saw the grants that are stored.
	var seen access.List
	req := backup.SourceRequest{Name: "Team 2", Excludes: src.Excludes, KeepRoot: true, Authorize: func(cur access.List) error {
		seen = cur
		return manager(cur)
	}}
	if _, err := svc.UpdateSource(ctx, editor, src.ID, req, true); err != nil {
		t.Fatal(err)
	}
	if !seen.Equal(team) {
		t.Errorf("the check saw %+v, want the stored grants %+v", seen, team)
	}

	// The administrator takes the grant away. An update that was allowed before, and is applied now, is refused and
	// changes nothing: not the name, and not the grants it would have put back.
	none := access.List{}
	if _, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: "Team 2", Excludes: src.Excludes, Access: &none}, true); err != nil {
		t.Fatal(err)
	}
	again := access.List{{Kind: access.User, Account: "E1001", Level: access.Full}}
	late := backup.SourceRequest{Name: "Team 3", Excludes: src.Excludes, KeepRoot: true, Access: &again, Authorize: manager}
	if _, err := svc.UpdateSource(ctx, editor, src.ID, late, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the late update = %v, want ErrForbidden", err)
	}
	got, err := svc.Source(ctx, src.ID)
	if err != nil || got.Name != "Team 2" || len(got.Access) != 0 {
		t.Errorf("the refused update changed the folder: %+v, %v", got, err)
	}
	for _, e := range h.updateEvents() {
		if n, ok := e.Detail["name"].(map[string]any); ok && n["new"] == "Team 3" {
			t.Errorf("the refused update is in the audit log: %+v", e)
		}
	}
}

func TestAnUpdateThatKeepsTheFolderNeverWritesIt(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	src, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Docs", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	got, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: "Docs 2", RootPath: other, KeepRoot: true, Excludes: src.Excludes}, true)
	if err != nil || got.RootPath != src.RootPath || got.Name != "Docs 2" {
		t.Fatalf("an update that keeps the folder = %+v, %v; the folder must stay %q", got, err, src.RootPath)
	}
	events := h.updateEvents()
	if len(events) != 1 || events[0].Detail["root"] != nil || events[0].Detail["name"] == nil {
		t.Errorf("the record lists the rename and not a change of the folder: %+v", events)
	}
	// A new source has to name its folder.
	if _, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "No folder", KeepRoot: true}); !errors.Is(err, backup.ErrInvalidSource) {
		t.Errorf("a new source that keeps its folder = %v, want ErrInvalidSource", err)
	}
}

func TestAPathMapForAVisitorKnowsOnlyTheirFolders(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	shown := `\\server.example.test\share\Secret`
	secret, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Secret", RootPath: t.TempDir(), DisplayPath: shown})
	if err != nil {
		t.Fatal(err)
	}
	visible := h.env.Src.ID
	m, err := svc.PathMapFor(ctx, func(s catalog.Source) bool { return s.ID == visible })
	if err != nil {
		t.Fatal(err)
	}
	if m.SourceLabel(secret.ID) != "" || m.SourceDisplay(secret.ID, "a") != "" {
		t.Errorf("the map of a visitor names a folder they may not see: %q", m.SourceLabel(secret.ID))
	}
	if got := m.Mask("failed at " + secret.RootPath + `\x`); !strings.Contains(got, secret.RootPath) || strings.Contains(got, "Secret") {
		t.Errorf("a text that quotes the real folder of a hidden folder = %q; it is left as it is, with nothing of the folder's name or display folder added", got)
	}
	if got := m.Mask("failed at " + h.env.Src.RootPath + `\x`); strings.Contains(got, h.env.Src.RootPath) {
		t.Errorf("the real folder of a visible one is still masked: %q", got)
	}
	all, err := svc.PathMap(ctx)
	if err != nil || all.SourceLabel(secret.ID) == "" {
		t.Errorf("the map of everybody knows every folder: %v", err)
	}
}
