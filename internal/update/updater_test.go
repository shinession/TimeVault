package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSource serves a release from memory. The program of a release is the text "timevault <version>", which is
// also what fakeChecker takes for the version the program says it is.
type fakeSource struct {
	mu        sync.Mutex
	manifest  Manifest
	program   []byte
	err       error
	manifests int
	opens     int
	// gate, when set, holds every read of the manifest until it is closed.
	gate chan struct{}
}

func (s *fakeSource) Address() string { return "fake://release/timevault.json" }

func (s *fakeSource) Manifest(ctx context.Context) (Manifest, error) {
	s.mu.Lock()
	s.manifests++
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifest, s.err
}

func (s *fakeSource) hold(gate chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = gate
}

func (s *fakeSource) Open(context.Context, Manifest) (io.ReadCloser, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens++
	return io.NopCloser(bytes.NewReader(s.program)), int64(len(s.program)), nil
}

// publish makes the manifest name a version and the program the given text, with the size and hash that fit.
func (s *fakeSource) publish(version, program string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.program = []byte(program)
	s.manifest = Manifest{LatestVersion: version, SHA256: sum(s.program), PackageURL: "timevault.exe", Size: int64(len(s.program))}
	s.err = nil
}

func (s *fakeSource) release(version string) { s.publish(version, "timevault "+version) }

// corrupt makes the program differ from the manifest, with the same size.
func (s *fakeSource) corrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.program = bytes.ToUpper(s.program)
}

func (s *fakeSource) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeSource) counts() (manifests, opens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifests, s.opens
}

type fakeChecker struct {
	mu        sync.Mutex
	configErr error
}

func (c *fakeChecker) Version(_ context.Context, program string) (string, error) {
	data, err := os.ReadFile(program)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(data), "timevault "), nil
}

func (c *fakeChecker) Config(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.configErr
}

func (c *fakeChecker) setConfigErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configErr = err
}

type fakeDrainer struct {
	mu       sync.Mutex
	running  int
	draining bool
	drains   int
	resumes  int
}

func (d *fakeDrainer) Drain() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.draining = true
	d.drains++
}

func (d *fakeDrainer) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.draining = false
	d.resumes++
}

func (d *fakeDrainer) Running() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

func (d *fakeDrainer) setRunning(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = n
}

func (d *fakeDrainer) state() (draining bool, drains, resumes int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.draining, d.drains, d.resumes
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) record(_ context.Context, e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) stages() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stages []string
	for _, e := range r.events {
		stages = append(stages, e.Stage)
	}
	return strings.Join(stages, ",")
}

func (r *recorder) all() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

type rig struct {
	t     *testing.T
	exe   string
	src   *fakeSource
	chk   *fakeChecker
	drain *fakeDrainer
	rec   *recorder
	u     *Updater
}

// newRig makes an updater for a program of version 0.1.7 that sees a release of the same version. The timers are
// long, so that a test makes the updater look by calling check itself, unless it tunes them.
func newRig(t *testing.T, tune ...func(*Options)) *rig {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "timevault.exe")
	writeFile(t, exe, []byte("timevault 0.1.7"))
	r := &rig{t: t, exe: exe, src: &fakeSource{}, chk: &fakeChecker{}, drain: &fakeDrainer{}, rec: &recorder{}}
	r.src.release("0.1.7")
	o := Options{
		Source: r.src, Checker: r.chk, Drainer: r.drain, Current: "0.1.7", ExePath: exe,
		Interval: time.Hour, FirstCheck: time.Hour, Poll: 5 * time.Millisecond,
		Record: r.rec.record,
		Now:    func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
	for _, f := range tune {
		f(&o)
	}
	u, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	r.u = u
	return r
}

func (r *rig) staged() string { return StagedPath(r.exe) }

var bg = context.Background()

func TestTheRunningVersionIsLeftAlone(t *testing.T) {
	r := newRig(t)
	r.u.check(bg)
	st := r.u.Status()
	if st.State != StateIdle || st.Latest != "0.1.7" || st.Current != "0.1.7" || st.CheckedAt.IsZero() || st.LastError != "" || st.Waiting != 0 {
		t.Fatalf("status = %+v", st)
	}
	if st.Address != "fake://release/timevault.json" {
		t.Errorf("address = %q", st.Address)
	}
	if _, opens := r.src.counts(); opens != 0 {
		t.Error("the program was downloaded")
	}
	if _, drains, _ := r.drain.state(); drains != 0 {
		t.Error("the runner was drained")
	}
	if r.rec.stages() != "" {
		t.Errorf("events: %s", r.rec.stages())
	}
}

func TestADifferentVersionIsExaminedAndTheRunnerDrained(t *testing.T) {
	r := newRig(t)
	r.drain.setRunning(2)
	r.src.release("0.1.8")
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateWaiting || st.Latest != "0.1.8" || st.Waiting != 2 || st.Since.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	if got := readFile(t, r.staged()); got != "timevault 0.1.8" {
		t.Errorf("the staged program is %q", got)
	}
	if got := readFile(t, r.exe); got != "timevault 0.1.7" {
		t.Errorf("the running program changed: %q", got)
	}
	if draining, drains, _ := r.drain.state(); !draining || drains != 1 {
		t.Errorf("draining = %v after %d drains", draining, drains)
	}
	if r.rec.stages() != "staged" {
		t.Errorf("events: %s", r.rec.stages())
	}

	r.u.check(bg) // the same release again changes nothing
	if _, opens := r.src.counts(); opens != 1 {
		t.Errorf("downloaded %d times", opens)
	}
	if _, drains, _ := r.drain.state(); drains != 1 {
		t.Errorf("drained %d times", drains)
	}
}

func TestRunRestartsWhenTheJobsAreThrough(t *testing.T) {
	r := newRig(t, func(o *Options) { o.FirstCheck = time.Millisecond })
	r.drain.setRunning(1)
	r.src.release("0.1.8")
	ctx, cancel := context.WithTimeout(bg, 20*time.Second)
	defer cancel()
	done := make(chan *Update, 1)
	go func() { done <- r.u.Run(ctx) }()

	waitFor(t, "the updater to wait", func() bool { return r.u.Status().State == StateWaiting })
	select {
	case <-done:
		t.Fatal("the service was restarted while a job still runs")
	case <-time.After(100 * time.Millisecond):
	}
	if st := r.u.Status(); st.Waiting != 1 {
		t.Errorf("waiting for %d jobs, want 1", st.Waiting)
	}

	r.drain.setRunning(0)
	var up *Update
	select {
	case up = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no restart after the last job ended")
	}
	if up == nil || up.From != "0.1.7" || up.To != "0.1.8" || up.SHA256 != sum([]byte("timevault 0.1.8")) || up.Previous != PreviousPath(r.exe, "0.1.7") {
		t.Fatalf("update = %+v", up)
	}
	if got := readFile(t, r.exe); got != "timevault 0.1.8" {
		t.Errorf("the program is %q", got)
	}
	if got := readFile(t, up.Previous); got != "timevault 0.1.7" {
		t.Errorf("the kept program is %q", got)
	}
	if exists(r.staged()) {
		t.Error("the staged program is still there")
	}
	marker, ok := TakeMarker(r.exe)
	if !ok || marker.From != "0.1.7" || marker.To != "0.1.8" {
		t.Errorf("marker = %+v, %v", marker, ok)
	}
	if r.rec.stages() != "staged,restart" {
		t.Errorf("events: %s", r.rec.stages())
	}
	if _, _, resumes := r.drain.state(); resumes != 0 {
		t.Error("jobs were taken again")
	}
}

func TestTheReleaseGoingBackToTheRunningVersionCancelsTheUpdate(t *testing.T) {
	r := newRig(t)
	r.drain.setRunning(1)
	r.src.release("0.1.8")
	r.u.check(bg)
	r.src.release("0.1.7")
	r.u.check(bg)

	if draining, _, resumes := r.drain.state(); draining || resumes != 1 {
		t.Errorf("draining = %v, resumes = %d", draining, resumes)
	}
	if exists(r.staged()) {
		t.Error("the staged program was kept")
	}
	if st := r.u.Status(); st.State != StateIdle || st.Waiting != 0 || !st.Since.IsZero() {
		t.Errorf("status = %+v", st)
	}
	if r.rec.stages() != "staged,cancelled" {
		t.Errorf("events: %s", r.rec.stages())
	}
}

func TestAnotherReleaseWhileWaitingReplacesTheStagedProgram(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.8")
	r.u.check(bg)
	r.src.release("0.1.9")
	r.u.check(bg)

	if got := readFile(t, r.staged()); got != "timevault 0.1.9" {
		t.Errorf("the staged program is %q", got)
	}
	if st := r.u.Status(); st.State != StateWaiting || st.Latest != "0.1.9" {
		t.Errorf("status = %+v", st)
	}
	if draining, drains, resumes := r.drain.state(); !draining || drains != 2 || resumes != 1 {
		t.Errorf("draining = %v, drains = %d, resumes = %d", draining, drains, resumes)
	}
}

func TestADownloadThatDoesNotMatchIsTriedAgain(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.8")
	r.src.corrupt() // a release that is still being copied
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateError || !strings.Contains(st.LastError, "not the one the manifest describes") {
		t.Fatalf("status = %+v", st)
	}
	if exists(r.staged()) || exists(r.staged()+".part") {
		t.Error("a program that does not match was kept")
	}
	if _, drains, _ := r.drain.state(); drains != 0 {
		t.Error("the runner was drained")
	}

	r.src.release("0.1.8") // the copy is complete
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAProgramOfAnotherVersionThanTheManifestIsNotTriedAgainUntilTheManifestChanges(t *testing.T) {
	r := newRig(t)
	r.src.publish("0.1.8", "timevault 0.1.9") // the manifest and the file agree, but the file is another version
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateError || !strings.Contains(st.LastError, "version 0.1.9") {
		t.Fatalf("status = %+v", st)
	}
	if exists(r.staged()) {
		t.Error("the program was kept")
	}
	_, opens := r.src.counts()
	r.u.check(bg)
	if _, again := r.src.counts(); again != opens {
		t.Error("the same release was downloaded again")
	}
	if st := r.u.Status(); st.State != StateError {
		t.Errorf("status = %+v", st)
	}
	if _, drains, _ := r.drain.state(); drains != 0 {
		t.Error("the runner was drained")
	}

	r.src.release("0.1.9")
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.Latest != "0.1.9" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAProgramThatDoesNotAcceptTheSettingsDoesNotDrainTheRunner(t *testing.T) {
	r := newRig(t)
	r.chk.setConfigErr(errors.New("the new program does not accept the settings of this service: no password"))
	r.src.release("0.1.8")
	r.u.check(bg)

	if st := r.u.Status(); st.State != StateError || !strings.Contains(st.LastError, "no password") {
		t.Fatalf("status = %+v", st)
	}
	if _, drains, _ := r.drain.state(); drains != 0 {
		t.Error("the runner was drained")
	}

	r.chk.setConfigErr(nil) // the administrator fixed the .env
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if _, opens := r.src.counts(); opens != 1 {
		t.Errorf("downloaded %d times, want once", opens)
	}
}

func TestAStagedProgramThatWasRemovedIsDownloadedAgain(t *testing.T) {
	r := newRig(t)
	r.chk.setConfigErr(errors.New("the new program does not accept the settings of this service: no password"))
	r.src.release("0.1.8")
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateError {
		t.Fatalf("status = %+v", st)
	}

	if err := os.Remove(r.staged()); err != nil { // a virus scanner took it away
		t.Fatal(err)
	}
	r.chk.setConfigErr(nil)
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if _, opens := r.src.counts(); opens != 2 {
		t.Errorf("downloaded %d times, want twice (the program was gone the second time)", opens)
	}
}

func TestAFailedInstallationTakesJobsAgainAndIsNotRepeated(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.8")
	r.u.check(bg)
	if err := os.Remove(r.staged()); err != nil { // the new program cannot be moved any more
		t.Fatal(err)
	}

	if up, ok := r.u.apply(bg); ok || up != nil {
		t.Fatalf("apply = %v, %v", up, ok)
	}
	if got := readFile(t, r.exe); got != "timevault 0.1.7" {
		t.Errorf("the program is %q, want the running one back", got)
	}
	if draining, _, resumes := r.drain.state(); draining || resumes != 1 {
		t.Errorf("draining = %v, resumes = %d", draining, resumes)
	}
	if st := r.u.Status(); st.State != StateError || !strings.Contains(st.LastError, "put in place") || st.Waiting != 0 {
		t.Fatalf("status = %+v", st)
	}
	if r.rec.stages() != "staged,failed" {
		t.Errorf("events: %s", r.rec.stages())
	}

	_, opens := r.src.counts()
	r.u.check(bg)
	if _, again := r.src.counts(); again != opens {
		t.Error("the release was downloaded again")
	}
	if _, drains, _ := r.drain.state(); drains != 1 {
		t.Errorf("the runner was drained again (%d)", drains)
	}
}

func TestAManifestThatCannotBeReadWhileWaitingKeepsWaiting(t *testing.T) {
	r := newRig(t)
	r.drain.setRunning(1)
	r.src.release("0.1.8")
	r.u.check(bg)
	r.src.fail(errors.New("the server is down"))
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateWaiting || st.LastError != "the server is down" || st.Waiting != 1 {
		t.Fatalf("status = %+v", st)
	}
	if _, _, resumes := r.drain.state(); resumes != 0 {
		t.Error("jobs were taken again")
	}
	if r.rec.stages() != "staged" {
		t.Errorf("events: %s (an unreachable server is not audited)", r.rec.stages())
	}

	r.drain.setRunning(0)
	if up, ok := r.u.apply(bg); !ok || up.To != "0.1.8" {
		t.Fatalf("apply = %v, %v", up, ok)
	}
}

func TestTheSameFailureIsAuditedOnceUntilALookGoesThrough(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.8")
	r.src.corrupt()
	for i := 0; i < 3; i++ {
		r.u.check(bg)
	}
	if got := r.rec.stages(); got != "failed" {
		t.Fatalf("events: %s", got)
	}

	r.src.release("0.1.7") // a look without a failure
	r.u.check(bg)
	r.src.release("0.1.8")
	r.src.corrupt()
	r.u.check(bg)
	if got := r.rec.stages(); got != "failed,failed" {
		t.Fatalf("events: %s", got)
	}
}

func TestCheckNowDoesNotWaitForTheInterval(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(bg)
	done := make(chan *Update, 1)
	go func() { done <- r.u.Run(ctx) }()
	r.u.CheckNow()
	r.u.CheckNow() // a second request while the first is pending is one
	waitFor(t, "the manifest to be read", func() bool { n, _ := r.src.counts(); return n >= 1 })
	cancel()
	if up := <-done; up != nil {
		t.Fatalf("update = %+v", up)
	}
}

func TestRunRemovesWhatAnEarlierRunLeftOfADownload(t *testing.T) {
	r := newRig(t)
	writeFile(t, r.staged(), []byte("left"))
	writeFile(t, r.staged()+".part", []byte("left"))
	ctx, cancel := context.WithCancel(bg)
	done := make(chan *Update, 1)
	go func() { done <- r.u.Run(ctx) }()
	waitFor(t, "the leftovers to be removed", func() bool {
		_, a := os.Stat(r.staged())
		_, b := os.Stat(r.staged() + ".part")
		return errors.Is(a, fs.ErrNotExist) && errors.Is(b, fs.ErrNotExist)
	})
	cancel()
	<-done
}

func TestTheAuditDetailsHoldNoPathAndNoErrorText(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.8")
	r.src.corrupt()
	r.u.check(bg) // a failed download
	r.src.release("0.1.8")
	r.u.check(bg) // staged
	if _, ok := r.u.apply(bg); !ok {
		t.Fatal("apply failed")
	}
	events := r.rec.all()
	if len(events) != 3 {
		t.Fatalf("events: %s", r.rec.stages())
	}
	for _, e := range events {
		for k, v := range e.Detail {
			text := strings.ToLower(k + " " + fmt.Sprint(v))
			if strings.Contains(text, strings.ToLower(filepath.Dir(r.exe))) || strings.Contains(text, "error") || strings.Contains(text, "previous") {
				t.Errorf("the %s event has %s=%v", e.Stage, k, v)
			}
		}
	}
	if got := events[0].Detail["step"]; got != "download" {
		t.Errorf("the failure names the step %v", got)
	}
}

func TestAnAuditLogThatDoesNotAnswerDoesNotHoldTheUpdateUp(t *testing.T) {
	old := recordTimeout
	recordTimeout = 50 * time.Millisecond
	t.Cleanup(func() { recordTimeout = old })
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	r := newRig(t, func(o *Options) { o.Record = func(context.Context, Event) { <-hang } })
	r.src.release("0.1.8")

	started := time.Now()
	r.u.check(bg)
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the check took %v", took)
	}
	if st := r.u.Status(); st.State != StateWaiting {
		t.Fatalf("status = %+v", st)
	}
}

func TestWaitingIsNotHiddenWhileTheManifestIsRead(t *testing.T) {
	r := newRig(t)
	r.drain.setRunning(1)
	r.src.release("0.1.8")
	r.u.check(bg)

	gate := make(chan struct{})
	r.src.hold(gate)
	done := make(chan struct{})
	go func() {
		r.u.check(bg)
		close(done)
	}()
	waitFor(t, "the manifest to be asked for again", func() bool { n, _ := r.src.counts(); return n >= 2 })
	if st := r.u.Status(); st.State != StateWaiting || st.Waiting != 1 {
		t.Errorf("status while the manifest is read = %+v", st)
	}
	close(gate)
	<-done
}

func TestNewNeedsWhatItWorksWith(t *testing.T) {
	good := Options{Source: &fakeSource{}, Drainer: &fakeDrainer{}, Current: "0.1.7", ExePath: "timevault.exe"}
	if _, err := New(good); err != nil {
		t.Fatalf("New: %v", err)
	}
	for name, tune := range map[string]func(*Options){
		"no source":  func(o *Options) { o.Source = nil },
		"no drainer": func(o *Options) { o.Drainer = nil },
		"no program": func(o *Options) { o.ExePath = "" },
		"no version": func(o *Options) { o.Current = " " },
	} {
		o := good
		tune(&o)
		if _, err := New(o); err == nil {
			t.Errorf("%s: New worked", name)
		}
	}
}
