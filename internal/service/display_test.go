package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"timevault/internal/audit"
	"timevault/internal/backup"
)

const displayTestHost = "sgmeslt595.co.murata.local"

// displayService is a service of the harness that knows the name of its server.
func displayService(h *harness, host string) *Service {
	return h.service(func(c *Config) { c.ServerHost = host })
}

// displayErr asserts that err is the typed validation error the UI translates.
func displayErr(t *testing.T, err error, key string, args []any) {
	t.Helper()
	var se *backup.SourceError
	if !errors.As(err, &se) || !errors.Is(err, backup.ErrInvalidSource) {
		t.Fatalf("error = %v, want a *backup.SourceError that wraps ErrInvalidSource", err)
	}
	if se.Key != key || !reflect.DeepEqual(se.Args, args) {
		t.Fatalf("error key %q, args %v; want %q, %v (message %q)", se.Key, se.Args, key, args, se.Msg)
	}
	if se.Msg == "" || strings.Contains(se.Msg, "errors.") {
		t.Fatalf("message %q must be a readable English sentence", se.Msg)
	}
}

func TestAddSourceDisplayPathPolicy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	strict := displayService(h, displayTestHost)
	open := h.service(nil)

	cases := []struct {
		name    string
		svc     *Service
		display string
		key     string // the expected error, "" when the source is accepted
		args    []any
		stored  string
	}{
		{"required when the server is known", strict, "", "errors.display_required", nil, ""},
		{"blank counts as missing", strict, " \t ", "errors.display_required", nil, ""},
		{"the server", strict, `\\sgmeslt595.co.murata.local\TimeVault\monitor`, "", nil, `\\sgmeslt595.co.murata.local\TimeVault\monitor`},
		{"another spelling of it", strict, ` //SGMESLT595.CO.MURATA.LOCAL/timevault/Monitor/ `, "", nil, `\\sgmeslt595.co.murata.local\timevault\Monitor`},
		{"another server", strict, `\\other\TimeVault\monitor`, "errors.display_host", []any{"other", displayTestHost}, ""},
		{"the short name of this server", strict, `\\sgmeslt595\TimeVault\monitor`, "errors.display_host", []any{"sgmeslt595", displayTestHost}, ""},
		{"a name that only starts like this server", strict, `\\sgmeslt595.co.murata.local.example.com\x\y`, "errors.display_host", []any{"sgmeslt595.co.murata.local.example.com", displayTestHost}, ""},
		{"an address", strict, `\\10.1.2.3\TimeVault`, "errors.display_host", []any{"10.1.2.3", displayTestHost}, ""},
		{"a drive path", strict, `D:\TimeVault\monitor`, "errors.display_invalid", []any{`D:\TimeVault\monitor`}, ""},
		{"no share", strict, `\\sgmeslt595.co.murata.local`, "errors.display_invalid", []any{`\\sgmeslt595.co.murata.local`}, ""},
		{"a hidden share", strict, `\\sgmeslt595.co.murata.local\C$\Users`, "errors.display_invalid", []any{`\\sgmeslt595.co.murata.local\C$\Users`}, ""},
		{"a parent folder", strict, `\\sgmeslt595.co.murata.local\TimeVault\..\x`, "errors.display_invalid", []any{`\\sgmeslt595.co.murata.local\TimeVault\..\x`}, ""},
		{"a forbidden character", strict, `\\sgmeslt595.co.murata.local\TimeVault\a*b`, "errors.display_invalid", []any{`\\sgmeslt595.co.murata.local\TimeVault\a*b`}, ""},
		{"syntax comes before the host", strict, `\\other\C$`, "errors.display_invalid", []any{`\\other\C$`}, ""},

		{"optional without a server host", open, "", "", nil, ""},
		{"any host without a server host", open, `//Other-Server/Share/Folder/`, "", nil, `\\Other-Server\Share\Folder`},
		{"syntax is checked without a server host", open, `D:\TimeVault\monitor`, "errors.display_invalid", []any{`D:\TimeVault\monitor`}, ""},
		{"a hidden share without a server host", open, `\\srv\ADMIN$`, "errors.display_invalid", []any{`\\srv\ADMIN$`}, ""},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("Source %d", i)
			src, err := c.svc.AddSource(ctx, admin, backup.SourceRequest{Name: name, RootPath: t.TempDir(), DisplayPath: c.display})
			if c.key != "" {
				displayErr(t, err, c.key, c.args)
				if n := h.count(`SELECT count(*) FROM sources WHERE name = $1`, name); n != 0 {
					t.Error("a refused source was registered")
				}
				return
			}
			if err != nil || src.DisplayPath != c.stored {
				t.Fatalf("AddSource(%q) = %q, %v; want %q", c.display, src.DisplayPath, err, c.stored)
			}
			if stored, err := open.Source(ctx, src.ID); err != nil || stored.DisplayPath != c.stored {
				t.Errorf("the stored display path is %q, %v; want %q", stored.DisplayPath, err, c.stored)
			}
		})
	}
}

func TestUpdateSourceDisplayPathPolicy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	strict := displayService(h, displayTestHost)
	open := h.service(nil)
	src := h.env.Src // registered without a display path
	req := func(display string) backup.SourceRequest {
		return backup.SourceRequest{Name: src.Name, Excludes: src.Excludes, DisplayPath: display}
	}

	// The edit form always sends the current value, so a source without one cannot be saved
	// until it gets one, and the display path of a source that has one cannot be emptied.
	_, err := strict.UpdateSource(ctx, admin, src.ID, req(""), true)
	displayErr(t, err, "errors.display_required", nil)
	_, err = strict.UpdateSource(ctx, admin, src.ID, req(`\\other\TimeVault\monitor`), true)
	displayErr(t, err, "errors.display_host", []any{"other", displayTestHost})
	_, err = strict.UpdateSource(ctx, admin, src.ID, req(`\\sgmeslt595\TimeVault\monitor`), true)
	displayErr(t, err, "errors.display_host", []any{"sgmeslt595", displayTestHost})
	_, err = strict.UpdateSource(ctx, admin, src.ID, req(`D:\TimeVault`), true)
	displayErr(t, err, "errors.display_invalid", []any{`D:\TimeVault`})
	if now, _ := open.Source(ctx, src.ID); now.DisplayPath != "" || len(h.updateEvents()) != 0 {
		t.Fatalf("a refused update changed the source (%+v) or was audited", now)
	}
	// The source is looked up first, so an unknown one is not found whatever the form says.
	if _, err := strict.UpdateSource(ctx, admin, 424242, req(""), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown source = %v, want ErrNotFound", err)
	}

	// The host is spelled like the configured one, whatever was typed.
	got, err := strict.UpdateSource(ctx, admin, src.ID, req(`//SGMESLT595.co.murata.local/timevault/Monitor/`), true)
	const canonical = `\\sgmeslt595.co.murata.local\timevault\Monitor`
	if err != nil || got.DisplayPath != canonical {
		t.Fatalf("UpdateSource = %q, %v; want %q", got.DisplayPath, err, canonical)
	}
	if stored, _ := open.Source(ctx, src.ID); stored.DisplayPath != canonical {
		t.Fatalf("stored display path = %q", stored.DisplayPath)
	}
	events := len(h.updateEvents())
	if events != 1 {
		t.Fatalf("%d audit events, want 1", events)
	}

	// The same value in another spelling, and a save that keeps the current value, change nothing.
	for _, same := range []string{`\\SGMESLT595.CO.MURATA.LOCAL\timevault\Monitor\`, canonical} {
		if got, err := strict.UpdateSource(ctx, admin, src.ID, req(same), true); err != nil || got.DisplayPath != canonical {
			t.Fatalf("UpdateSource(%q) = %q, %v", same, got.DisplayPath, err)
		}
	}
	if n := len(h.updateEvents()); n != events {
		t.Fatalf("saving the same display path wrote audit events: %d, want %d", n, events)
	}

	// Without a server host the display path is optional: replaced by any network path, or removed.
	if got, err := open.UpdateSource(ctx, admin, src.ID, req(`\\any-server\Share`), true); err != nil || got.DisplayPath != `\\any-server\Share` {
		t.Fatalf("UpdateSource without a server host = %q, %v", got.DisplayPath, err)
	}
	if got, err := open.UpdateSource(ctx, admin, src.ID, req(""), true); err != nil || got.DisplayPath != "" {
		t.Fatalf("an empty display path removes it: %q, %v", got.DisplayPath, err)
	}
	_, err = open.UpdateSource(ctx, admin, src.ID, req(`not a path`), true)
	displayErr(t, err, "errors.display_invalid", []any{`not a path`})
}

func TestDisplayPathIsKeptAcrossOtherUpdates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	strict := displayService(h, displayTestHost)
	src := h.env.Src
	const display = `\\sgmeslt595.co.murata.local\TimeVault\monitor`
	if _, err := strict.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: src.Name, Excludes: src.Excludes, DisplayPath: display}, true); err != nil {
		t.Fatal(err)
	}

	// An edit form that sends the current display path along keeps it; the audit record lists only what changed.
	hourly := 60
	got, err := strict.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: "Renamed", Excludes: src.Excludes, ScheduleMinutes: &hourly, DisplayPath: display}, false)
	if err != nil || got.DisplayPath != display || got.Name != "Renamed" || got.Enabled {
		t.Fatalf("UpdateSource = %+v, %v", got, err)
	}
	events := h.updateEvents()
	if len(events) != 2 || strings.Join(keys(events[0].Detail), ",") != "enabled,name,schedule_minutes" {
		t.Fatalf("the audit record of the second update lists %v", keys(events[0].Detail))
	}
	if stored, _ := strict.Source(ctx, src.ID); stored.DisplayPath != display {
		t.Fatalf("stored display path = %q", stored.DisplayPath)
	}
}

func TestDisplayPathIsAudited(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	svc := displayService(h, displayTestHost)
	const first, second = `\\sgmeslt595.co.murata.local\TimeVault\a`, `\\sgmeslt595.co.murata.local\TimeVault\b`

	added, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Audited", RootPath: t.TempDir(), DisplayPath: "//SGMESLT595.co.murata.local/TimeVault/a"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionSourceCreate})
	if err != nil || len(created) != 1 || created[0].Detail["display"] != first {
		t.Fatalf("source.create events = %+v, %v; the display path belongs in the detail, as stored", created, err)
	}

	// A source created without one has no "display" in its record.
	plain, err := h.service(nil).AddSource(ctx, admin, backup.SourceRequest{Name: "Plain", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	created, _ = svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionSourceCreate})
	if len(created) != 2 {
		t.Fatalf("%d source.create events, want 2", len(created))
	}
	if _, has := created[0].Detail["display"]; has || created[0].Detail["name"] != plain.Name {
		t.Fatalf("a source without a display path has %v in its record", created[0].Detail)
	}

	update := func(name, display string, enabled bool) {
		t.Helper()
		if _, err := svc.UpdateSource(ctx, admin, added.ID, backup.SourceRequest{Name: name, Excludes: added.Excludes, DisplayPath: display}, enabled); err != nil {
			t.Fatal(err)
		}
	}
	update("Audited", second, true)
	events := h.updateEvents()
	if len(events) != 1 || strings.Join(keys(events[0].Detail), ",") != "display" {
		t.Fatalf("audit events = %+v; only the display path changed", events)
	}
	if o, n := change(t, events[0], "display"); o != first || n != second {
		t.Fatalf("display change = %v -> %v", o, n)
	}
	update("Audited and renamed", second, true)
	if events = h.updateEvents(); len(events) != 2 || strings.Join(keys(events[0].Detail), ",") != "name" {
		t.Fatalf("a rename that keeps the display path lists %v", keys(events[0].Detail))
	}

	// Removing it, which only a service without a server host allows, is a change to "".
	if _, err := h.service(nil).UpdateSource(ctx, admin, added.ID, backup.SourceRequest{Name: "Audited and renamed", Excludes: added.Excludes}, true); err != nil {
		t.Fatal(err)
	}
	events = h.updateEvents()
	if o, n := change(t, events[0], "display"); len(events) != 3 || o != second || n != "" {
		t.Fatalf("removing the display path = %v -> %v (%d events)", o, n, len(events))
	}
}

// The display path is only shown, so it can be corrected after the first backup, while the folder stays fixed.
func TestDisplayPathCanChangeAfterBackups(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	strict := displayService(h, displayTestHost)
	src := h.env.Src
	h.env.Write("a.txt", "alpha")
	h.env.Backup()
	if err := h.env.Cat.MarkRun(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	ran, err := strict.Source(ctx, src.ID)
	if err != nil || ran.LastRunAt == nil {
		t.Fatalf("source after the backup = %+v, %v", ran, err)
	}

	req := backup.SourceRequest{Name: src.Name, Excludes: src.Excludes, DisplayPath: `\\sgmeslt595.co.murata.local\TimeVault\team`}
	for _, display := range []string{req.DisplayPath, `\\sgmeslt595.co.murata.local\TimeVault\team\corrected`} {
		req.DisplayPath = display
		got, err := strict.UpdateSource(ctx, admin, src.ID, req, true)
		if err != nil || got.DisplayPath != display || got.RootPath != src.RootPath {
			t.Fatalf("UpdateSource with display path %q = %+v, %v", display, got, err)
		}
		if got.LastRunAt == nil || !got.LastRunAt.Equal(*ran.LastRunAt) {
			t.Fatalf("changing the display path moved the last run: %v", got.LastRunAt)
		}
	}
	if o, n := change(t, h.updateEvents()[0], "display"); o != `\\sgmeslt595.co.murata.local\TimeVault\team` || n != req.DisplayPath {
		t.Fatalf("display change = %v -> %v", o, n)
	}

	// The folder of a source with backups is still fixed.
	req.RootPath = t.TempDir()
	if _, err := strict.UpdateSource(ctx, admin, src.ID, req, true); !errors.Is(err, backup.ErrInvalidSource) || !strings.Contains(err.Error(), "already has backups") {
		t.Fatalf("moving a source that has backups = %v", err)
	}
	if now, _ := strict.Source(ctx, src.ID); now.RootPath != src.RootPath || now.DisplayPath != `\\sgmeslt595.co.murata.local\TimeVault\team\corrected` {
		t.Fatalf("a refused move changed the source: %+v", now)
	}
	if _, err := strict.LatestPoint(ctx, src.ID); err != nil {
		t.Fatalf("the recovery point is gone: %v", err)
	}
}

func TestServiceKnowsItsServerAndBuildsAPathMap(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	svc := h.service(func(c *Config) {
		c.ServerHost = displayTestHost
		c.RestoreDisplayRoot = restoreDisplay
	})
	if svc.ServerHost() != displayTestHost || svc.RestoreDisplayRoot() != restoreDisplay {
		t.Fatalf("ServerHost = %q, RestoreDisplayRoot = %q", svc.ServerHost(), svc.RestoreDisplayRoot())
	}
	if bare := h.service(nil); bare.ServerHost() != "" || bare.RestoreDisplayRoot() != "" {
		t.Fatalf("an unconfigured service has %q and %q", bare.ServerHost(), bare.RestoreDisplayRoot())
	}

	const shown = `\\sgmeslt595.co.murata.local\TimeVault\shown`
	added, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Shown", RootPath: t.TempDir(), DisplayPath: shown})
	if err != nil {
		t.Fatal(err)
	}
	plain := h.env.Src // registered without a display path
	m, err := svc.PathMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.SourceDisplay(added.ID, "a/b.txt"); got != shown+`\a\b.txt` {
		t.Errorf("SourceDisplay = %q", got)
	}
	if got := m.SourceDisplay(plain.ID, "a"); got != "" {
		t.Errorf("SourceDisplay of a source without a display path = %q", got)
	}
	if got, want := m.SourceLabel(plain.ID), plain.Name; got != want {
		t.Errorf("SourceLabel = %q, want %q", got, want)
	}
	if got := m.RestoreDisplay("restore-1"); got != restoreDisplay+`\restore-1` {
		t.Errorf("RestoreDisplay = %q", got)
	}
	for in, want := range map[string]string{
		filepath.Join(added.RootPath, "a", "b.txt"):                shown + `\a\b.txt`,
		strings.ToUpper(added.RootPath):                            shown,
		plain.RootPath + string(filepath.Separator) + "x":          `[` + plain.Name + `]\x`,
		filepath.Join(h.env.Repo.Root(), "objects", "ab"):          `[backup folder]\objects\ab`,
		filepath.Join(h.env.RestoreRoot, "restore-1", "a.txt"):     restoreDisplay + `\restore-1\a.txt`,
		"cannot read " + added.RootPath + ": access is denied":     "cannot read " + shown + ": access is denied",
		"nothing to hide here, only " + t.TempDir() + `\elsewhere`: "",
	} {
		if want == "" {
			want = in // not a known folder
		}
		if got := m.Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}

	// The map is a snapshot: a display path set afterwards shows in the next one only.
	const later = `\\sgmeslt595.co.murata.local\TimeVault\later`
	if _, err := svc.UpdateSource(ctx, admin, added.ID, backup.SourceRequest{Name: added.Name, Excludes: added.Excludes, DisplayPath: later}, true); err != nil {
		t.Fatal(err)
	}
	if got := m.SourceDisplay(added.ID, ""); got != shown {
		t.Errorf("the snapshot changed: %q", got)
	}
	if next, err := svc.PathMap(ctx); err != nil || next.SourceDisplay(added.ID, "") != later {
		t.Errorf("the next map = %v, %v", next, err)
	}

	// Without a display root the restore folder only gets a label.
	bare, err := h.service(nil).PathMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := bare.Mask(filepath.Join(h.env.RestoreRoot, "restore-1")); got != `[restore folder]\restore-1` {
		t.Errorf("Mask of the restore folder without a display root = %q", got)
	}
	if got := bare.RestoreDisplay("restore-1"); got != "" {
		t.Errorf("RestoreDisplay without a display root = %q", got)
	}
}
