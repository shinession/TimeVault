// Package jobs is a small durable job queue stored in PostgreSQL.
//
// Jobs survive restarts: a job that was running when the process died is
// marked failed on the next start (its work is rolled back by the handler's own
// transactional design), while queued jobs simply run once the service is up.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job kinds.
const (
	KindBackup  = "backup"
	KindVerify  = "verify"
	KindRestore = "restore"
	KindPrune   = "prune"
)

// Job states.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCanceled  = "canceled"
)

const (
	pollInterval    = 2 * time.Second
	progressEvery   = 500 * time.Millisecond
	maxEventsPerJob = 2000
	maxErrorLength  = 1000
)

// ErrNotFound means the job does not exist.
var ErrNotFound = errors.New("jobs: not found")

// ErrForget is returned by a handler whose job turned out to have nothing worth
// recording, such as a scheduled backup that found no change. The runner then
// deletes the job instead of recording a result.
var ErrForget = errors.New("jobs: nothing worth recording")

// Job is one unit of background work.
type Job struct {
	ID             int64
	Kind           string
	SourceID       *int64
	PointID        *int64
	State          string
	Phase          string
	Params         json.RawMessage
	Progress       json.RawMessage
	Error          string
	RequestedBy    string
	IdempotencyKey string
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

// Active reports whether the job has not finished.
func (j Job) Active() bool { return j.State == StateQueued || j.State == StateRunning }

// NewJob describes a job to enqueue.
type NewJob struct {
	Kind           string
	SourceID       *int64
	PointID        *int64
	Params         any
	RequestedBy    string
	IdempotencyKey string
}

// Event is one line of a job's log.
type Event struct {
	ID      int64
	At      time.Time
	Level   string
	Message string
}

// Handler runs one job. A nil return marks it succeeded; ErrForget deletes it.
type Handler func(ctx context.Context, job Job, rep *Reporter) error

const jobColumns = `id, kind, source_id, point_id, state, phase, params, progress, error, requested_by,
	idempotency_key, created_at, started_at, finished_at`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	var errText, idem *string
	err := row.Scan(&j.ID, &j.Kind, &j.SourceID, &j.PointID, &j.State, &j.Phase, &j.Params, &j.Progress, &errText,
		&j.RequestedBy, &idem, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	if errText != nil {
		j.Error = *errText
	}
	if idem != nil {
		j.IdempotencyKey = *idem
	}
	return j, err
}

// Runner claims queued jobs and runs them on a fixed number of workers.
type Runner struct {
	pool     *pgxpool.Pool
	log      *slog.Logger
	workers  int
	handlers map[string]Handler
	wake     chan struct{}

	mu       sync.Mutex
	running  map[int64]context.CancelFunc
	canceled map[int64]bool
	// draining stops claim from taking another job; see Drain.
	draining bool

	wg   sync.WaitGroup
	stop context.CancelFunc
}

// NewRunner creates a runner; register handlers before Start.
func NewRunner(pool *pgxpool.Pool, log *slog.Logger, workers int) *Runner {
	if workers < 1 {
		workers = 1
	}
	return &Runner{
		pool: pool, log: log, workers: workers,
		handlers: map[string]Handler{}, wake: make(chan struct{}, 1),
		running: map[int64]context.CancelFunc{}, canceled: map[int64]bool{},
	}
}

// Handle registers the handler of a job kind.
func (r *Runner) Handle(kind string, h Handler) { r.handlers[kind] = h }

// Enqueue adds a job. For backups, verifications and prunes an already active job for
// the same source or point is returned instead (created is then false); a
// repeated idempotency key returns the original job.
func (r *Runner) Enqueue(ctx context.Context, nj NewJob) (job Job, created bool, err error) {
	params, err := json.Marshal(nj.Params)
	if err != nil {
		return Job{}, false, fmt.Errorf("jobs: encode params: %w", err)
	}
	var idem any
	if nj.IdempotencyKey != "" {
		idem = nj.IdempotencyKey
	}
	job, err = scanJob(r.pool.QueryRow(ctx,
		`INSERT INTO jobs (kind, source_id, point_id, state, params, requested_by, idempotency_key)
		 VALUES ($1, $2, $3, 'queued', $4, $5, $6)
		 RETURNING `+jobColumns, nj.Kind, nj.SourceID, nj.PointID, params, nj.RequestedBy, idem))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			existing, findErr := r.findDuplicate(ctx, nj)
			if findErr != nil {
				return Job{}, false, findErr
			}
			return existing, false, nil
		}
		return Job{}, false, fmt.Errorf("jobs: enqueue: %w", err)
	}
	r.notify()
	return job, true, nil
}

func (r *Runner) findDuplicate(ctx context.Context, nj NewJob) (Job, error) {
	if nj.IdempotencyKey != "" {
		j, err := scanJob(r.pool.QueryRow(ctx,
			`SELECT `+jobColumns+` FROM jobs WHERE kind = $1 AND idempotency_key = $2`, nj.Kind, nj.IdempotencyKey))
		if err == nil {
			return j, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Job{}, err
		}
	}
	j, err := scanJob(r.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs
		 WHERE kind = $1 AND state IN ('queued', 'running')
		   AND source_id IS NOT DISTINCT FROM $2 AND point_id IS NOT DISTINCT FROM $3
		 ORDER BY id DESC LIMIT 1`, nj.Kind, nj.SourceID, nj.PointID))
	if err != nil {
		return Job{}, fmt.Errorf("jobs: find existing job: %w", err)
	}
	return j, nil
}

func (r *Runner) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// FindByKey returns the job of a kind that was created with an idempotency key.
func (r *Runner) FindByKey(ctx context.Context, kind, key string) (Job, bool, error) {
	j, err := scanJob(r.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE kind = $1 AND idempotency_key = $2`, kind, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("jobs: find by key: %w", err)
	}
	return j, true, nil
}

// RecoverInterrupted marks jobs that were running when the previous process
// ended as failed. Call it once at startup, before Start.
func (r *Runner) RecoverInterrupted(ctx context.Context) (int, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE jobs SET state = 'failed', error = 'interrupted by a service restart', finished_at = now()
		 WHERE state = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("jobs: recover interrupted: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Start launches the workers. They stop when ctx ends or Shutdown is called.
func (r *Runner) Start(ctx context.Context) {
	ctx, r.stop = context.WithCancel(ctx)
	for i := 0; i < r.workers; i++ {
		r.wg.Add(1)
		go r.worker(ctx)
	}
	r.notify()
}

// Shutdown cancels running jobs and waits for the workers, at most until ctx ends.
func (r *Runner) Shutdown(ctx context.Context) {
	if r.stop == nil {
		return
	}
	r.stop()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		r.log.Warn("jobs did not stop before the shutdown deadline")
	}
}

// Cancel asks a running job to stop, or cancels a queued one. It reports
// whether the job was still active.
func (r *Runner) Cancel(ctx context.Context, id int64) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE jobs SET state = 'canceled', finished_at = now() WHERE id = $1 AND state = 'queued'`, id)
	if err != nil {
		return false, fmt.Errorf("jobs: cancel: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel, ok := r.running[id]
	if !ok {
		return false, nil
	}
	r.canceled[id] = true
	cancel()
	return true, nil
}

func (r *Runner) worker(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		for {
			job, jobCtx, cancel, ok, err := r.claim(ctx)
			if err != nil {
				if ctx.Err() == nil {
					r.log.Error("claim job", "error", err)
				}
				break
			}
			if !ok {
				break
			}
			r.run(ctx, jobCtx, cancel, job)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

// Drain stops the runner from taking another job. The jobs that run go on until they end, and the queued ones wait in
// the database (also the ones that are enqueued from now on), so they run after the service has restarted or after
// Resume. It is for a restart that waits until the service is idle: once Drain has returned, Running can only go
// down, because a claim that was under way has finished registering its job by then.
func (r *Runner) Drain() {
	r.mu.Lock()
	r.draining = true
	r.mu.Unlock()
}

// Resume undoes Drain: the queued jobs are taken again.
func (r *Runner) Resume() {
	r.mu.Lock()
	r.draining = false
	r.mu.Unlock()
	r.notify()
}

// Running is the number of jobs this runner is working on at the moment. A job counts until its result is stored.
func (r *Runner) Running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

// claim takes the next runnable job. Its cancel function is registered before
// the lock is released, so Cancel can always stop a job the database already
// reports as running.
//
// A job that cannot run yet stays queued, so it holds no worker: a backup waits for the backup of its source that is
// running, and the deletion of the old versions of a folder (kind prune) takes turns with every other job of that
// folder, so it is not claimed while a backup, a restore or a verification of the folder runs and none of them is
// claimed while it runs. Claims are serialised by r.mu, so two workers cannot start a cleanup and a backup of one
// folder at the same moment.
func (r *Runner) claim(parent context.Context) (Job, context.Context, context.CancelFunc, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.draining {
		return Job{}, nil, nil, false, nil
	}
	j, err := scanJob(r.pool.QueryRow(parent,
		`UPDATE jobs SET state = 'running', started_at = now()
		 WHERE id = (
		     SELECT q.id FROM jobs q
		     WHERE q.state = 'queued'
		       AND (q.kind <> 'backup' OR NOT EXISTS (
		            SELECT 1 FROM jobs b WHERE b.kind = 'backup' AND b.state = 'running' AND b.source_id = q.source_id))
		       AND NOT EXISTS (
		            SELECT 1 FROM jobs b
		            WHERE b.state = 'running' AND b.source_id = q.source_id
		              AND ((q.kind = 'prune' AND b.kind IN ('backup', 'verify', 'restore'))
		                OR (q.kind IN ('backup', 'verify', 'restore') AND b.kind = 'prune')))
		     ORDER BY q.id FOR UPDATE SKIP LOCKED LIMIT 1)
		 RETURNING `+jobColumns))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, nil, nil, false, nil
	}
	if err != nil {
		return Job{}, nil, nil, false, err
	}
	ctx, cancel := context.WithCancel(parent)
	r.running[j.ID] = cancel
	return j, ctx, cancel, true, nil
}

func (r *Runner) run(parent, ctx context.Context, cancel context.CancelFunc, job Job) {
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.running, job.ID)
		delete(r.canceled, job.ID)
		r.mu.Unlock()
	}()

	rep := &Reporter{pool: r.pool, jobID: job.ID, log: r.log}
	handler, ok := r.handlers[job.Kind]
	var runErr error
	if !ok {
		runErr = fmt.Errorf("no handler for job kind %q", job.Kind)
	} else {
		func() {
			defer func() {
				if p := recover(); p != nil {
					runErr = fmt.Errorf("job panicked: %v", p)
					r.log.Error("job panicked", "job", job.ID, "kind", job.Kind, "panic", p)
				}
			}()
			runErr = handler(ctx, job, rep)
		}()
	}
	rep.Flush()

	if errors.Is(runErr, ErrForget) {
		if r.forget(parent, job) {
			return
		}
		// The row could not be deleted; the work itself did succeed.
		runErr = nil
	}

	state, msg := StateSucceeded, ""
	if runErr != nil {
		state, msg = StateFailed, runErr.Error()
		r.mu.Lock()
		byUser := r.canceled[job.ID]
		r.mu.Unlock()
		if byUser && errors.Is(runErr, context.Canceled) {
			state, msg = StateCanceled, "canceled"
		} else if parent.Err() != nil {
			msg = "interrupted because the service was stopping: " + msg
		}
		if len(msg) > maxErrorLength {
			msg = msg[:maxErrorLength]
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer finishCancel()
	var errArg any
	if msg != "" {
		errArg = msg
	}
	if _, err := r.pool.Exec(finishCtx,
		`UPDATE jobs SET state = $2, error = $3, finished_at = now() WHERE id = $1`, job.ID, state, errArg); err != nil {
		r.log.Error("record job result", "job", job.ID, "error", err)
	}
	if runErr != nil {
		r.log.Warn("job finished with an error", "job", job.ID, "kind", job.Kind, "state", state, "by", job.RequestedBy, "error", msg)
	} else {
		r.log.Info("job finished", "job", job.ID, "kind", job.Kind, "by", job.RequestedBy)
	}
}

// forget deletes a job that has nothing worth recording, together with its log
// lines and stored restore items. It reports whether the job is gone.
func (r *Runner) forget(parent context.Context, job Job) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	if _, err := r.pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, job.ID); err != nil {
		r.log.Error("delete job without result", "job", job.ID, "kind", job.Kind, "error", err)
		return false
	}
	r.log.Debug("job dropped, nothing worth recording", "job", job.ID, "kind", job.Kind)
	return true
}

// Reporter lets a handler publish phase, progress and log lines of its job.
type Reporter struct {
	pool  *pgxpool.Pool
	jobID int64
	log   *slog.Logger

	mu        sync.Mutex
	lastWrite time.Time
	pending   []byte
	events    int
}

// SetPhase records the current phase immediately.
func (r *Reporter) SetPhase(phase string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.pool.Exec(ctx, `UPDATE jobs SET phase = $2 WHERE id = $1`, r.jobID, phase); err != nil {
		r.log.Warn("record job phase", "job", r.jobID, "error", err)
	}
}

// SetProgress records a progress snapshot, at most twice per second.
func (r *Reporter) SetProgress(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.pending = b
	due := time.Since(r.lastWrite) >= progressEvery
	r.mu.Unlock()
	if due {
		r.Flush()
	}
}

// Flush writes the latest progress snapshot if it has not been written yet.
func (r *Reporter) Flush() {
	r.mu.Lock()
	b := r.pending
	r.pending = nil
	r.lastWrite = time.Now()
	r.mu.Unlock()
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.pool.Exec(ctx, `UPDATE jobs SET progress = $2 WHERE id = $1`, r.jobID, b); err != nil {
		r.log.Warn("record job progress", "job", r.jobID, "error", err)
	}
}

// Event appends a line to the job log. Beyond a cap further lines are dropped.
func (r *Reporter) Event(level, message string) {
	r.mu.Lock()
	r.events++
	over := r.events > maxEventsPerJob
	r.mu.Unlock()
	if over {
		return
	}
	if level != "warn" && level != "error" {
		level = "info"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.pool.Exec(ctx, `INSERT INTO job_events (job_id, level, message) VALUES ($1, $2, $3)`, r.jobID, level, message); err != nil {
		r.log.Warn("record job event", "job", r.jobID, "error", err)
	}
}

// Get returns one job.
func Get(ctx context.Context, pool *pgxpool.Pool, id int64) (Job, error) {
	j, err := scanJob(pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: get: %w", err)
	}
	return j, nil
}

// Filter narrows List.
type Filter struct {
	SourceID *int64
	Kind     string
	// OnlySources limits the list to the jobs of the sources in SourceIDs, none at all when that is empty. Jobs
	// that belong to no source are left out too.
	OnlySources bool
	SourceIDs   []int64
	Limit       int
}

// Counts says how many jobs are queued and how many are running, among those that Filter selects (its Kind, SourceID
// and OnlySources/SourceIDs; Limit does not apply).
func Counts(ctx context.Context, pool *pgxpool.Pool, f Filter) (queued, running int, err error) {
	if f.SourceIDs == nil {
		f.SourceIDs = []int64{}
	}
	err = pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE state = 'queued'), count(*) FILTER (WHERE state = 'running') FROM jobs
		 WHERE ($1::bigint IS NULL OR source_id = $1) AND ($2 = '' OR kind = $2)
		   AND (NOT $3::boolean OR source_id = ANY($4::bigint[]))`, f.SourceID, f.Kind, f.OnlySources, f.SourceIDs).Scan(&queued, &running)
	if err != nil {
		return 0, 0, fmt.Errorf("jobs: count: %w", err)
	}
	return queued, running, nil
}

// List returns recent jobs, newest first.
func List(ctx context.Context, pool *pgxpool.Pool, f Filter) ([]Job, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 30
	}
	if f.SourceIDs == nil {
		f.SourceIDs = []int64{}
	}
	rows, err := pool.Query(ctx,
		`SELECT `+jobColumns+` FROM jobs
		 WHERE ($1::bigint IS NULL OR source_id = $1) AND ($2 = '' OR kind = $2)
		   AND (NOT $4::boolean OR source_id = ANY($5::bigint[]))
		 ORDER BY id DESC LIMIT $3`, f.SourceID, f.Kind, f.Limit, f.OnlySources, f.SourceIDs)
	if err != nil {
		return nil, fmt.Errorf("jobs: list: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) { return scanJob(r) })
	if err != nil {
		return nil, fmt.Errorf("jobs: list: %w", err)
	}
	return out, nil
}

// Events returns a job's log lines with an id greater than afterID.
func Events(ctx context.Context, pool *pgxpool.Pool, jobID, afterID int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := pool.Query(ctx,
		`SELECT id, at, level, message FROM job_events WHERE job_id = $1 AND id > $2 ORDER BY id LIMIT $3`, jobID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("jobs: events: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		err := r.Scan(&e.ID, &e.At, &e.Level, &e.Message)
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: events: %w", err)
	}
	return out, nil
}
