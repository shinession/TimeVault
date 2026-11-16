package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"timevault/internal/audit"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/search"
)

func TestAutoGapKeepsUpWithEverySchedule(t *testing.T) {
	cases := []struct {
		name      string
		intervals []int
		want      time.Duration
	}{
		{"ten folders of an hour: one every six minutes", []int{60, 60, 60, 60, 60, 60, 60, 60, 60, 60}, 6 * time.Minute},
		{"one folder: the gap is capped", []int{60}, maxGap},
		{"nothing scheduled", nil, maxGap},
		{"many short schedules: the gap has a floor", []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, minGap},
		{"mixed schedules add up their rates", []int{10, 10, 10, 10, 10, 240, 240, 240, 240, 240}, 115 * time.Second},
	}
	for _, c := range cases {
		got := autoGap(c.intervals)
		if diff := got - c.want; diff < -time.Second || diff > time.Second {
			t.Errorf("%s: autoGap = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRestFollowsHowLongTheRunTook(t *testing.T) {
	for run, want := range map[time.Duration]time.Duration{
		2 * time.Second: minRest, 4 * time.Minute: 4 * time.Minute, 3 * time.Hour: maxRest,
	} {
		if got := restAfter(run); got != want {
			t.Errorf("rest after %v = %v, want %v", run, got, want)
		}
	}
	// Only a single automatic backup at a time rests.
	auto := SchedulerOptions{Parallel: 1, RestAuto: true}
	if auto.restFor(4*time.Minute) != 4*time.Minute || (SchedulerOptions{Parallel: 2, RestAuto: true}).restFor(4*time.Minute) != 0 {
		t.Error("the rest applies to one automatic backup at a time only")
	}
	if got := (SchedulerOptions{Rest: 7 * time.Second}).restFor(time.Hour); got != 7*time.Second {
		t.Errorf("fixed rest = %v", got)
	}
}

func TestPaceWaitsForTheGapAndTheRest(t *testing.T) {
	var p pace
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	rest := func(d time.Duration) time.Duration { return d }

	if !p.earliest(5*time.Minute, rest).Before(start) {
		t.Error("nothing ran yet: any moment is fine")
	}
	p.started(start)
	if got := p.earliest(5*time.Minute, rest); !got.Equal(start.Add(5 * time.Minute)) {
		t.Errorf("earliest after a start = %v", got)
	}
	// A run that took ten minutes is followed by ten minutes of rest, which is later than the gap.
	p.ended(start.Add(10*time.Minute), 10*time.Minute)
	if got := p.earliest(5*time.Minute, rest); !got.Equal(start.Add(20 * time.Minute)) {
		t.Errorf("earliest after a long run = %v, want 20 minutes after the start", got)
	}
	// With no rest only the gap counts.
	if got := p.earliest(5*time.Minute, func(time.Duration) time.Duration { return 0 }); !got.Equal(start.Add(10 * time.Minute)) {
		t.Errorf("earliest without a rest = %v", got)
	}
}

func TestRankDueOrder(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ago := func(m int) *time.Time { t := now.Add(-time.Duration(m) * time.Minute); return &t }
	ms := func(v int64) *int64 { return &v }
	due := []catalog.Due{
		{ID: 1, ScheduleMinutes: 60, LastRunAt: ago(61), HasPoint: true},                   // 1.02 times overdue
		{ID: 2, ScheduleMinutes: 10, LastRunAt: ago(30), HasPoint: true},                   // 3 times overdue
		{ID: 3, ScheduleMinutes: 60, LastRunAt: ago(61), HasPoint: true, LastRunMS: ms(5)}, // as 1, but a run of 5 ms counts
		{ID: 4, ScheduleMinutes: 60, LastRunAt: nil, HasPoint: false},                      // never backed up
		{ID: 5, ScheduleMinutes: 60, LastRunAt: ago(5000), HasPoint: false},                // failed for ages, still no point
		{ID: 6, ScheduleMinutes: 60, LastRunAt: ago(61), HasPoint: true, LastRunMS: ms(9)},
	}
	var got []int64
	for _, d := range rankDue(due, now) {
		got = append(got, d.ID)
	}
	// Without a point first (the most overdue of them first), then by how overdue in proportion to the interval,
	// then the quicker run first; a run of unknown length counts as the quickest.
	want := []int64{5, 4, 2, 1, 3, 6}
	if len(got) != len(want) {
		t.Fatalf("rank = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rank = %v, want %v", got, want)
		}
	}
	if due[0].ID != 1 || due[5].ID != 6 {
		t.Error("rankDue changed the slice it was given")
	}
}

// moreSources registers n more sources with an automatic schedule, each in a folder of its own.
func (h *harness) moreSources(n int, minutes int) []catalog.Source {
	h.t.Helper()
	var out []catalog.Source
	for i := 0; i < n; i++ {
		dir := h.t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
			h.t.Fatal(err)
		}
		src, err := h.env.Cat.CreateSource(context.Background(), catalog.NewSource{
			Name: "extra " + string(rune('A'+i)), RootPath: dir, CreatedBy: "tester", ScheduleMinutes: &minutes,
		})
		if err != nil {
			h.t.Fatal(err)
		}
		out = append(out, src)
	}
	return out
}

// queuedBackups lists the ids of the sources that have a queued or running backup.
func (h *harness) queuedBackups() []int64 {
	h.t.Helper()
	rows, err := h.env.Pool.Query(context.Background(),
		`SELECT source_id FROM jobs WHERE kind = 'backup' AND state IN ('queued', 'running') ORDER BY id`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			h.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// finishQueued pretends that the queued backup of a source ran and ended.
func (h *harness) finishQueued(id int64) {
	h.t.Helper()
	ctx := context.Background()
	if _, err := h.env.Pool.Exec(ctx, `UPDATE jobs SET state = 'succeeded', finished_at = now() WHERE source_id = $1 AND state = 'queued'`, id); err != nil {
		h.t.Fatal(err)
	}
	if err := h.env.Cat.MarkRun(ctx, id); err != nil {
		h.t.Fatal(err)
	}
}

func TestSchedulerStartsOneAutomaticBackupAtATime(t *testing.T) {
	h := newHarness(t)
	extra := h.moreSources(2, 1)
	svc := h.service(fastScheduler) // the runner is not started, so a queued job stays queued
	if _, err := h.env.Pool.Exec(context.Background(), `UPDATE sources SET schedule_minutes = NULL WHERE id = $1`, h.env.Src.ID); err != nil {
		t.Fatal(err)
	}
	startScheduler(t, svc)

	eventually(t, "the first automatic backup", func() bool { return len(h.queuedBackups()) == 1 })
	time.Sleep(300 * time.Millisecond) // many ticks, with two more sources due
	if got := h.queuedBackups(); len(got) != 1 || got[0] != extra[0].ID {
		t.Fatalf("queued backups = %v, want only the first source (%d): one automatic backup at a time", got, extra[0].ID)
	}

	h.finishQueued(extra[0].ID)
	eventually(t, "the second automatic backup", func() bool { return len(h.queuedBackups()) == 1 })
	if got := h.queuedBackups(); got[0] != extra[1].ID {
		t.Fatalf("queued backups = %v, want the second source (%d); the first one just ran", got, extra[1].ID)
	}
}

func TestSchedulerSpacesAutomaticBackupsOut(t *testing.T) {
	h := newHarness(t)
	extra := h.moreSources(2, 1)
	svc := h.service(func(c *Config) {
		fastScheduler(c)
		c.Scheduler = SchedulerOptions{Parallel: 1, Gap: 5 * time.Minute, RestAuto: true}
	})
	if _, err := h.env.Pool.Exec(context.Background(), `UPDATE sources SET schedule_minutes = NULL WHERE id = $1`, h.env.Src.ID); err != nil {
		t.Fatal(err)
	}
	startScheduler(t, svc)
	eventually(t, "the first automatic backup", func() bool { return len(h.queuedBackups()) == 1 })

	// The first one ran for two minutes and ended; the next waits for the gap of five minutes after the start.
	h.finishQueued(extra[0].ID)
	svc.pace.ended(time.Now(), 2*time.Minute)
	time.Sleep(300 * time.Millisecond)
	if got := h.queuedBackups(); len(got) != 0 {
		t.Fatalf("queued backups = %v, want none: the gap since the last start has not passed", got)
	}

	// Once the gap has passed, the rest after a long run still holds the next one back.
	svc.pace.started(time.Now().Add(-10 * time.Minute))
	svc.pace.ended(time.Now(), 4*time.Minute)
	time.Sleep(300 * time.Millisecond)
	if got := h.queuedBackups(); len(got) != 0 {
		t.Fatalf("queued backups = %v, want none: a run of four minutes is followed by four minutes of rest", got)
	}

	svc.pace.ended(time.Now().Add(-5*time.Minute), 4*time.Minute)
	eventually(t, "the next automatic backup after the rest", func() bool { return len(h.queuedBackups()) == 1 })
	if got := h.queuedBackups(); got[0] != extra[1].ID {
		t.Fatalf("queued backups = %v, want the second source (%d)", got, extra[1].ID)
	}
}

func TestSchedulerStartsSourcesWithoutABackupFirst(t *testing.T) {
	h := newHarness(t)
	svc := h.service(fastScheduler)
	h.env.Write("a.txt", "alpha")
	h.env.Backup()
	h.schedule(svc, 1, true)
	h.makeDue() // overdue, and it has a recovery point
	fresh := h.moreSources(1, 60)[0]
	startScheduler(t, svc)

	eventually(t, "an automatic backup", func() bool { return len(h.queuedBackups()) == 1 })
	if got := h.queuedBackups(); got[0] != fresh.ID {
		t.Fatalf("queued backups = %v, want the source that was never backed up (%d) first", got, fresh.ID)
	}
}

// probeHarness is a source with a recovery point and a complete scan on record, scheduled every minute and due,
// with a search engine that answers the probes the test sets.
func probeHarness(t *testing.T, options SchedulerOptions) (*harness, *Service, *search.Mock, int64) {
	t.Helper()
	h := newHarness(t)
	mock := search.NewMock(nil)
	svc := h.service(func(c *Config) {
		fastScheduler(c)
		c.Search = mock
		c.Scheduler = options
	})
	h.env.Write("a.txt", "alpha")
	h.env.Backup()
	h.schedule(svc, 1, true)
	items := int64(42)
	walked := time.Now().Add(-10 * time.Minute)
	if err := h.env.Cat.RecordRun(context.Background(), h.env.Src.ID, catalog.RunEnd{Duration: time.Second, WalkStarted: walked, WalkItems: &items}); err != nil {
		t.Fatal(err)
	}
	h.makeDue()
	return h, svc, mock, items
}

func TestSchedulerSkipsTheScanWhenTheSearchEngineSeesNoChange(t *testing.T) {
	h, svc, mock, items := probeHarness(t, SchedulerOptions{Parallel: 1, Probe: true, FullScanEvery: 24 * time.Hour})
	mock.SetProbe(h.env.SrcDir, search.ProbeResult{Items: items})
	startScheduler(t, svc)

	eventually(t, "the probe", func() bool { return mock.ProbeCalls() > 0 && h.ranRecently() })
	time.Sleep(200 * time.Millisecond)
	if got := h.queuedBackups(); len(got) != 0 || len(h.backupJobs()) != 0 {
		t.Fatalf("jobs = %v; a folder the search engine sees no change in is not scanned", h.backupJobs())
	}
	if h.points() != 1 {
		t.Fatalf("%d recovery points", h.points())
	}
	// The skip restarted the interval but did not pretend that the folder was scanned.
	var walkAgo float64
	if err := h.env.Pool.QueryRow(context.Background(),
		`SELECT extract(epoch FROM now() - last_walk_at) FROM sources WHERE id = $1`, h.env.Src.ID).Scan(&walkAgo); err != nil || walkAgo < 9*60 {
		t.Fatalf("last scan was %.0f seconds ago (%v); a skipped scan must leave it alone", walkAgo, err)
	}
}

func TestSchedulerScansWhenTheProbeCannotVouchForTheFolder(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *harness, mock *search.Mock, items int64)
	}{
		{"something changed", func(h *harness, m *search.Mock, n int64) {
			m.SetProbe(h.env.SrcDir, search.ProbeResult{Items: n, Changed: true})
		}},
		{"something was deleted", func(h *harness, m *search.Mock, n int64) { m.SetProbe(h.env.SrcDir, search.ProbeResult{Items: n - 1}) }},
		{"the folder is not indexed", func(h *harness, m *search.Mock, n int64) {}},
		{"the search engine is down", func(h *harness, m *search.Mock, n int64) {
			m.SetProbe(h.env.SrcDir, search.ProbeResult{Items: n})
			m.SetUnavailable(true)
		}},
		{"the last scan left items uncaptured", func(h *harness, m *search.Mock, n int64) {
			m.SetProbe(h.env.SrcDir, search.ProbeResult{Items: n})
			if _, err := h.env.Pool.Exec(context.Background(), `UPDATE recovery_points SET skipped_count = 2 WHERE source_id = $1`, h.env.Src.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"the last complete scan is old", func(h *harness, m *search.Mock, n int64) {
			m.SetProbe(h.env.SrcDir, search.ProbeResult{Items: n})
			if _, err := h.env.Pool.Exec(context.Background(), `UPDATE sources SET last_walk_at = now() - interval '25 hours' WHERE id = $1`, h.env.Src.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"no scan was ever recorded", func(h *harness, m *search.Mock, n int64) {
			m.SetProbe(h.env.SrcDir, search.ProbeResult{Items: n})
			if _, err := h.env.Pool.Exec(context.Background(), `UPDATE sources SET last_walk_at = NULL, last_walk_items = NULL WHERE id = $1`, h.env.Src.ID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, svc, mock, items := probeHarness(t, SchedulerOptions{Parallel: 1, Probe: true, FullScanEvery: 24 * time.Hour})
			c.setup(h, mock, items)
			startScheduler(t, svc)
			eventually(t, "a scan of the folder", func() bool { return len(h.queuedBackups()) == 1 })
		})
	}
}

func TestSchedulerDoesNotProbeWhenItIsOff(t *testing.T) {
	h, svc, mock, items := probeHarness(t, SchedulerOptions{Parallel: 1})
	mock.SetProbe(h.env.SrcDir, search.ProbeResult{Items: items})
	startScheduler(t, svc)
	eventually(t, "a scan of the folder", func() bool { return len(h.queuedBackups()) == 1 })
	if mock.ProbeCalls() != 0 {
		t.Errorf("the search engine was probed %d times although probing is off", mock.ProbeCalls())
	}
}

// A search engine that does not answer is asked once and then left alone for a while, not once for every due folder
// in every tick. A folder the engine does not know says nothing about an outage.
func TestAStoppedSearchEngineIsAskedOnceAndThenLeftAlone(t *testing.T) {
	h, svc, mock, items := probeHarness(t, SchedulerOptions{Parallel: 1, Probe: true, FullScanEvery: 24 * time.Hour})
	ctx := context.Background()
	mock.SetProbe(h.env.SrcDir, search.ProbeResult{Items: items})
	due, err := h.env.Cat.DueNow(ctx)
	if err != nil || len(due) != 1 {
		t.Fatalf("due = %+v, %v", due, err)
	}
	d, now := due[0], time.Now()

	unknown := d
	unknown.RootPath = filepath.Join(t.TempDir(), "unknown")
	if skip, asked := svc.probeUnchanged(ctx, unknown, now); skip || !asked {
		t.Fatalf("a folder the engine does not know = skip %v, asked %v; want a scan after one question", skip, asked)
	}
	if skip, asked := svc.probeUnchanged(ctx, d, now); !skip || !asked {
		t.Fatalf("an unchanged folder = skip %v, asked %v; want the scan skipped, so the engine is not left alone for that", skip, asked)
	}

	mock.SetUnavailable(true)
	calls := mock.ProbeCalls()
	if skip, asked := svc.probeUnchanged(ctx, d, now); skip || !asked || mock.ProbeCalls() != calls+1 {
		t.Fatalf("an engine that does not answer = skip %v, asked %v, %d questions; want one question and a scan", skip, asked, mock.ProbeCalls()-calls)
	}
	if skip, asked := svc.probeUnchanged(ctx, d, now.Add(time.Second)); skip || asked || mock.ProbeCalls() != calls+1 {
		t.Fatalf("an engine that is being left alone = skip %v, asked %v, %d questions; want none", skip, asked, mock.ProbeCalls()-calls)
	}

	mock.SetUnavailable(false)
	if skip, asked := svc.probeUnchanged(ctx, d, now.Add(probeBackoff+time.Second)); !skip || !asked {
		t.Fatalf("after the pause = skip %v, asked %v; want the engine asked again", skip, asked)
	}
}

// With several folders due, a tick asks an engine that does not answer once, and the next ticks leave it alone.
func TestATickAsksAStoppedSearchEngineOnce(t *testing.T) {
	h, svc, mock, items := probeHarness(t, SchedulerOptions{Parallel: 1, Probe: true, FullScanEvery: 24 * time.Hour})
	ctx := context.Background()
	mock.SetProbe(h.env.SrcDir, search.ProbeResult{Items: items})
	for _, e := range h.moreSources(2, 1) { // each with a recovery point and a complete scan on record, and due
		if _, err := h.env.Eng.Run(ctx, e.ID, backup.RunOptions{}, backup.NopReporter{}); err != nil {
			t.Fatal(err)
		}
		n := int64(1)
		if err := h.env.Cat.RecordRun(ctx, e.ID, catalog.RunEnd{Duration: time.Second, WalkStarted: time.Now().Add(-10 * time.Minute), WalkItems: &n}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.env.Pool.Exec(ctx, `UPDATE sources SET last_run_at = now() - interval '2 minutes' WHERE id = $1`, e.ID); err != nil {
			t.Fatal(err)
		}
		mock.SetProbe(e.RootPath, search.ProbeResult{Items: n})
	}
	mock.SetUnavailable(true)

	svc.queueDue(ctx, map[int64]time.Time{})
	if mock.ProbeCalls() != 1 || len(h.queuedBackups()) != 1 {
		t.Fatalf("%d questions and %d queued backups after a tick with three due folders; want one question and one scan", mock.ProbeCalls(), len(h.queuedBackups()))
	}
	svc.queueDue(ctx, map[int64]time.Time{})
	if mock.ProbeCalls() != 1 {
		t.Fatalf("%d questions after a second tick, want the engine left alone", mock.ProbeCalls())
	}
}

func TestBackupRunsKeepWhatTheProbeNeeds(t *testing.T) {
	h := newHarness(t)
	mock := search.NewMock(nil)
	svc := h.service(func(c *Config) {
		c.Search = mock
		c.Scheduler = SchedulerOptions{Parallel: 1, Probe: true, FullScanEvery: 24 * time.Hour}
	})
	ctx := context.Background()
	h.startJobs()
	h.env.Write("a.txt", "alpha")
	mock.SetProbe(h.env.SrcDir, search.ProbeResult{Items: 17})

	read := func() (walk *time.Time, items *int64, ms *int64) {
		src, err := svc.Source(ctx, h.env.Src.ID)
		if err != nil {
			t.Fatal(err)
		}
		return src.LastWalkAt, src.LastWalkItems, src.LastRunMS
	}
	if walk, items, ms := read(); walk != nil || items != nil || ms != nil {
		t.Fatal("a source that never ran has no scan on record")
	}

	// A manual backup is a complete scan: the time it began and the items the engine saw then are kept.
	if _, _, err := svc.StartBackup(ctx, admin, h.env.Src.ID, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the manual backup", func() bool { return h.points() == 1 && !h.busy() })
	walk, items, ms := read()
	if walk == nil || items == nil || *items != 17 || ms == nil || time.Since(*walk) > time.Minute {
		t.Fatalf("scan on record = %v %v %v, want the start of the run and 17 items", walk, items, ms)
	}

	// A run that finds nothing changed is a complete scan too.
	mock.SetProbe(h.env.SrcDir, search.ProbeResult{Items: 18})
	first := *walk
	time.Sleep(20 * time.Millisecond)
	if _, _, err := svc.StartScheduledBackup(ctx, h.env.Src.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the unchanged run", func() bool { w, _, _ := read(); return w != nil && w.After(first) })
	eventually(t, "the unchanged run to be forgotten", func() bool { return !h.busy() })
	if _, items, _ := read(); items == nil || *items != 18 {
		t.Fatalf("items on record = %v, want 18 from the newest scan", items)
	}

	// A run that fails scanned nothing completely: the record stays.
	before, _, _ := read()
	if err := os.RemoveAll(h.env.SrcDir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.StartScheduledBackup(ctx, h.env.Src.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the failed run", func() bool {
		js := h.backupJobs()
		return len(js) > 0 && js[0].State == jobs.StateFailed
	})
	if after, items, _ := read(); !after.Equal(*before) || *items != 18 {
		t.Fatalf("scan on record after a failed run = %v %v, want it untouched (%v, 18)", after, items, before)
	}
}

func TestBackupFinishIsAuditedWithItsDuration(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	h.startJobs()
	h.env.Write("a.txt", "alpha")
	h.env.Write("docs/b.txt", "bravo bravo")

	if _, _, err := svc.StartBackup(ctx, admin, h.env.Src.ID, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the backup", func() bool { return h.points() == 1 && !h.busy() })

	events, err := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionBackupFinish})
	if err != nil || len(events) != 1 {
		t.Fatalf("backup.finish events = %v, %v", events, err)
	}
	finish := events[0]
	if !finish.HasDuration() || finish.Duration > time.Minute {
		t.Fatalf("duration = %v, want how long the run took", finish.Duration)
	}
	if finish.Detail["bytes"] != float64(16) || finish.Detail["new_bytes"] != float64(16) || finish.Detail["new_objects"] != float64(2) {
		t.Errorf("detail = %v, want the bytes of the point and what was copied", finish.Detail)
	}
	requests, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionBackupRequest})
	if len(requests) != 1 || requests[0].HasDuration() {
		t.Errorf("backup.request = %+v; a request has no duration", requests)
	}

	// A failed run is timed as well, and so is a verification.
	pointID := *h.backupJobs()[0].PointID
	if _, _, err := svc.StartVerify(ctx, admin, h.env.Src.ID, pointID, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the verification", func() bool {
		ev, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionVerifyFinish})
		return len(ev) == 1
	})
	ev, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionVerifyFinish})
	if !ev[0].HasDuration() {
		t.Errorf("verify.finish has no duration: %+v", ev[0])
	}

	if err := os.RemoveAll(h.env.SrcDir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.StartBackup(ctx, admin, h.env.Src.ID, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the failed backup", func() bool {
		ev, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionBackupFinish})
		return len(ev) == 2
	})
	ev, _ = svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionBackupFinish})
	if ev[0].Result != audit.ResultFailed || !ev[0].HasDuration() {
		t.Errorf("the failed backup is recorded as %+v, want a result of failed with its duration", ev[0])
	}
}
