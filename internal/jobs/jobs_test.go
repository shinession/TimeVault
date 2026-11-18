package jobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/db/dbtest"
	"timevault/internal/jobs"
)

func setup(t *testing.T, workers int) (*jobs.Runner, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.New(t)
	return jobs.NewRunner(pool, slog.New(slog.DiscardHandler), workers), pool
}

func addSource(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO sources (name, root_path, root_key, created_by) VALUES ($1, $1, $1, 'tester') RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func wait(t *testing.T, pool *pgxpool.Pool, id int64, until func(jobs.Job) bool) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		j, err := jobs.Get(context.Background(), pool, id)
		if err != nil {
			t.Fatal(err)
		}
		if until(j) {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %d did not reach the expected state in time", id)
	return jobs.Job{}
}

func finished(j jobs.Job) bool { return !j.Active() }
func running(j jobs.Job) bool  { return j.State == jobs.StateRunning }

func start(t *testing.T, r *jobs.Runner) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	t.Cleanup(func() {
		cancel()
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		r.Shutdown(stop)
	})
	return cancel
}

func TestJobRunsAndRecordsPhaseProgressAndLog(t *testing.T) {
	r, pool := setup(t, 2)
	r.Handle(jobs.KindVerify, func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		rep.SetPhase("working")
		rep.SetProgress(map[string]int{"done": 3})
		rep.Event("info", "halfway")
		rep.Event("bogus-level", "normalized to info")
		return nil
	})
	start(t, r)

	job, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindVerify, Params: map[string]int{"n": 1}, RequestedBy: "alice"})
	if err != nil || !created {
		t.Fatalf("Enqueue = %+v %v %v", job, created, err)
	}
	got := wait(t, pool, job.ID, finished)
	var progress map[string]int
	var params map[string]int
	if err := json.Unmarshal(got.Progress, &progress); err != nil || progress["done"] != 3 {
		t.Fatalf("progress = %s (%v)", got.Progress, err)
	}
	if err := json.Unmarshal(got.Params, &params); err != nil || params["n"] != 1 {
		t.Fatalf("params = %s (%v)", got.Params, err)
	}
	if got.State != jobs.StateSucceeded || got.Phase != "working" {
		t.Fatalf("job = %+v", got)
	}
	if got.StartedAt == nil || got.FinishedAt == nil || got.RequestedBy != "alice" {
		t.Fatalf("job bookkeeping = %+v", got)
	}
	events, err := jobs.Events(context.Background(), pool, job.ID, 0, 10)
	if err != nil || len(events) != 2 || events[0].Message != "halfway" || events[1].Level != "info" {
		t.Fatalf("events = %+v %v", events, err)
	}
	if later, _ := jobs.Events(context.Background(), pool, job.ID, events[0].ID, 10); len(later) != 1 {
		t.Fatalf("events after the first = %+v", later)
	}
}

// Drain is for a restart that waits until the service is idle: the jobs that run go on, none is taken any more (not
// even one that is enqueued later) and Running says when the last one is through. Resume takes the queued ones again.
func TestDrainLetsRunningJobsFinishAndTakesNoMoreUntilResumed(t *testing.T) {
	r, pool := setup(t, 2)
	ctx := context.Background()
	slow, other := addSource(t, pool, "slow"), addSource(t, pool, "other")
	started, release := make(chan struct{}), make(chan struct{})
	r.Handle(jobs.KindBackup, func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		if *j.SourceID == slow {
			close(started)
			<-release
		}
		return nil
	})
	start(t, r)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	running, _, err := r.Enqueue(ctx, jobs.NewJob{Kind: jobs.KindBackup, SourceID: &slow, RequestedBy: "a"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the job did not start")
	}
	if got := r.Running(); got != 1 {
		t.Fatalf("Running = %d with one job running, want 1", got)
	}

	r.Drain()
	queued, _, err := r.Enqueue(ctx, jobs.NewJob{Kind: jobs.KindBackup, SourceID: &other, RequestedBy: "a"})
	if err != nil {
		t.Fatal(err)
	}
	// A worker is free and was woken by the new job; while draining it does not take it.
	time.Sleep(700 * time.Millisecond)
	if j, _ := jobs.Get(ctx, pool, queued.ID); j.State != jobs.StateQueued {
		t.Fatalf("a job that was enqueued while draining is %s, want queued", j.State)
	}
	if got := r.Running(); got != 1 {
		t.Fatalf("Running = %d while draining, want the 1 job that was running", got)
	}

	close(release)
	wait(t, pool, running.ID, finished)
	deadline := time.Now().Add(5 * time.Second)
	for r.Running() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.Running(); got != 0 {
		t.Fatalf("Running = %d after the last job ended, want 0", got)
	}
	time.Sleep(300 * time.Millisecond)
	if j, _ := jobs.Get(ctx, pool, queued.ID); j.State != jobs.StateQueued {
		t.Fatalf("the queued job is %s although the runner is idle and draining, want queued", j.State)
	}

	r.Resume()
	if j := wait(t, pool, queued.ID, finished); j.State != jobs.StateSucceeded {
		t.Fatalf("the queued job after Resume = %+v", j)
	}
}

func TestFailuresAndPanicsAreRecordedNotPropagated(t *testing.T) {
	r, pool := setup(t, 2)
	r.Handle(jobs.KindVerify, func(context.Context, jobs.Job, *jobs.Reporter) error { return errors.New("disk on fire") })
	r.Handle(jobs.KindRestore, func(context.Context, jobs.Job, *jobs.Reporter) error { panic("handler bug") })
	start(t, r)

	failed, _, _ := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindVerify, RequestedBy: "a"})
	panicked, _, _ := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindRestore, RequestedBy: "a"})
	unknown, _, _ := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindBackup, RequestedBy: "a"})

	if j := wait(t, pool, failed.ID, finished); j.State != jobs.StateFailed || j.Error != "disk on fire" {
		t.Fatalf("failed job = %+v", j)
	}
	if j := wait(t, pool, panicked.ID, finished); j.State != jobs.StateFailed || !strings.Contains(j.Error, "panicked") {
		t.Fatalf("panicked job = %+v", j)
	}
	if j := wait(t, pool, unknown.ID, finished); j.State != jobs.StateFailed || !strings.Contains(j.Error, "no handler") {
		t.Fatalf("job without a handler = %+v", j)
	}
}

func TestOnlyOneActiveBackupPerSource(t *testing.T) {
	r, pool := setup(t, 1)
	src := addSource(t, pool, "s1")
	release := make(chan struct{})
	r.Handle(jobs.KindBackup, func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		<-release
		return nil
	})
	start(t, r)

	first, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindBackup, SourceID: &src, RequestedBy: "a"})
	if err != nil || !created {
		t.Fatal(err)
	}
	wait(t, pool, first.ID, running)
	second, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindBackup, SourceID: &src, RequestedBy: "b"})
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("a second backup of the same source must return the active one: %+v created=%v err=%v", second, created, err)
	}
	other := addSource(t, pool, "s2")
	if _, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindBackup, SourceID: &other, RequestedBy: "a"}); err != nil || !created {
		t.Fatalf("another source must be allowed: created=%v err=%v", created, err)
	}

	close(release)
	wait(t, pool, first.ID, finished)
	third, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindBackup, SourceID: &src, RequestedBy: "a"})
	if err != nil || !created || third.ID == first.ID {
		t.Fatalf("after completion a new backup is allowed: %+v created=%v err=%v", third, created, err)
	}
	wait(t, pool, third.ID, finished)
}

func TestIdempotencyKeyReturnsTheOriginalJob(t *testing.T) {
	r, pool := setup(t, 1)
	r.Handle(jobs.KindRestore, func(context.Context, jobs.Job, *jobs.Reporter) error { return nil })
	start(t, r)

	a, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindRestore, RequestedBy: "alice", IdempotencyKey: "key-1"})
	if err != nil || !created {
		t.Fatal(err)
	}
	b, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindRestore, RequestedBy: "alice", IdempotencyKey: "key-1"})
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("repeat = %+v created=%v err=%v", b, created, err)
	}
	found, ok, err := r.FindByKey(context.Background(), jobs.KindRestore, "key-1")
	if err != nil || !ok || found.ID != a.ID {
		t.Fatalf("FindByKey = %+v %v %v", found, ok, err)
	}
	if _, ok, _ := r.FindByKey(context.Background(), jobs.KindRestore, "other"); ok {
		t.Fatal("an unknown key must not match")
	}
	wait(t, pool, a.ID, finished)
	c, created, err := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindRestore, RequestedBy: "alice", IdempotencyKey: "key-2"})
	if err != nil || !created || c.ID == a.ID {
		t.Fatalf("a new key starts a new job: %+v %v %v", c, created, err)
	}
}

func TestCancelRunningAndQueuedJobs(t *testing.T) {
	r, pool := setup(t, 1)
	started := make(chan struct{})
	r.Handle(jobs.KindVerify, func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	r.Handle(jobs.KindRestore, func(context.Context, jobs.Job, *jobs.Reporter) error {
		t.Error("a canceled queued job must never run")
		return nil
	})
	start(t, r)

	blocking, _, _ := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindVerify, RequestedBy: "a"})
	<-started
	queued, _, _ := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindRestore, RequestedBy: "a"})

	if ok, err := r.Cancel(context.Background(), queued.ID); err != nil || !ok {
		t.Fatalf("cancel queued = %v %v", ok, err)
	}
	if j := wait(t, pool, queued.ID, finished); j.State != jobs.StateCanceled {
		t.Fatalf("queued job = %+v", j)
	}
	if ok, err := r.Cancel(context.Background(), blocking.ID); err != nil || !ok {
		t.Fatalf("cancel running = %v %v", ok, err)
	}
	if j := wait(t, pool, blocking.ID, finished); j.State != jobs.StateCanceled || j.Error != "canceled" {
		t.Fatalf("running job = %+v", j)
	}
	if ok, _ := r.Cancel(context.Background(), blocking.ID); ok {
		t.Fatal("a finished job cannot be canceled")
	}
}

func TestRecoverInterruptedMarksRunningJobsFailed(t *testing.T) {
	r, pool := setup(t, 1)
	ctx := context.Background()
	var running, queued int64
	if err := pool.QueryRow(ctx, `INSERT INTO jobs (kind, state, requested_by) VALUES ('verify', 'running', 'a') RETURNING id`).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO jobs (kind, state, requested_by) VALUES ('verify', 'queued', 'a') RETURNING id`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	n, err := r.RecoverInterrupted(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RecoverInterrupted = %d %v", n, err)
	}
	if j, _ := jobs.Get(ctx, pool, running); j.State != jobs.StateFailed || !strings.Contains(j.Error, "restart") || j.FinishedAt == nil {
		t.Fatalf("interrupted job = %+v", j)
	}
	if j, _ := jobs.Get(ctx, pool, queued); j.State != jobs.StateQueued {
		t.Fatalf("a queued job must stay queued, got %+v", j)
	}
}

func TestShutdownStopsRunningJobsAndSaysWhy(t *testing.T) {
	r, pool := setup(t, 1)
	started := make(chan struct{})
	r.Handle(jobs.KindVerify, func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	cancel := start(t, r)
	job, _, _ := r.Enqueue(context.Background(), jobs.NewJob{Kind: jobs.KindVerify, RequestedBy: "a"})
	<-started
	cancel()
	stop, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	r.Shutdown(stop)
	j := wait(t, pool, job.ID, finished)
	if j.State != jobs.StateFailed || !strings.Contains(j.Error, "service was stopping") {
		t.Fatalf("job after shutdown = %+v", j)
	}
}

func TestListAndGet(t *testing.T) {
	r, pool := setup(t, 1)
	src := addSource(t, pool, "s1")
	ctx := context.Background()
	a, _, _ := r.Enqueue(ctx, jobs.NewJob{Kind: jobs.KindBackup, SourceID: &src, RequestedBy: "a"})
	b, _, _ := r.Enqueue(ctx, jobs.NewJob{Kind: jobs.KindVerify, RequestedBy: "a"})
	all, err := jobs.List(ctx, pool, jobs.Filter{})
	if err != nil || len(all) != 2 || all[0].ID != b.ID {
		t.Fatalf("List = %+v %v", all, err)
	}
	only, _ := jobs.List(ctx, pool, jobs.Filter{SourceID: &src})
	if len(only) != 1 || only[0].ID != a.ID {
		t.Fatalf("List by source = %+v", only)
	}
	byKind, _ := jobs.List(ctx, pool, jobs.Filter{Kind: jobs.KindVerify})
	if len(byKind) != 1 || byKind[0].ID != b.ID {
		t.Fatalf("List by kind = %+v", byKind)
	}
	if _, err := jobs.Get(ctx, pool, 999999); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("Get unknown = %v", err)
	}
}

// Whoever may see only some folders is shown the jobs of those folders, and the counts of the queued and running
// jobs are theirs too. A job that belongs to no folder is not theirs.
func TestListAndCountCanBeLimitedToSomeSources(t *testing.T) {
	r, pool := setup(t, 1)
	ctx := context.Background()
	a, b, c := addSource(t, pool, "a"), addSource(t, pool, "b"), addSource(t, pool, "c")
	enqueue := func(kind string, src *int64) jobs.Job {
		t.Helper()
		j, _, err := r.Enqueue(ctx, jobs.NewJob{Kind: kind, SourceID: src, RequestedBy: "tester"})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	jobA, jobB, jobC := enqueue(jobs.KindBackup, &a), enqueue(jobs.KindBackup, &b), enqueue(jobs.KindBackup, &c)
	orphan := enqueue(jobs.KindVerify, nil)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET state = 'running', started_at = now() WHERE id = $1`, jobA.ID); err != nil {
		t.Fatal(err)
	}

	ids := func(list []jobs.Job) string {
		var out []string
		for _, j := range list {
			out = append(out, fmt.Sprint(j.ID))
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct {
		name           string
		f              jobs.Filter
		list           []int64
		queued, active int
	}{
		{"everything", jobs.Filter{}, []int64{orphan.ID, jobC.ID, jobB.ID, jobA.ID}, 3, 1},
		{"a and b", jobs.Filter{OnlySources: true, SourceIDs: []int64{a, b}}, []int64{jobB.ID, jobA.ID}, 1, 1},
		{"c", jobs.Filter{OnlySources: true, SourceIDs: []int64{c}}, []int64{jobC.ID}, 1, 0},
		{"no source at all", jobs.Filter{OnlySources: true}, nil, 0, 0},
		{"an empty list", jobs.Filter{OnlySources: true, SourceIDs: []int64{}}, nil, 0, 0},
		{"a source that is not there", jobs.Filter{OnlySources: true, SourceIDs: []int64{987654}}, nil, 0, 0},
		{"b and c, backups only", jobs.Filter{OnlySources: true, SourceIDs: []int64{b, c}, Kind: jobs.KindBackup}, []int64{jobC.ID, jobB.ID}, 2, 0},
		{"a and b, verifications only", jobs.Filter{OnlySources: true, SourceIDs: []int64{a, b}, Kind: jobs.KindVerify}, nil, 0, 0},
	} {
		list, err := jobs.List(ctx, pool, tc.f)
		if err != nil {
			t.Fatalf("%s: List: %v", tc.name, err)
		}
		var want []string
		for _, id := range tc.list {
			want = append(want, fmt.Sprint(id))
		}
		if got := ids(list); got != strings.Join(want, ",") {
			t.Errorf("%s: List = %q, want %q", tc.name, got, strings.Join(want, ","))
		}
		queued, active, err := jobs.Counts(ctx, pool, tc.f)
		if err != nil || queued != tc.queued || active != tc.active {
			t.Errorf("%s: Counts = %d queued, %d running, %v; want %d and %d", tc.name, queued, active, err, tc.queued, tc.active)
		}
	}
}

// lockedBuffer lets the test read what the runner's worker goroutines logged.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestErrForgetDeletesTheJobInsteadOfRecordingAResult(t *testing.T) {
	pool := dbtest.New(t)
	logs := &lockedBuffer{}
	r := jobs.NewRunner(pool, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})), 2)
	src := addSource(t, pool, "s1")
	r.Handle(jobs.KindBackup, func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		rep.Event("info", "looked and found nothing")
		return fmt.Errorf("nothing to do: %w", jobs.ErrForget)
	})
	r.Handle(jobs.KindVerify, func(context.Context, jobs.Job, *jobs.Reporter) error { return errors.New("disk on fire") })
	r.Handle(jobs.KindRestore, func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		rep.Event("info", "kept")
		return nil
	})
	start(t, r)

	ctx := context.Background()
	forgotten, _, err := r.Enqueue(ctx, jobs.NewJob{Kind: jobs.KindBackup, SourceID: &src, RequestedBy: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	failed, _, _ := r.Enqueue(ctx, jobs.NewJob{Kind: jobs.KindVerify, RequestedBy: "a"})
	kept, _, _ := r.Enqueue(ctx, jobs.NewJob{Kind: jobs.KindRestore, RequestedBy: "a"})

	if j := wait(t, pool, failed.ID, finished); j.State != jobs.StateFailed || j.Error != "disk on fire" {
		t.Fatalf("an ordinary failure must still be recorded: %+v", j)
	}
	if j := wait(t, pool, kept.ID, finished); j.State != jobs.StateSucceeded {
		t.Fatalf("a successful job = %+v", j)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := jobs.Get(ctx, pool, forgotten.ID); errors.Is(err, jobs.ErrNotFound) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the forgotten job was not deleted")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if events, err := jobs.Events(ctx, pool, forgotten.ID, 0, 10); err != nil || len(events) != 0 {
		t.Fatalf("log lines of the forgotten job = %+v, %v", events, err)
	}
	if events, _ := jobs.Events(ctx, pool, kept.ID, 0, 10); len(events) != 1 {
		t.Fatalf("another job lost its log lines: %+v", events)
	}
	all, err := jobs.List(ctx, pool, jobs.Filter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("jobs left = %+v, %v; want the failed and the successful one", all, err)
	}
	// Nothing about it may be logged above debug level: it is an expected outcome, not a problem.
	mention := regexp.MustCompile(fmt.Sprintf(`\bjob=%d\b`, forgotten.ID))
	if mention.MatchString(logs.String()) {
		t.Fatalf("the forgotten job was logged:\n%s", logs.String())
	}
}

// A cleanup of the old versions of a folder (kind prune) and the backups, restores and verifications of that folder
// take turns: a job that has to wait stays queued, holds no worker, and starts when the other one is over. Other
// folders are not held back.
func TestACleanupOfAFolderTakesTurnsWithItsOtherJobs(t *testing.T) {
	r, pool := setup(t, 3)
	ctx := context.Background()
	src, other := addSource(t, pool, "s1"), addSource(t, pool, "s2")

	var mu sync.Mutex
	gates := map[string]chan struct{}{}
	for _, name := range []string{"backup1", "backup2", "verify1", "verify2", "restore1", "prune1", "prune2", "prune3"} {
		gates[name] = make(chan struct{})
	}
	release := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		close(gates[name])
	}
	handler := func(ctx context.Context, j jobs.Job, rep *jobs.Reporter) error {
		var p struct {
			Gate string `json:"gate"`
		}
		_ = json.Unmarshal(j.Params, &p)
		mu.Lock()
		gate := gates[p.Gate]
		mu.Unlock()
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return nil
	}
	for _, kind := range []string{jobs.KindBackup, jobs.KindVerify, jobs.KindRestore, jobs.KindPrune} {
		r.Handle(kind, handler)
	}
	start(t, r)

	enqueue := func(kind string, source int64, gate string) jobs.Job {
		t.Helper()
		j, created, err := r.Enqueue(ctx, jobs.NewJob{Kind: kind, SourceID: &source, Params: map[string]string{"gate": gate}, RequestedBy: "t"})
		if err != nil || !created {
			t.Fatalf("enqueue %s: %+v created=%v err=%v", gate, j, created, err)
		}
		return j
	}
	// stillQueued gives the workers time to take the jobs, which they would do at once if they were allowed to.
	stillQueued := func(why string, waiting ...jobs.Job) {
		t.Helper()
		time.Sleep(400 * time.Millisecond)
		for _, j := range waiting {
			if got, err := jobs.Get(ctx, pool, j.ID); err != nil || got.State != jobs.StateQueued {
				t.Fatalf("%s: job %d is %q (%v), want it to wait in the queue", why, j.ID, got.State, err)
			}
		}
	}

	// A running backup holds the cleanup of its folder back; a cleanup of another folder starts at once.
	backup := enqueue(jobs.KindBackup, src, "backup1")
	wait(t, pool, backup.ID, running)
	cleanup := enqueue(jobs.KindPrune, src, "prune1")
	elsewhere := enqueue(jobs.KindPrune, other, "prune2")
	wait(t, pool, elsewhere.ID, running)
	stillQueued("a backup of the folder is running", cleanup)
	release("prune2")
	wait(t, pool, elsewhere.ID, finished)

	// When the backup ends the cleanup starts, and then nothing else of the folder starts: not a backup, not a
	// verification, not a restore. They wait in the queue and hold no worker (two of the three are idle).
	release("backup1")
	wait(t, pool, backup.ID, finished)
	wait(t, pool, cleanup.ID, running)
	nextBackup := enqueue(jobs.KindBackup, src, "backup2")
	verify := enqueue(jobs.KindVerify, src, "verify1")
	restore := enqueue(jobs.KindRestore, src, "restore1")
	stillQueued("a cleanup of the folder is running", nextBackup, verify, restore)
	otherVerify := enqueue(jobs.KindVerify, other, "verify2")
	wait(t, pool, otherVerify.ID, running)
	release("verify2")

	// Once the cleanup ends they all go.
	release("prune1")
	wait(t, pool, cleanup.ID, finished)
	for _, j := range []jobs.Job{nextBackup, verify, restore} {
		wait(t, pool, j.ID, running)
	}
	release("backup2")
	release("verify1")
	release("restore1")
	for _, j := range []jobs.Job{nextBackup, verify, restore} {
		wait(t, pool, j.ID, finished)
	}
}
