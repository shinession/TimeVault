package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"timevault/internal/audit"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/testenv"
)

var admin = Actor{User: "admin", Remote: "127.0.0.1"}

// harness is a complete back end with a registered source. The job runner is created
// but only runs jobs after startJobs, so a queued job can be observed before it runs.
type harness struct {
	t      *testing.T
	env    *testenv.Env
	runner *jobs.Runner
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	env := testenv.New(t)
	return &harness{t: t, env: env, runner: jobs.NewRunner(env.Pool, slog.New(slog.DiscardHandler), 2)}
}

func (h *harness) service(mutate func(*Config)) *Service {
	log := slog.New(slog.DiscardHandler)
	cfg := Config{
		Pool: h.env.Pool, Catalog: h.env.Cat, Repo: h.env.Repo, Engine: h.env.Eng, Restore: h.env.Restore,
		Jobs: h.runner, Audit: audit.New(h.env.Pool, log), Logger: log,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg)
}

func (h *harness) startJobs() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.runner.Start(ctx)
	h.t.Cleanup(func() {
		cancel()
		stop, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		h.runner.Shutdown(stop)
	})
}

func fastScheduler(c *Config) { c.SchedulerTick = 20 * time.Millisecond }

// startScheduler runs the scheduler until the test ends or stop is called.
func startScheduler(t *testing.T, svc *Service) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunScheduler(ctx)
		close(done)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("RunScheduler did not return after its context ended")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.env.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) points() int {
	return h.count(`SELECT count(*) FROM recovery_points WHERE source_id = $1 AND state = 'committed'`, h.env.Src.ID)
}

func (h *harness) attempts() int {
	return h.count(`SELECT count(*) FROM recovery_points WHERE source_id = $1`, h.env.Src.ID)
}

func (h *harness) auditCount(action string) int {
	return h.count(`SELECT count(*) FROM audit_events WHERE action = $1`, action)
}

// backupJobs lists the backup jobs of the source, newest first.
func (h *harness) backupJobs() []jobs.Job {
	h.t.Helper()
	id := h.env.Src.ID
	list, err := jobs.List(context.Background(), h.env.Pool, jobs.Filter{SourceID: &id, Kind: jobs.KindBackup, Limit: 50})
	if err != nil {
		h.t.Fatal(err)
	}
	return list
}

func (h *harness) busy() bool {
	for _, j := range h.backupJobs() {
		if j.Active() {
			return true
		}
	}
	return false
}

// ranRecently reports whether a backup run of the source ended within the last minute.
func (h *harness) ranRecently() bool {
	return h.count(`SELECT count(*) FROM sources WHERE id = $1 AND last_run_at > now() - interval '1 minute'`, h.env.Src.ID) == 1
}

// makeDue pretends that the last run of the source ended two intervals ago.
func (h *harness) makeDue() {
	h.t.Helper()
	if _, err := h.env.Pool.Exec(context.Background(), `UPDATE sources SET last_run_at = now() - interval '2 minutes' WHERE id = $1`, h.env.Src.ID); err != nil {
		h.t.Fatal(err)
	}
}

// schedule sets the interval and the enabled flag of the harness' source, leaving the rest as it is.
func (h *harness) schedule(svc *Service, minutes int, enabled bool) {
	h.t.Helper()
	src := h.env.Src
	if _, err := svc.UpdateSource(context.Background(), admin, src.ID, backup.SourceRequest{Name: src.Name, Excludes: src.Excludes, ScheduleMinutes: &minutes}, enabled); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) updateEvents() []audit.Event {
	h.t.Helper()
	events, err := h.service(nil).AuditEvents(context.Background(), audit.Filter{Action: audit.ActionSourceUpdate})
	if err != nil {
		h.t.Fatal(err)
	}
	return events
}

// change returns the old and new value an audit record lists for a field.
func change(t *testing.T, ev audit.Event, field string) (oldValue, newValue any) {
	t.Helper()
	m, ok := ev.Detail[field].(map[string]any)
	if !ok {
		t.Fatalf("audit detail %v has no change for %q", ev.Detail, field)
	}
	return m["old"], m["new"]
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestUpdateSourceAuditsOnlyWhatChanged(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	src := h.env.Src
	hourly := 60

	got, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: "Renamed", Excludes: src.Excludes, ScheduleMinutes: &hourly}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.RootPath != src.RootPath || got.Enabled || got.ScheduleMinutes == nil || *got.ScheduleMinutes != 60 {
		t.Fatalf("updated source = %+v", got)
	}
	events := h.updateEvents()
	if len(events) != 1 {
		t.Fatalf("%d audit events, want 1", len(events))
	}
	ev := events[0]
	if ev.Actor != admin.User || ev.TargetType != "source" || ev.SourceID == nil || *ev.SourceID != src.ID || ev.Result != audit.ResultOK {
		t.Fatalf("audit event = %+v", ev)
	}
	if got := keys(ev.Detail); strings.Join(got, ",") != "enabled,name,schedule_minutes" {
		t.Fatalf("audit detail lists %v; only the fields that changed belong in it", got)
	}
	if o, n := change(t, ev, "name"); o != "Team Files" || n != "Renamed" {
		t.Fatalf("name change = %v -> %v", o, n)
	}
	if o, n := change(t, ev, "enabled"); o != true || n != false {
		t.Fatalf("enabled change = %v -> %v", o, n)
	}
	if o, n := change(t, ev, "schedule_minutes"); o != nil || n != float64(60) {
		t.Fatalf("schedule change = %v -> %v", o, n)
	}

	// Saving the same values again succeeds and writes nothing.
	if _, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: " Renamed ", Excludes: src.Excludes, ScheduleMinutes: &hourly}, false); err != nil {
		t.Fatal(err)
	}
	if n := len(h.updateEvents()); n != 1 {
		t.Fatalf("a no-op update wrote an audit event: %d events", n)
	}

	if _, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: "Renamed", Excludes: []string{"*.bak"}, ScheduleMinutes: &hourly}, false); err != nil {
		t.Fatal(err)
	}
	events = h.updateEvents()
	if len(events) != 2 || strings.Join(keys(events[0].Detail), ",") != "excludes" {
		t.Fatalf("audit events after changing the excludes = %+v", events)
	}
	if o, n := change(t, events[0], "excludes"); len(o.([]any)) != len(src.Excludes) || len(n.([]any)) != 1 {
		t.Fatalf("excludes change = %v -> %v", o, n)
	}
}

func TestAddSourceStoresAndAuditsTheSchedule(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	daily := 1440

	src, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Daily", RootPath: t.TempDir(), ScheduleMinutes: &daily})
	if err != nil || src.ScheduleMinutes == nil || *src.ScheduleMinutes != daily {
		t.Fatalf("AddSource = %+v, %v", src, err)
	}
	if stored, err := svc.Source(ctx, src.ID); err != nil || stored.ScheduleMinutes == nil || *stored.ScheduleMinutes != daily {
		t.Fatalf("stored source = %+v, %v", stored, err)
	}
	events, err := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionSourceCreate})
	if err != nil || len(events) != 1 || events[0].Detail["schedule_minutes"] != float64(daily) {
		t.Fatalf("source.create events = %+v, %v; the schedule belongs in the detail", events, err)
	}
}

func TestUpdateSourceRefusesWhatItCannotApply(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	src := h.env.Src

	if _, err := svc.UpdateSource(ctx, admin, 424242, backup.SourceRequest{Name: "x"}, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown source = %v, want ErrNotFound", err)
	}
	other, err := svc.AddSource(ctx, admin, backup.SourceRequest{Name: "Second", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: other.Name}, true); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("a name used by another source = %v, want ErrConflict", err)
	}
	if _, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: src.Name, RootPath: other.RootPath}, true); !errors.Is(err, backup.ErrInvalidSource) {
		t.Fatalf("a folder that is another source = %v, want ErrInvalidSource", err)
	}
	zero := 0
	if _, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: src.Name, ScheduleMinutes: &zero}, true); !errors.Is(err, backup.ErrInvalidSource) {
		t.Fatalf("a schedule of 0 minutes = %v, want ErrInvalidSource", err)
	}

	if now, err := svc.Source(ctx, src.ID); err != nil || now.Name != src.Name || now.ScheduleMinutes != nil {
		t.Fatalf("a refused update changed the source: %+v, %v", now, err)
	}
	if n := len(h.updateEvents()); n != 0 {
		t.Fatalf("refused updates wrote %d audit events", n)
	}
}

// A first backup that starts while the folder is being corrected would record the old folder's files for the
// new one. The folder cannot change while a backup of the source holds its writer lock.
func TestFolderCannotChangeWhileABackupRuns(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	src := h.env.Src
	moved := t.TempDir()
	req := backup.SourceRequest{Name: src.Name, RootPath: moved, Excludes: src.Excludes}

	lock, err := h.env.Cat.LockSource(ctx, src.ID) // what a running backup holds
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSource(ctx, admin, src.ID, req, true); !errors.Is(err, catalog.ErrBusy) {
		t.Fatalf("moving a source while a backup of it runs = %v, want ErrBusy", err)
	}
	// Everything else can still be edited meanwhile: the run works from what it read when it started.
	renamed := backup.SourceRequest{Name: "Renamed", RootPath: src.RootPath, Excludes: src.Excludes}
	if _, err := svc.UpdateSource(ctx, admin, src.ID, renamed, true); err != nil {
		t.Fatalf("renaming a source while a backup of it runs: %v", err)
	}
	lock.Unlock()

	if now, _ := svc.Source(ctx, src.ID); now.RootPath != src.RootPath {
		t.Fatalf("the folder changed to %q although the update was refused", now.RootPath)
	}
	if _, err := svc.UpdateSource(ctx, admin, src.ID, req, true); err != nil {
		t.Fatalf("moving the source once the backup has ended: %v", err)
	}
}

func TestFolderOfASourceWithBackupsCannotChange(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	src := h.env.Src
	moved := t.TempDir()
	req := func(root string, name string) backup.SourceRequest {
		return backup.SourceRequest{Name: name, RootPath: root, Excludes: src.Excludes}
	}

	// Without recovery points the folder can still be corrected.
	got, err := svc.UpdateSource(ctx, admin, src.ID, req(moved, src.Name), true)
	if err != nil || got.RootPath != filepath.Clean(moved) {
		t.Fatalf("moving a source without backups = %+v, %v", got, err)
	}
	if o, n := change(t, h.updateEvents()[0], "root"); o != src.RootPath || n != filepath.Clean(moved) {
		t.Fatalf("root change = %v -> %v", o, n)
	}

	h.env.Backup()
	_, err = svc.UpdateSource(ctx, admin, src.ID, req(t.TempDir(), src.Name), true)
	if !errors.Is(err, backup.ErrInvalidSource) || !strings.Contains(err.Error(), "already has backups") {
		t.Fatalf("moving a source that has backups = %v, want ErrInvalidSource", err)
	}
	if now, _ := svc.Source(ctx, src.ID); now.RootPath != filepath.Clean(moved) {
		t.Fatalf("the folder changed to %q although the update was refused", now.RootPath)
	}

	// Everything else stays editable, and saving the same folder is not a move.
	if _, err := svc.UpdateSource(ctx, admin, src.ID, req(moved, "Renamed"), false); err != nil {
		t.Fatalf("renaming a source that has backups: %v", err)
	}
}

// The scheduler starts a source that never ran at once, waits out the interval, and keeps nothing
// of a run that found no change: no job, no recovery point, no audit record.
func TestSchedulerRecordsOnlyRunsThatFoundChanges(t *testing.T) {
	h := newHarness(t)
	svc := h.service(fastScheduler)
	ctx := context.Background()
	h.startJobs()
	h.env.Write("a.txt", "alpha")
	h.schedule(svc, 1, true)
	id := h.env.Src.ID
	startScheduler(t, svc)

	eventually(t, "the first scheduled backup", func() bool { return h.points() == 1 && !h.busy() })
	first := h.backupJobs()
	if len(first) != 1 || first[0].RequestedBy != "scheduler" || first[0].State != jobs.StateSucceeded || first[0].PointID == nil {
		t.Fatalf("jobs after the first run = %+v", first)
	}
	if !h.ranRecently() {
		t.Fatal("a finished run must restart the interval")
	}
	if h.auditCount(audit.ActionBackupFinish) != 1 || h.auditCount(audit.ActionBackupRequest) != 0 {
		t.Fatalf("audit: %d finished, %d requested; the run is audited, the scheduler's request is not",
			h.auditCount(audit.ActionBackupFinish), h.auditCount(audit.ActionBackupRequest))
	}

	// Many ticks later the source is still not due.
	time.Sleep(300 * time.Millisecond)
	if n := len(h.backupJobs()); n != 1 {
		t.Fatalf("%d jobs; the next run must wait for the interval", n)
	}

	// Due, but nothing changed: the run leaves no trace except the restarted interval.
	h.makeDue()
	eventually(t, "the unchanged run", func() bool { return h.ranRecently() && len(h.backupJobs()) == 1 })
	if h.points() != 1 || h.attempts() != 1 || h.auditCount(audit.ActionBackupFinish) != 1 {
		t.Fatalf("an unchanged run left %d points (%d attempts) and %d finish records", h.points(), h.attempts(), h.auditCount(audit.ActionBackupFinish))
	}

	// Due and changed: a job, a recovery point and an audit record, like any backup.
	h.env.Write("a.txt", "alpha v2")
	h.makeDue()
	eventually(t, "the run that finds the change", func() bool { return h.points() == 2 && !h.busy() })
	jobsNow := h.backupJobs()
	if len(jobsNow) != 2 || jobsNow[0].RequestedBy != "scheduler" || jobsNow[0].State != jobs.StateSucceeded {
		t.Fatalf("jobs after the change = %+v", jobsNow)
	}
	if h.auditCount(audit.ActionBackupFinish) != 2 {
		t.Fatalf("%d finish records, want 2", h.auditCount(audit.ActionBackupFinish))
	}

	// A manual backup is never skipped, even though nothing changed since the last point.
	job, created, err := svc.StartBackup(ctx, admin, id, false)
	if err != nil || !created {
		t.Fatalf("StartBackup = %+v %v %v", job, created, err)
	}
	eventually(t, "the manual backup", func() bool { return h.points() == 3 && !h.busy() })
	if h.auditCount(audit.ActionBackupRequest) != 1 || h.auditCount(audit.ActionBackupFinish) != 3 {
		t.Fatalf("audit: %d requested, %d finished", h.auditCount(audit.ActionBackupRequest), h.auditCount(audit.ActionBackupFinish))
	}
}

func TestSchedulerLeavesDisabledAndManualSourcesAlone(t *testing.T) {
	h := newHarness(t)
	svc := h.service(fastScheduler)
	ctx := context.Background()
	h.startJobs()
	h.env.Write("a.txt", "alpha")
	id := h.env.Src.ID
	stop := startScheduler(t, svc)

	// No schedule yet: manual only.
	time.Sleep(200 * time.Millisecond)
	h.schedule(svc, 1, false)
	time.Sleep(200 * time.Millisecond)
	stop()
	if n := len(h.backupJobs()); n != 0 {
		t.Fatalf("%d jobs for a manual-only and then a disabled source", n)
	}
	if _, _, err := svc.StartScheduledBackup(ctx, id); err == nil {
		t.Fatal("a disabled source must not be scheduled")
	}
	if n := len(h.backupJobs()); n != 0 {
		t.Fatalf("StartScheduledBackup queued a job for a disabled source")
	}
}

func TestScheduledBackupIsCoalescedWithTheActiveOne(t *testing.T) {
	h := newHarness(t)
	svc := h.service(fastScheduler) // the runner is not started, so the first job stays queued
	ctx := context.Background()
	h.env.Write("a.txt", "alpha")
	h.schedule(svc, 1, true)
	id := h.env.Src.ID
	startScheduler(t, svc)

	eventually(t, "the scheduled job", func() bool { return len(h.backupJobs()) == 1 })
	time.Sleep(200 * time.Millisecond) // many more ticks while the job is still active
	queued := h.backupJobs()
	if len(queued) != 1 || queued[0].State != jobs.StateQueued || queued[0].RequestedBy != "scheduler" {
		t.Fatalf("jobs = %+v; the scheduler must not queue a second backup of a busy source", queued)
	}
	var params backupParams
	if err := json.Unmarshal(queued[0].Params, &params); err != nil || params != (backupParams{SourceID: id, Scheduled: true}) {
		t.Fatalf("job parameters = %s (%v), want a scheduled backup of the source", queued[0].Params, err)
	}

	var lastID int64
	if err := h.env.Pool.QueryRow(ctx,
		`SELECT last_value FROM pg_sequences WHERE schemaname = current_schema() AND sequencename = 'jobs_id_seq'`).Scan(&lastID); err != nil || lastID != queued[0].ID {
		t.Fatalf("job ids reached %d (%v); the scheduler must not try to insert more jobs while one is active", lastID, err)
	}

	again, created, err := svc.StartScheduledBackup(ctx, id)
	if err != nil || created || again.ID != queued[0].ID {
		t.Fatalf("StartScheduledBackup while one is queued = %+v %v %v", again, created, err)
	}
	manual, created, err := svc.StartBackup(ctx, admin, id, false)
	if err != nil || created || manual.ID != queued[0].ID {
		t.Fatalf("StartBackup while a scheduled one is queued = %+v %v %v", manual, created, err)
	}
	if n := h.auditCount(audit.ActionBackupRequest); n != 0 {
		t.Fatalf("%d backup requests audited; coalesced and scheduled requests are not requests of a person", n)
	}
}

// A run that fails still restarts the interval, otherwise a source that cannot be read would be
// retried in a loop.
func TestFailedScheduledRunWaitsForTheNextInterval(t *testing.T) {
	h := newHarness(t)
	svc := h.service(fastScheduler)
	h.startJobs()
	h.env.Write("a.txt", "alpha")
	if err := os.RemoveAll(h.env.SrcDir); err != nil { // the drive is gone
		t.Fatal(err)
	}
	h.schedule(svc, 1, true) // a source whose drive is offline can still be rescheduled
	startScheduler(t, svc)

	eventually(t, "the failed run", func() bool {
		js := h.backupJobs()
		return len(js) == 1 && js[0].State == jobs.StateFailed
	})
	if !h.ranRecently() {
		t.Fatal("a failed run must restart the interval")
	}
	if h.auditCount(audit.ActionBackupFinish) != 1 {
		t.Fatalf("%d finish records, want the failure to be audited once", h.auditCount(audit.ActionBackupFinish))
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(h.backupJobs()); n != 1 {
		t.Fatalf("%d jobs; a failing source must be retried once per interval, not in a loop", n)
	}
}

type fakeDirectory struct {
	addr     string
	err      error
	deadline time.Time
}

func (f *fakeDirectory) Ping(ctx context.Context) error {
	f.deadline, _ = ctx.Deadline()
	return f.err
}

func (f *fakeDirectory) Addr() string { return f.addr }

func TestHealthReportsTheUserDirectory(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	find := func(rep HealthReport, name string) (int, Component) {
		t.Helper()
		for i, c := range rep.Components {
			if c.Name == name {
				return i, c
			}
		}
		t.Fatalf("no %q component in %+v", name, rep.Components)
		return 0, Component{}
	}

	rep := h.service(nil).Health(ctx)
	searchAt, _ := find(rep, "search")
	at, c := find(rep, "directory")
	if at != searchAt+1 {
		t.Fatalf("the directory is component %d, want it right after search (%d)", at, searchAt)
	}
	if c.State != "disabled" || c.Detail != "no user directory configured" || c.Key != "directory_off" {
		t.Fatalf("without a directory: %+v", c)
	}

	fake := &fakeDirectory{addr: "directory.example.test:389"}
	rep = h.service(func(c *Config) { c.Directory = fake }).Health(ctx)
	_, c = find(rep, "directory")
	if c.State != "ok" || c.Detail != fake.addr || c.Key != "directory_ok" || len(c.Args) != 1 || c.Args[0] != fake.addr {
		t.Fatalf("with an answering directory: %+v", c)
	}
	if left := time.Until(fake.deadline); fake.deadline.IsZero() || left <= 0 || left > 3*time.Second {
		t.Fatalf("the directory check must be bounded to 3 seconds, its deadline is in %v", left)
	}

	fake.err = errors.New("dial ldap://user:secret-token@directory.example.test:389: connection refused")
	rep = h.service(func(c *Config) { c.Directory = fake }).Health(ctx)
	_, c = find(rep, "directory")
	if c.State != "degraded" || c.Key != "directory_down" || c.Detail != "the user directory does not answer" {
		t.Fatalf("with a silent directory: %+v", c)
	}
	if strings.Contains(c.Detail, "secret-token") || strings.Contains(c.Detail, "ldap://") {
		t.Fatalf("the detail leaks the connection error: %q", c.Detail)
	}
	if !rep.OK() {
		t.Fatal("a silent directory degrades the service, it does not fail it")
	}
}
