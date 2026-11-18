package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// State is what the updater is doing.
type State string

const (
	// StateIdle: the running version is the latest one, or nothing has been checked yet.
	StateIdle State = "idle"
	// StateChecking: the manifest is being read.
	StateChecking State = "checking"
	// StateDownloading: the new program is being downloaded and examined.
	StateDownloading State = "downloading"
	// StateWaiting: a new program is ready and no job is taken any more; the service restarts when the running jobs
	// are through.
	StateWaiting State = "waiting"
	// StateApplying: the new program is being put in place.
	StateApplying State = "applying"
	// StateError: the last check, download or installation failed. The service runs on as it is.
	StateError State = "error"
)

// Status is what the Settings page and the API say about the updater.
type Status struct {
	Address   string
	Interval  time.Duration
	Current   string
	Latest    string
	State     State
	CheckedAt time.Time
	LastError string
	// Waiting is the number of jobs that run while the service waits to restart (State is StateWaiting); Since is
	// when it began to wait.
	Waiting int
	Since   time.Time
}

// Drainer is the part of the job runner an update needs: no job is taken after Drain, and Running says how many are
// still at work.
type Drainer interface {
	Drain()
	Resume()
	Running() int
}

// Event is something the audit log should know about an update.
type Event struct {
	// Stage is staged (a new program is ready), restart (it was put in place), cancelled (the release went back to
	// the running version) or failed.
	Stage    string
	OK       bool
	From, To string
	Detail   map[string]any
}

// Options configures an Updater.
type Options struct {
	Source  Source
	Checker Checker
	Drainer Drainer
	// Current is the version that runs; ExePath is the program that runs.
	Current string
	ExePath string
	// Interval is the time between two looks at the manifest.
	Interval time.Duration
	// FirstCheck is how long after Run starts the first look is made; zero means 10 seconds.
	FirstCheck time.Duration
	// Poll is how often a new program that is ready looks whether the jobs are through; zero means 2 seconds.
	Poll time.Duration
	Log  *slog.Logger
	// Record is told what happens, for the audit log. It may be nil.
	Record func(ctx context.Context, e Event)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Update is a new program that has been put in place of the running one and is to be started.
type Update struct {
	From, To string
	SHA256   string
	// Previous is where the program that ran until now was kept.
	Previous string
	// Waited is how long the service waited for its jobs.
	Waited time.Duration
}

// pending is the release the updater is working towards.
type pending struct {
	m    Manifest
	path string
	// staged: the program is on disk with the size and the hash of the manifest.
	staged bool
	// ready: it was examined and the job runner is drained.
	ready bool
	// bad is why this release is not tried again until the manifest changes: the program is not the version the
	// manifest says, or it could not be put in place.
	bad   string
	since time.Time
	// announced: the download of this release has been logged once.
	announced bool
}

// Updater watches a release manifest and replaces the running program when the version in it differs.
type Updater struct {
	o        Options
	checkNow chan struct{}

	mu          sync.Mutex
	st          Status
	ready       bool
	lastFailure string

	// pending and failed belong to the goroutine of Run.
	pending *pending
	failed  bool
}

// New makes an Updater; call Run to start it.
func New(o Options) (*Updater, error) {
	switch {
	case o.Source == nil || o.Drainer == nil:
		return nil, errors.New("update: a source and a job runner are needed")
	case o.ExePath == "" || NormalizeVersion(o.Current) == "":
		return nil, errors.New("update: the program and its version are needed")
	}
	if o.Interval <= 0 {
		o.Interval = 10 * time.Minute
	}
	if o.FirstCheck <= 0 {
		o.FirstCheck = 10 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	if o.Checker == nil {
		o.Checker = ExecChecker{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.Current = NormalizeVersion(o.Current)
	u := &Updater{o: o, checkNow: make(chan struct{}, 1)}
	u.st = Status{Address: o.Source.Address(), Interval: o.Interval, Current: o.Current, State: StateIdle}
	return u, nil
}

// Status describes what the updater is doing.
func (u *Updater) Status() Status {
	u.mu.Lock()
	st, ready := u.st, u.ready
	u.mu.Unlock()
	if ready {
		st.Waiting = u.o.Drainer.Running()
	}
	return st
}

// CheckNow asks for a look at the manifest without waiting for the next one. It does not wait for the answer.
func (u *Updater) CheckNow() {
	select {
	case u.checkNow <- struct{}{}:
	default:
	}
}

// Run looks at the manifest now and then until ctx ends or a new program has been put in place, which it returns:
// the caller stops the service and starts the new program. While a new program is ready no job is taken, so the
// running jobs are the last ones.
func (u *Updater) Run(ctx context.Context) *Update {
	u.removeLeftovers()
	timer := time.NewTimer(u.o.FirstCheck)
	defer timer.Stop()
	poll := time.NewTicker(u.o.Poll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			u.check(ctx)
			timer.Reset(u.o.Interval)
		case <-u.checkNow:
			u.check(ctx)
			timer.Reset(u.o.Interval)
		case <-poll.C:
			if p := u.pending; p != nil && p.ready && u.o.Drainer.Running() == 0 {
				if update, ok := u.apply(ctx); ok {
					return update
				}
			}
		}
	}
}

// removeLeftovers deletes what an earlier run of the service left of a download.
func (u *Updater) removeLeftovers() {
	staged := StagedPath(u.o.ExePath)
	_ = os.Remove(staged)
	_ = os.Remove(staged + ".part")
}

func (u *Updater) setState(s State) {
	u.mu.Lock()
	u.st.State = s
	u.mu.Unlock()
}

// fail records why a step did not work. The service goes on as it is; the step is tried again at the next look. The
// same text is only logged and audited once until a look goes through without a failure, so a server that stays
// unreachable does not fill the log.
func (u *Updater) fail(ctx context.Context, step string, err error) {
	if ctx.Err() != nil {
		return
	}
	u.failed = true
	msg := err.Error()
	u.mu.Lock()
	u.st.LastError = msg
	u.st.State = StateError
	if u.ready {
		u.st.State = StateWaiting
	}
	repeat := msg == u.lastFailure
	u.lastFailure = msg
	u.mu.Unlock()
	if repeat {
		u.o.Log.Debug("automatic update: still failing", "step", step, "error", msg)
		return
	}
	u.o.Log.Warn("automatic update: a step failed; the service runs on as it is", "step", step, "error", msg)
	if step != "check" {
		// The audit log is shown to everybody who signs in: it says which step failed, not the error text, which
		// may hold a path.
		u.record(ctx, Event{Stage: "failed", From: u.o.Current, To: u.target(), Detail: map[string]any{"step": step}})
	}
}

func (u *Updater) target() string {
	if u.pending != nil {
		return u.pending.m.LatestVersion
	}
	return ""
}

// recordTimeout is how long the update waits for the audit log.
var recordTimeout = 5 * time.Second

func (u *Updater) record(ctx context.Context, e Event) {
	if u.o.Record == nil {
		return
	}
	// The audit log is written even when the service is being stopped, but the update does not wait long for it: a
	// database that does not answer must not hold the restart up.
	ctx = context.WithoutCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.o.Record(ctx, e)
	}()
	select {
	case <-done:
	case <-time.After(recordTimeout):
		u.o.Log.Warn("automatic update: the audit log did not answer in time; the update goes on", "stage", e.Stage)
	}
}

// check reads the manifest and acts on what it says.
func (u *Updater) check(ctx context.Context) {
	u.failed = false
	defer func() {
		if !u.failed {
			u.mu.Lock()
			u.lastFailure = ""
			u.mu.Unlock()
		}
	}()
	u.mu.Lock()
	if !u.ready {
		u.st.State = StateChecking
	}
	u.mu.Unlock()
	m, err := u.o.Source.Manifest(ctx)
	if err != nil {
		u.fail(ctx, "check", err)
		return
	}
	u.mu.Lock()
	u.st.Latest = m.LatestVersion
	u.st.CheckedAt = u.o.Now()
	u.st.LastError = ""
	u.mu.Unlock()

	if SameVersion(m.LatestVersion, u.o.Current) {
		u.cancel(ctx)
		u.mu.Lock()
		u.st.State, u.st.LastError = StateIdle, ""
		u.mu.Unlock()
		return
	}
	if u.pending != nil && u.pending.m != m {
		u.dropPending()
	}
	if u.pending == nil {
		u.pending = &pending{m: m, path: StagedPath(u.o.ExePath)}
	}
	p := u.pending
	switch {
	case p.bad != "":
		u.fail(ctx, "program", errors.New(p.bad))
	case p.ready:
		u.setState(StateWaiting)
	default:
		u.stage(ctx, p)
	}
}

// stage downloads and examines the program of a release; when it is fine the job runner is drained.
func (u *Updater) stage(ctx context.Context, p *pending) {
	if p.staged {
		// A virus scanner may have taken the program away after it was downloaded.
		if st, err := os.Stat(p.path); err != nil || st.Size() != p.m.Size {
			p.staged = false
		}
	}
	if !p.staged {
		u.setState(StateDownloading)
		if p.announced {
			u.o.Log.Debug("automatic update: downloading the released version again", "released", p.m.LatestVersion)
		} else {
			p.announced = true
			u.o.Log.Info("automatic update: a different version is released, downloading it", "running", u.o.Current, "released", p.m.LatestVersion, "size", p.m.Size)
		}
		if err := Download(ctx, u.o.Source, p.m, p.path); err != nil {
			u.fail(ctx, "download", err)
			return
		}
		p.staged = true
	}
	version, err := u.o.Checker.Version(ctx, p.path)
	if err != nil {
		u.fail(ctx, "program", err)
		return
	}
	if !SameVersion(version, p.m.LatestVersion) {
		p.bad = fmt.Sprintf("the program of the release says it is version %s, but the manifest says %s", version, p.m.LatestVersion)
		p.staged = false
		_ = os.Remove(p.path)
		u.fail(ctx, "program", errors.New(p.bad))
		return
	}
	if err := u.o.Checker.Config(ctx, p.path); err != nil {
		u.fail(ctx, "program", err)
		return
	}
	u.o.Drainer.Drain()
	p.ready, p.since = true, u.o.Now()
	u.mu.Lock()
	u.ready = true
	u.st.State, u.st.LastError, u.st.Since = StateWaiting, "", p.since
	u.mu.Unlock()
	u.o.Log.Info("automatic update: the new version is ready; no job is taken any more, and the service restarts when the running jobs are through",
		"running", u.o.Current, "released", p.m.LatestVersion, "running_jobs", u.o.Drainer.Running())
	u.record(ctx, Event{Stage: "staged", OK: true, From: u.o.Current, To: p.m.LatestVersion,
		Detail: map[string]any{"sha256": p.m.SHA256, "size": p.m.Size}})
}

// dropPending forgets the release that was being worked on and deletes its program; when it had drained the job
// runner, the jobs are taken again.
func (u *Updater) dropPending() {
	p := u.pending
	u.pending = nil
	if p == nil {
		return
	}
	_ = os.Remove(p.path)
	_ = os.Remove(p.path + ".part")
	if p.ready {
		u.mu.Lock()
		u.ready = false
		u.st.Since = time.Time{}
		u.mu.Unlock()
		u.o.Drainer.Resume()
	}
}

// cancel ends an update that the manifest no longer asks for, because it names the running version again.
func (u *Updater) cancel(ctx context.Context) {
	p := u.pending
	if p == nil {
		return
	}
	wasReady, version := p.ready, p.m.LatestVersion
	u.dropPending()
	if wasReady {
		u.o.Log.Info("automatic update: the release is the running version again; jobs are taken again", "released", version)
		u.record(ctx, Event{Stage: "cancelled", OK: true, From: u.o.Current, To: version})
	}
}

// apply puts the new program in place. Nothing runs any more, so this is the moment; when it does not work the jobs
// are taken again and the service goes on as it is.
func (u *Updater) apply(ctx context.Context) (*Update, bool) {
	p := u.pending
	u.setState(StateApplying)
	previous, err := Swap(u.o.ExePath, p.path, u.o.Current)
	if err != nil {
		p.bad = "the new program could not be put in place: " + err.Error()
		p.ready = false
		u.mu.Lock()
		u.ready = false
		u.mu.Unlock()
		u.o.Drainer.Resume()
		u.fail(ctx, "install", errors.New(p.bad))
		return nil, false
	}
	waited := u.o.Now().Sub(p.since)
	if err := WriteMarker(u.o.ExePath, Marker{From: u.o.Current, To: p.m.LatestVersion, SHA256: p.m.SHA256, At: u.o.Now(), WaitedSeconds: int64(waited.Seconds())}); err != nil {
		u.o.Log.Warn("automatic update: the note for the new program could not be written", "error", err)
	}
	u.o.Log.Info("automatic update: the new program is in place; the service restarts", "from", u.o.Current, "to", p.m.LatestVersion,
		"waited", waited.Round(time.Second).String(), "previous_program", previous)
	u.record(ctx, Event{Stage: "restart", OK: true, From: u.o.Current, To: p.m.LatestVersion,
		Detail: map[string]any{"sha256": p.m.SHA256, "waited_seconds": int64(waited.Seconds())}})
	return &Update{From: u.o.Current, To: p.m.LatestVersion, SHA256: p.m.SHA256, Previous: previous, Waited: waited}, true
}
