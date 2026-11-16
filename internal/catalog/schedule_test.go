package catalog_test

import (
	"context"
	"testing"
	"time"

	"timevault/internal/catalog"
)

func TestRecordRunKeepsWhatTheSchedulerNeeds(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	hourly := 60
	src := mustCreate(t, cat, "timed", &hourly)
	if src.LastRunMS != nil || src.LastWalkAt != nil || src.LastWalkItems != nil || src.MaxVersions != nil {
		t.Fatalf("a new source has nothing on record: %+v", src)
	}

	// A complete scan: the duration, when it began and how many entries the search engine held then.
	began := time.Now().Add(-5 * time.Minute).Truncate(time.Millisecond)
	items := int64(1234)
	if err := cat.RecordRun(ctx, src.ID, catalog.RunEnd{Duration: 90 * time.Second, WalkStarted: began, WalkItems: &items}); err != nil {
		t.Fatal(err)
	}
	got, _ := cat.GetSource(ctx, src.ID)
	if got.LastRunAt == nil || time.Since(*got.LastRunAt) > time.Minute || got.LastRunMS == nil || *got.LastRunMS != 90_000 ||
		got.LastWalkAt == nil || !got.LastWalkAt.Equal(began) || got.LastWalkItems == nil || *got.LastWalkItems != 1234 {
		t.Fatalf("after a complete scan: %+v", got)
	}

	// A run that did not scan completely restarts the interval and keeps the length, nothing else.
	if err := cat.RecordRun(ctx, src.ID, catalog.RunEnd{Duration: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
	got, _ = cat.GetSource(ctx, src.ID)
	if *got.LastRunMS != 2000 || !got.LastWalkAt.Equal(began) || *got.LastWalkItems != 1234 {
		t.Fatalf("after a run without a complete scan: %+v", got)
	}

	// MarkRun only restarts the interval.
	if err := cat.MarkRun(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = cat.GetSource(ctx, src.ID)
	if *got.LastRunMS != 2000 || !got.LastWalkAt.Equal(began) {
		t.Fatalf("after MarkRun: %+v", got)
	}

	// A scan that the search engine could not count replaces the old count with nothing, so the next probe
	// cannot compare with a number that belongs to an older scan.
	later := began.Add(time.Minute)
	if err := cat.RecordRun(ctx, src.ID, catalog.RunEnd{Duration: time.Second, WalkStarted: later}); err != nil {
		t.Fatal(err)
	}
	got, _ = cat.GetSource(ctx, src.ID)
	if !got.LastWalkAt.Equal(later) || got.LastWalkItems != nil {
		t.Fatalf("after a scan without a count: %+v", got)
	}
}

func TestChangingTheFolderForgetsItsRunsAndScans(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	every30 := 30
	src := mustCreate(t, cat, "moves", &every30)
	items := int64(5)
	if err := cat.RecordRun(ctx, src.ID, catalog.RunEnd{Duration: time.Second, WalkStarted: time.Now(), WalkItems: &items}); err != nil {
		t.Fatal(err)
	}

	same, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "renamed", RootPath: src.RootPath, Enabled: true, ScheduleMinutes: &every30})
	if err != nil || same.LastRunAt == nil || same.LastRunMS == nil || same.LastWalkAt == nil || same.LastWalkItems == nil {
		t.Fatalf("a rename must keep the record: %+v, %v", same, err)
	}

	// New exclude patterns change what a scan has to capture although no file changed, so the search engine cannot
	// vouch for the folder any more: the last scan is forgotten, the interval is not.
	excluded, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "renamed", RootPath: src.RootPath, Excludes: []string{"*.tmp"}, Enabled: true, ScheduleMinutes: &every30})
	if err != nil || excluded.LastRunAt == nil || excluded.LastRunMS == nil || excluded.LastWalkAt != nil || excluded.LastWalkItems != nil {
		t.Fatalf("new exclude patterns must forget the last scan and nothing else: %+v, %v", excluded, err)
	}
	if err := cat.RecordRun(ctx, src.ID, catalog.RunEnd{Duration: time.Second, WalkStarted: time.Now(), WalkItems: &items}); err != nil {
		t.Fatal(err)
	}
	kept, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "renamed", RootPath: src.RootPath, Excludes: []string{"*.tmp"}, Enabled: true, ScheduleMinutes: &every30})
	if err != nil || kept.LastWalkAt == nil || kept.LastWalkItems == nil {
		t.Fatalf("the same patterns must keep the last scan: %+v, %v", kept, err)
	}

	moved, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "renamed", RootPath: folder("elsewhere"), Excludes: []string{"*.tmp"}, Enabled: true, ScheduleMinutes: &every30})
	if err != nil || moved.LastRunAt != nil || moved.LastRunMS != nil || moved.LastWalkAt != nil || moved.LastWalkItems != nil {
		t.Fatalf("another folder starts without a record: %+v, %v", moved, err)
	}
}

func TestMaxVersionsIsStoredAndBounded(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)
	five := 5
	src, err := cat.CreateSource(ctx, catalog.NewSource{Name: "limited", RootPath: folder("limited"), CreatedBy: "t", MaxVersions: &five})
	if err != nil || src.MaxVersions == nil || *src.MaxVersions != 5 {
		t.Fatalf("created = %+v, %v", src, err)
	}
	three := 3
	up, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "limited", RootPath: src.RootPath, Enabled: true, MaxVersions: &three})
	if err != nil || up.MaxVersions == nil || *up.MaxVersions != 3 {
		t.Fatalf("updated = %+v, %v", up, err)
	}
	up, err = cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "limited", RootPath: src.RootPath, Enabled: true})
	if err != nil || up.MaxVersions != nil {
		t.Fatalf("no limit = %+v, %v", up, err)
	}
	for _, bad := range []int{0, -1, 10001} {
		if _, err := pool.Exec(ctx, `UPDATE sources SET max_versions = $2 WHERE id = $1`, src.ID, bad); err == nil {
			t.Errorf("a limit of %d was accepted: 0 is spelled NULL and the largest is 10000", bad)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE sources SET max_versions = 10000 WHERE id = $1`, src.ID); err != nil {
		t.Errorf("the largest limit: %v", err)
	}
}

func TestDueNowDescribesWhatTheSchedulerDecidesWith(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)
	hourly := 60
	fresh := mustCreate(t, cat, "fresh", &hourly)
	backedUp := mustCreate(t, cat, "backed up", &hourly)
	mustCreate(t, cat, "manual", nil)

	// A committed point with items that could not be captured, and a scan on record.
	if _, err := pool.Exec(ctx,
		`INSERT INTO recovery_points (source_id, seq, kind, state, skipped_count) VALUES ($1, 1, 'full', 'committed', 3), ($1, 2, 'incremental', 'failed', 9)`, backedUp.ID); err != nil {
		t.Fatal(err)
	}
	items := int64(77)
	walked := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	if _, err := pool.Exec(ctx, `UPDATE sources SET last_run_at = now() - interval '90 minutes', last_run_ms = 1500, last_walk_at = $2, last_walk_items = $3 WHERE id = $1`,
		backedUp.ID, walked, items); err != nil {
		t.Fatal(err)
	}

	due, err := cat.DueNow(ctx)
	if err != nil || len(due) != 2 {
		t.Fatalf("DueNow = %+v, %v", due, err)
	}
	// A source that never ran comes first.
	if due[0].ID != fresh.ID || due[0].HasPoint || due[0].LastRunAt != nil || due[0].LatestSkipped != 0 || due[0].ScheduleMinutes != 60 || due[0].RootPath != fresh.RootPath {
		t.Errorf("fresh = %+v", due[0])
	}
	d := due[1]
	if d.ID != backedUp.ID || !d.HasPoint || d.LatestSkipped != 3 || d.LastRunMS == nil || *d.LastRunMS != 1500 ||
		d.LastWalkAt == nil || !d.LastWalkAt.Equal(walked) || d.LastWalkItems == nil || *d.LastWalkItems != 77 {
		t.Errorf("backed up = %+v; the newest committed point (not the failed attempt) tells what was skipped", d)
	}
}

func TestScheduleIntervalsListsTheEnabledSchedules(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	hourly, daily := 60, 1440
	mustCreate(t, cat, "a", &hourly)
	mustCreate(t, cat, "b", &hourly)
	mustCreate(t, cat, "c", &daily)
	mustCreate(t, cat, "manual", nil)
	off := mustCreate(t, cat, "off", &hourly)
	if _, err := cat.UpdateSource(ctx, off.ID, catalog.SourceUpdate{Name: "off", RootPath: off.RootPath, Enabled: false, ScheduleMinutes: &hourly}); err != nil {
		t.Fatal(err)
	}
	got, err := cat.ScheduleIntervals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sum, n := 0, len(got)
	for _, m := range got {
		sum += m
	}
	if n != 3 || sum != 60+60+1440 {
		t.Fatalf("ScheduleIntervals = %v, want the three enabled schedules", got)
	}
}
