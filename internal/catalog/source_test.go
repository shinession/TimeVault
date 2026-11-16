package catalog_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/catalog"
	"timevault/internal/db/dbtest"
)

func newStore(t *testing.T) (*catalog.Store, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.New(t)
	return catalog.NewStore(pool), pool
}

// folder returns a path for a source. The catalog never touches the disk, it only stores the text.
func folder(name string) string { return filepath.Join(string(filepath.Separator), "data", name) }

func mustCreate(t *testing.T, cat *catalog.Store, name string, minutes *int) catalog.Source {
	t.Helper()
	src, err := cat.CreateSource(context.Background(), catalog.NewSource{Name: name, RootPath: folder(name), CreatedBy: "tester", ScheduleMinutes: minutes})
	if err != nil {
		t.Fatalf("create source %q: %v", name, err)
	}
	return src
}

func sameInt(a, b *int) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func TestSourceScheduleRoundTrip(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	hourly := 60
	timed := mustCreate(t, cat, "timed", &hourly)
	manual := mustCreate(t, cat, "manual", nil)

	if timed.ScheduleMinutes == nil || *timed.ScheduleMinutes != 60 || timed.LastRunAt != nil {
		t.Fatalf("created timed source = %+v", timed)
	}
	if manual.ScheduleMinutes != nil || manual.LastRunAt != nil {
		t.Fatalf("a source without a schedule is manual only: %+v", manual)
	}
	got, err := cat.GetSource(ctx, timed.ID)
	if err != nil || !sameInt(got.ScheduleMinutes, timed.ScheduleMinutes) {
		t.Fatalf("GetSource = %+v, %v", got, err)
	}
	list, err := cat.ListSources(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "manual" || list[0].ScheduleMinutes != nil ||
		list[1].Name != "timed" || !sameInt(list[1].ScheduleMinutes, &hourly) {
		t.Fatalf("ListSources = %+v, %v", list, err)
	}

	// The table refuses what the backup layer would have rejected anyway.
	for _, bad := range []int{0, -1, 43201} {
		if _, err := cat.CreateSource(ctx, catalog.NewSource{Name: "bad", RootPath: folder("bad"), CreatedBy: "t", ScheduleMinutes: &bad}); err == nil || errors.Is(err, catalog.ErrConflict) {
			t.Fatalf("a schedule of %d minutes: got %v, want a constraint error", bad, err)
		}
	}
}

func TestUpdateSourceReplacesTheEditableFields(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	every30 := 30
	a, err := cat.CreateSource(ctx, catalog.NewSource{Name: "alpha", RootPath: folder("a"), Excludes: []string{"*.tmp"}, CreatedBy: "alice", ScheduleMinutes: &every30})
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, cat, "beta", nil)
	if err := cat.MarkRun(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a, err = cat.GetSource(ctx, a.ID); err != nil || a.LastRunAt == nil {
		t.Fatalf("source after MarkRun = %+v, %v", a, err)
	}

	// Spelled with a trailing separator: the folder's identity is the cleaned path, not the text.
	moved := folder("a2") + string(filepath.Separator)
	up, err := cat.UpdateSource(ctx, a.ID, catalog.SourceUpdate{Name: "alpha 2", RootPath: moved, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != "alpha 2" || up.RootPath != moved || len(up.Excludes) != 0 || up.Enabled || up.ScheduleMinutes != nil {
		t.Fatalf("updated source = %+v; every editable field must be replaced", up)
	}
	if up.CreatedBy != "alice" || !up.CreatedAt.Equal(a.CreatedAt) {
		t.Fatalf("an update must leave the bookkeeping alone: %+v", up)
	}
	if up.LastRunAt != nil {
		t.Fatalf("the last run of the old folder says nothing about the new one: %v", up.LastRunAt)
	}

	// The folder's identity follows the update: the old one is free again, the new one is taken.
	if _, err := cat.CreateSource(ctx, catalog.NewSource{Name: "reuses the old folder", RootPath: folder("a"), CreatedBy: "t"}); err != nil {
		t.Fatalf("the old folder should be free after the move: %v", err)
	}
	if _, err := cat.CreateSource(ctx, catalog.NewSource{Name: "wants the new folder", RootPath: folder("a2"), CreatedBy: "t"}); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("the new folder must be taken, got %v", err)
	}

	if _, err := cat.UpdateSource(ctx, a.ID, catalog.SourceUpdate{Name: "beta", RootPath: folder("a2"), Enabled: true}); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("a name used by another source = %v, want ErrConflict", err)
	}
	if _, err := cat.UpdateSource(ctx, a.ID, catalog.SourceUpdate{Name: "alpha 2", RootPath: folder("beta"), Enabled: true}); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("a folder used by another source = %v, want ErrConflict", err)
	}
	if _, err := cat.UpdateSource(ctx, 999999, catalog.SourceUpdate{Name: "ghost", RootPath: folder("ghost")}); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("an unknown source = %v, want ErrNotFound", err)
	}
	if still, _ := cat.GetSource(ctx, a.ID); still.Name != "alpha 2" || still.RootPath != moved || still.Enabled {
		t.Fatalf("a rejected update changed the source: %+v", still)
	}

	hourly := 60
	back, err := cat.UpdateSource(ctx, a.ID, catalog.SourceUpdate{Name: "alpha 2", RootPath: folder("a2"), Excludes: []string{"x"}, Enabled: true, ScheduleMinutes: &hourly})
	if err != nil || !back.Enabled || !sameInt(back.ScheduleMinutes, &hourly) || len(back.Excludes) != 1 {
		t.Fatalf("second update = %+v, %v", back, err)
	}
}

func TestUpdateSourceKeepsTheLastRunUnlessTheFolderMoves(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	every30 := 30
	a := mustCreate(t, cat, "alpha", &every30)
	if err := cat.MarkRun(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	ran, err := cat.GetSource(ctx, a.ID)
	if err != nil || ran.LastRunAt == nil {
		t.Fatalf("source after MarkRun = %+v, %v", ran, err)
	}

	// The same folder, spelled in two ways, is not a move.
	for _, root := range []string{a.RootPath, a.RootPath + string(filepath.Separator)} {
		same, err := cat.UpdateSource(ctx, a.ID, catalog.SourceUpdate{Name: "renamed", RootPath: root, Enabled: true, ScheduleMinutes: &every30})
		if err != nil || same.LastRunAt == nil || !same.LastRunAt.Equal(*ran.LastRunAt) {
			t.Fatalf("an update that keeps the folder %q must keep the last run: %+v, %v", root, same, err)
		}
	}
	moved, err := cat.UpdateSource(ctx, a.ID, catalog.SourceUpdate{Name: "renamed", RootPath: folder("elsewhere"), Enabled: true, ScheduleMinutes: &every30})
	if err != nil || moved.LastRunAt != nil {
		t.Fatalf("a move must forget the last run, so that the new folder is due at once: %+v, %v", moved, err)
	}
	due, err := cat.DueSources(ctx)
	if err != nil || len(due) != 1 || due[0] != a.ID {
		t.Fatalf("DueSources after the move = %v, %v", due, err)
	}
}

const displayPath = `\\srv.example.com\TimeVault\monitor`

func TestSourceDisplayPathRoundTrip(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)

	shown, err := cat.CreateSource(ctx, catalog.NewSource{Name: "shown", RootPath: folder("shown"), CreatedBy: "t", DisplayPath: displayPath})
	if err != nil || shown.DisplayPath != displayPath {
		t.Fatalf("created source = %+v, %v", shown, err)
	}
	plain := mustCreate(t, cat, "plain", nil)
	if plain.DisplayPath != "" {
		t.Fatalf("a source without a display path has %q", plain.DisplayPath)
	}
	nulls := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM sources WHERE display_path IS NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := nulls(); n != 1 {
		t.Fatalf("%d sources store NULL, want 1: no display path is NULL, never an empty string", n)
	}

	if got, err := cat.GetSource(ctx, shown.ID); err != nil || got.DisplayPath != displayPath {
		t.Fatalf("GetSource = %+v, %v", got, err)
	}
	list, err := cat.ListSources(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "plain" || list[0].DisplayPath != "" || list[1].DisplayPath != displayPath {
		t.Fatalf("ListSources = %+v, %v", list, err)
	}
	bare, err := cat.Sources(ctx)
	if err != nil || len(bare) != 2 || bare[0].Name != "plain" || bare[1].Name != "shown" || bare[1].DisplayPath != displayPath || bare[1].RootPath != shown.RootPath {
		t.Fatalf("Sources = %+v, %v", bare, err)
	}

	// An update replaces it, an empty one removes it, and it can be set again.
	same := catalog.SourceUpdate{Name: "shown", RootPath: shown.RootPath, Enabled: true}
	for _, want := range []string{displayPath + `\sub`, "", displayPath} {
		up := same
		up.DisplayPath = want
		got, err := cat.UpdateSource(ctx, shown.ID, up)
		if err != nil || got.DisplayPath != want {
			t.Fatalf("UpdateSource with display path %q = %+v, %v", want, got, err)
		}
		if n := nulls(); (want == "") != (n == 2) {
			t.Fatalf("with display path %q %d sources store NULL", want, n)
		}
	}
	// Leaving it out of an update is the same as removing it: the update replaces every editable field.
	if got, err := cat.UpdateSource(ctx, shown.ID, same); err != nil || got.DisplayPath != "" {
		t.Fatalf("UpdateSource without a display path = %+v, %v", got, err)
	}

	// The table refuses what the backup layer would have rejected anyway.
	long := `\\srv\share\` + strings.Repeat("a", 510)
	if _, err := cat.CreateSource(ctx, catalog.NewSource{Name: "long", RootPath: folder("long"), CreatedBy: "t", DisplayPath: long}); err == nil || errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("a display path of %d characters: got %v, want a constraint error", len([]rune(long)), err)
	}
	if _, err := cat.UpdateSource(ctx, plain.ID, catalog.SourceUpdate{Name: "plain", RootPath: plain.RootPath, Enabled: true, DisplayPath: long}); err == nil || errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("updating to a display path of %d characters: got %v, want a constraint error", len([]rune(long)), err)
	}
	if got, _ := cat.GetSource(ctx, plain.ID); got.DisplayPath != "" {
		t.Fatalf("a refused update changed the display path to %q", got.DisplayPath)
	}
}

// The display path is only shown, nothing in the history refers to it, so it can change after the first backup
// and it does not disturb the schedule.
func TestDisplayPathCanChangeAfterRecoveryPointsExist(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	every30 := 30
	src := mustCreate(t, cat, "alpha", &every30)

	lock, err := cat.LockSource(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	point, _, err := cat.BeginPoint(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.CommitPoint(ctx, lock, point, catalog.Stats{}, []byte("manifest")); err != nil {
		t.Fatal(err)
	}
	lock.Unlock()
	if err := cat.MarkRun(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	ran, err := cat.GetSource(ctx, src.ID)
	if err != nil || ran.LastRunAt == nil {
		t.Fatalf("source after MarkRun = %+v, %v", ran, err)
	}

	for _, display := range []string{displayPath, displayPath + `\renamed`, ""} {
		got, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "alpha", RootPath: src.RootPath, Enabled: true, ScheduleMinutes: &every30, DisplayPath: display})
		if err != nil || got.DisplayPath != display {
			t.Fatalf("UpdateSource with display path %q = %+v, %v", display, got, err)
		}
		if got.RootPath != src.RootPath || got.LastRunAt == nil || !got.LastRunAt.Equal(*ran.LastRunAt) {
			t.Fatalf("changing the display path must leave the folder and the last run alone: %+v", got)
		}
	}
	if has, err := cat.HasPoints(ctx, src.ID); err != nil || !has {
		t.Fatalf("the recovery point is gone: %v, %v", has, err)
	}
	if due, err := cat.DueSources(ctx); err != nil || len(due) != 0 {
		t.Fatalf("DueSources = %v, %v; changing the display path must not make the source due", due, err)
	}
}

func TestHasPointsCountsEveryState(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	src := mustCreate(t, cat, "a", nil)
	other := mustCreate(t, cat, "b", nil)

	if has, err := cat.HasPoints(ctx, src.ID); err != nil || has {
		t.Fatalf("a new source has points: %v, %v", has, err)
	}
	lock, err := cat.LockSource(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	p, _, err := cat.BeginPoint(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	if has, _ := cat.HasPoints(ctx, src.ID); !has {
		t.Fatal("a building point counts")
	}
	if err := cat.FailPoint(ctx, lock, p, catalog.StateFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	if has, _ := cat.HasPoints(ctx, src.ID); !has {
		t.Fatal("a failed point counts")
	}
	if has, _ := cat.HasPoints(ctx, other.ID); has {
		t.Fatal("points of one source must not count for another")
	}
}

func TestDueSourcesFollowTheSchedule(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)
	hourly, every10 := 60, 10
	ranMinutesAgo := func(id int64, minutes int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE sources SET last_run_at = now() - make_interval(mins => $2) WHERE id = $1`, id, minutes); err != nil {
			t.Fatal(err)
		}
	}

	never := mustCreate(t, cat, "never ran", &hourly)
	never2 := mustCreate(t, cat, "never ran too", &hourly)
	waiting := mustCreate(t, cat, "waiting", &hourly)
	overdue := mustCreate(t, cat, "overdue", &hourly)
	longest := mustCreate(t, cat, "waiting longest", &every10)
	off := mustCreate(t, cat, "disabled", &hourly)
	mustCreate(t, cat, "manual", nil)
	ranMinutesAgo(waiting.ID, 30)
	ranMinutesAgo(overdue.ID, 90)
	ranMinutesAgo(longest.ID, 180)
	if _, err := cat.UpdateSource(ctx, off.ID, catalog.SourceUpdate{Name: off.Name, RootPath: off.RootPath, Enabled: false, ScheduleMinutes: &hourly}); err != nil {
		t.Fatal(err)
	}

	due, err := cat.DueSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Never-run sources first (by id), then whoever has waited longest; not-yet-due, disabled and manual sources are absent.
	want := []int64{never.ID, never2.ID, longest.ID, overdue.ID}
	if len(due) != len(want) {
		t.Fatalf("due = %v, want %v", due, want)
	}
	for i := range want {
		if due[i] != want[i] {
			t.Fatalf("due = %v, want %v", due, want)
		}
	}

	// A run restarts the interval whatever its outcome.
	if err := cat.MarkRun(ctx, overdue.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := cat.GetSource(ctx, overdue.ID)
	if got.LastRunAt == nil || time.Since(*got.LastRunAt) > time.Minute || time.Until(*got.LastRunAt) > time.Minute {
		t.Fatalf("MarkRun left last_run_at = %v", got.LastRunAt)
	}
	due, _ = cat.DueSources(ctx)
	for _, id := range due {
		if id == overdue.ID {
			t.Fatalf("a source that just ran is still due: %v", due)
		}
	}
}

// A build that finds nothing to record is removed completely: its row, what it wrote, and
// the closures it applied to the history of earlier points.
func TestDiscardPointLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)
	src := mustCreate(t, cat, "a", nil)
	lock, err := cat.LockSource(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	shaOne, shaTwo := make([]byte, 32), make([]byte, 32)
	shaOne[0], shaTwo[0] = 1, 2
	objects := []catalog.ObjectRef{{SHA256: shaOne, Size: 3}, {SHA256: shaTwo, Size: 4}}

	first, prev, err := cat.BeginPoint(ctx, lock)
	if err != nil || first.Seq != 1 || prev != 0 {
		t.Fatalf("first point = %+v, prev %d, %v", first, prev, err)
	}
	root, err := cat.RootNode(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ApplyDir(ctx, first, prev, root, []catalog.Change{
		{Kind: catalog.ChangeAdd, Name: "a.txt", State: catalog.VersionOK, Size: 3, SHA256: shaOne},
	}, objects[:1]); err != nil {
		t.Fatal(err)
	}
	if err := cat.CommitPoint(ctx, lock, first, catalog.Stats{FileCount: 1, AddedCount: 1}, []byte("manifest")); err != nil {
		t.Fatal(err)
	}

	second, prev, err := cat.BeginPoint(ctx, lock)
	if err != nil || second.Seq != 2 || prev != 1 {
		t.Fatalf("second point = %+v, prev %d, %v", second, prev, err)
	}
	entry, err := cat.ResolveAt(ctx, src.ID, 1, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ApplyDir(ctx, second, prev, root, []catalog.Change{
		{Kind: catalog.ChangeModify, Name: "a.txt", State: catalog.VersionOK, Size: 4, SHA256: shaTwo, NodeID: entry.NodeID, VersionID: entry.VersionID},
	}, objects[1:]); err != nil {
		t.Fatal(err)
	}

	if err := cat.DiscardPoint(ctx, lock, second); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.GetPoint(ctx, second.ID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("the discarded point is still there: %v", err)
	}
	attempts, err := cat.ListAttempts(ctx, src.ID, 10)
	if err != nil || len(attempts) != 1 || attempts[0].ID != first.ID {
		t.Fatalf("attempts after the discard = %+v, %v", attempts, err)
	}
	var versions, open int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE last_point IS NULL) FROM node_versions`).Scan(&versions, &open); err != nil || versions != 1 || open != 1 {
		t.Fatalf("%d version rows, %d still open (%v); the first point's history must be exactly as it was", versions, open, err)
	}
	if again, err := cat.ResolveAt(ctx, src.ID, 1, "a.txt"); err != nil || again.Size != 3 || again.VersionID != entry.VersionID {
		t.Fatalf("a.txt at point 1 = %+v, %v", again, err)
	}

	// Nothing was recorded, so the next build takes over the sequence number.
	next, _, err := cat.BeginPoint(ctx, lock)
	if err != nil || next.Seq != 2 {
		t.Fatalf("the next point = %+v, %v; want sequence number 2 again", next, err)
	}
	if err := cat.DiscardPoint(ctx, lock, first); err == nil {
		t.Fatal("a committed point must not be discardable")
	}
	if p, err := cat.GetPoint(ctx, first.ID); err != nil || p.State != catalog.StateCommitted {
		t.Fatalf("the committed point after a refused discard = %+v, %v", p, err)
	}
}
