package weblog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"timevault/internal/audit"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func logger(buf *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func closeWriter(t *testing.T, w *Writer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// at is a time in the local zone, which is the zone the lines are written in.
func at(sec, micro int) time.Time {
	return time.Date(2026, 11, 18, 13, 58, sec, micro*1000, time.Local)
}

func TestLineHasSixFieldsLikeTheLinesOfTheOtherApplications(t *testing.T) {
	w := New(nil, Options{Host: "SGMESLT595"})
	defer closeWriter(t, w)
	got := w.Line(Entry{At: at(6, 446524), IP: "10.135.36.89", Message: "TimeVault: /explorer user=E1 0.112(s)", Level: slog.LevelInfo})
	want := "10.135.36.89 | 2026-11-18 13:58:06.446524 | TimeVault: /explorer user=E1 0.112(s) | 200 | info | SGMESLT595"
	if got != want {
		t.Errorf("line =\n%q\nwant\n%q", got, want)
	}
}

func TestLineFields(t *testing.T) {
	w := New(nil, Options{Host: "h"})
	defer closeWriter(t, w)
	const stamp = "2026-11-18 13:58:06.000001"
	cases := []struct {
		name string
		e    Entry
		want string
	}{
		{"no address is the service itself", Entry{Message: "m"}, "127.0.0.1 | " + stamp + " | m | 200 | info | h"},
		{"IPv6 loopback", Entry{IP: "::1", Message: "m"}, "::1 | " + stamp + " | m | 200 | info | h"},
		{"mapped IPv4 is written as IPv4", Entry{IP: "::ffff:10.1.2.3", Message: "m"}, "10.1.2.3 | " + stamp + " | m | 200 | info | h"},
		{"a zone is dropped", Entry{IP: "fe80::1%eth0", Message: "m"}, "fe80::1 | " + stamp + " | m | 200 | info | h"},
		{"text that is no address", Entry{IP: "evil | 200", Message: "m"}, "- | " + stamp + " | m | 200 | info | h"},
		{"a bar in the message cannot start another field", Entry{Message: "a | b"}, "127.0.0.1 | " + stamp + " | a / b | 200 | info | h"},
		{"line ends and controls become spaces", Entry{Message: "a\r\nb\x00c"}, "127.0.0.1 | " + stamp + " | a  b c | 200 | info | h"},
		{"status and level", Entry{Message: "m", Status: 404, Level: slog.LevelWarn}, "127.0.0.1 | " + stamp + " | m | 404 | warning | h"},
	}
	for _, c := range cases {
		c.e.At = at(6, 1)
		if got := w.Line(c.e); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestLevelNames(t *testing.T) {
	for l, want := range map[slog.Level]string{slog.LevelDebug: "debug", slog.LevelInfo: "info", slog.LevelWarn: "warning", slog.LevelError: "error"} {
		if got := levelName(l); got != want {
			t.Errorf("levelName(%v) = %q, want %q", l, got, want)
		}
	}
}

func TestAMessageIsCutAtTheLimit(t *testing.T) {
	long := strings.Repeat("\u6587", 2*maxMessage)
	got := cleanText(long)
	if r := []rune(got); len(r) != maxMessage+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("got %d runes (%q...), want %d and an ellipsis", len(r), string(r[:5]), maxMessage+3)
	}
}

func TestTheHostIsTheNameOfTheComputerInLowerCaseUnlessSet(t *testing.T) {
	w := New(nil, Options{})
	defer closeWriter(t, w)
	if h := w.Host(); h == "" || h != strings.ToLower(h) {
		t.Errorf("host = %q, want a name in lower case", h)
	}
	if h := cleanHost("bad host!/name"); h != "badhostname" {
		t.Errorf("cleanHost = %q", h)
	}
	if h := cleanHost("???"); h != "unknown" {
		t.Errorf("cleanHost of nothing = %q", h)
	}
}

func TestWriterAppendsEncodedLinesToTheList(t *testing.T) {
	mr, rdb := newRedis(t)
	w := New(rdb, Options{Host: "host1", MinLevel: slog.LevelInfo})
	for i, msg := range []string{"first", "second", "third"} {
		w.Write(Entry{At: at(6, i), IP: "10.0.0.1", Message: msg, Level: slog.LevelInfo})
	}
	closeWriter(t, w)

	list, err := mr.List(DefaultKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("%d elements in %s, want 3", len(list), DefaultKey)
	}
	for i, want := range []string{"first", "second", "third"} {
		line := unpickle(t, []byte(list[i]))
		wantLine := "10.0.0.1 | 2026-11-18 13:58:06.00000" + string(rune('0'+i)) + " | " + want + " | 200 | info | host1"
		if line != wantLine {
			t.Errorf("element %d = %q, want %q", i, line, wantLine)
		}
	}
	if written, dropped := w.Stats(); written != 3 || dropped != 0 {
		t.Errorf("written %d, dropped %d, want 3 and 0", written, dropped)
	}
}

func TestWriterUsesTheKeyItWasGiven(t *testing.T) {
	mr, rdb := newRedis(t)
	w := New(rdb, Options{Key: "tv_test_log", Host: "h"})
	w.Write(Entry{Message: "x"})
	closeWriter(t, w)
	if list, _ := mr.List("tv_test_log"); len(list) != 1 {
		t.Errorf("%d elements under tv_test_log, want 1", len(list))
	}
	if mr.Exists(DefaultKey) {
		t.Errorf("%s was written although another key was set", DefaultKey)
	}
}

func TestLinesBelowTheMinimumLevelAreNotWritten(t *testing.T) {
	mr, rdb := newRedis(t)
	w := New(rdb, Options{Host: "h", MinLevel: slog.LevelInfo})
	w.Write(Entry{Message: "debug line", Level: slog.LevelDebug})
	w.Write(Entry{Message: "info line", Level: slog.LevelInfo})
	closeWriter(t, w)
	list, _ := mr.List(DefaultKey)
	if len(list) != 1 || !strings.Contains(unpickle(t, []byte(list[0])), "info line") {
		t.Errorf("list = %d elements, want only the info line", len(list))
	}

	mr2, rdb2 := newRedis(t)
	w2 := New(rdb2, Options{Host: "h", MinLevel: slog.LevelDebug})
	w2.Write(Entry{Message: "debug line", Level: slog.LevelDebug})
	closeWriter(t, w2)
	if list, _ := mr2.List(DefaultKey); len(list) != 1 {
		t.Errorf("with the debug level the debug line is written; got %d elements", len(list))
	}
}

func TestPagesAreLimitedTighterThanEvents(t *testing.T) {
	_, rdb := newRedis(t)
	now := at(0, 0)
	clock := func() time.Time { return now }
	w := New(rdb, Options{Host: "h", Now: clock, pageRate: 1, pageBurst: 3, eventRate: 1, eventBurst: 5})
	for range 10 {
		w.Write(Entry{Message: "page", Class: ClassPage})
	}
	for range 10 {
		w.Write(Entry{Message: "event", Class: ClassEvent})
	}
	closeWriter(t, w)
	written, dropped := w.Stats()
	if written != 3+5 || dropped != 7+5 {
		t.Errorf("written %d, dropped %d; want 8 and 12 (3 of 10 pages and 5 of 10 events pass)", written, dropped)
	}
}

func TestTheLimitRefillsWithTime(t *testing.T) {
	b := newBucket(2, 2, at(0, 0))
	if !b.allow(at(0, 0)) || !b.allow(at(0, 0)) || b.allow(at(0, 0)) {
		t.Fatal("a burst of 2 lets exactly 2 through")
	}
	if !b.allow(at(1, 0)) || !b.allow(at(1, 0)) || b.allow(at(1, 0)) {
		t.Error("after a second 2 more tokens are back, no more")
	}
}

func TestWriteDoesNotWaitWhenTheQueueIsFull(t *testing.T) {
	release := make(chan struct{})
	p := &fakePusher{block: release}
	w := New(p, Options{Host: "h", queue: 2})
	done := make(chan struct{})
	go func() {
		for range 50 {
			w.Write(Entry{Message: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Write waited for a worker that waits for Redis")
	}
	if _, dropped := w.Stats(); dropped == 0 {
		t.Error("lines that found no room were not counted as dropped")
	}
	close(release)
	closeWriter(t, w)
}

func TestARedisThatFailsIsLeftAloneAndReportedOnce(t *testing.T) {
	buf := &syncBuffer{}
	p := &fakePusher{err: errors.New("dial tcp 10.0.0.1:6379: connection refused, password s3cr3t")}
	mask := func(err error) error { return errors.New(strings.ReplaceAll(err.Error(), "s3cr3t", "***")) }
	w := New(p, Options{Host: "h", Log: logger(buf), Redact: mask, minBackoff: time.Hour, maxBackoff: time.Hour})
	for range 5 {
		w.Write(Entry{Message: "x"})
		waitFor(t, "the worker to take the line", func() bool { _, d := w.Stats(); return d > 0 && w.queueEmpty() })
	}
	closeWriter(t, w)

	if calls := p.calls.Load(); calls != 1 {
		t.Errorf("Redis was tried %d times, want 1 (the others fall into the wait)", calls)
	}
	logged := buf.String()
	if n := strings.Count(logged, "cannot be written to Redis"); n != 1 {
		t.Errorf("the failure was logged %d times, want 1:\n%s", n, logged)
	}
	if strings.Contains(logged, "s3cr3t") || !strings.Contains(logged, "***") {
		t.Errorf("the password was not masked in the log:\n%s", logged)
	}
	if written, dropped := w.Stats(); written != 0 || dropped != 5 {
		t.Errorf("written %d, dropped %d, want 0 and 5", written, dropped)
	}
}

func TestWritingStartsAgainWhenRedisAnswers(t *testing.T) {
	buf := &syncBuffer{}
	p := &fakePusher{err: errors.New("connection refused")}
	w := New(p, Options{Host: "h", Log: logger(buf), minBackoff: 20 * time.Millisecond, maxBackoff: 20 * time.Millisecond})
	w.Write(Entry{Message: "lost"})
	waitFor(t, "the first line to fail", func() bool { _, d := w.Stats(); return d == 1 })

	p.setErr(nil)
	time.Sleep(60 * time.Millisecond) // longer than the wait
	w.Write(Entry{Message: "kept"})
	waitFor(t, "the line to be written", func() bool { written, _ := w.Stats(); return written == 1 })
	closeWriter(t, w)
	if !strings.Contains(buf.String(), "takes the lines again") {
		t.Errorf("recovery was not logged:\n%s", buf.String())
	}
	if got := p.values(); len(got) != 1 || !strings.Contains(unpickle(t, got[0]), "kept") {
		t.Errorf("sent %d lines, want only the one written after Redis answered", len(got))
	}
}

func TestCloseSendsWhatIsQueued(t *testing.T) {
	mr, rdb := newRedis(t)
	w := New(rdb, Options{Host: "h", eventRate: 1000, eventBurst: 1000})
	for range 250 { // more than one batch
		w.Write(Entry{Message: "x"})
	}
	closeWriter(t, w)
	if list, _ := mr.List(DefaultKey); len(list) != 250 {
		t.Errorf("%d lines in Redis, want 250", len(list))
	}
	w.Write(Entry{Message: "after close"})
	if _, dropped := w.Stats(); dropped != 0 {
		t.Errorf("a line after Close was counted as dropped (%d); it is ignored", dropped)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Errorf("a second Close = %v", err)
	}
}

func TestCloseGivesUpWhenRedisDoesNotAnswerInTime(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	w := New(&fakePusher{block: release}, Options{Host: "h"})
	w.Write(Entry{Message: "x"})
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := w.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close = %v, want the deadline", err)
	}
}

func TestANilWriterDropsEverything(t *testing.T) {
	var w *Writer
	w.Write(Entry{Message: "x"})
	if err := w.Close(context.Background()); err != nil {
		t.Errorf("Close = %v", err)
	}
	var sink Sink = w // the program keeps a nil *Writer when the web log is off
	sink.Write(Entry{})
}

func TestAuditEventsBecomeLines(t *testing.T) {
	src := int64(3)
	cases := []struct {
		name   string
		e      audit.Event
		msg    string
		status int
		level  slog.Level
		ip     string
	}{
		{
			"a backup that somebody asked for",
			audit.Event{Actor: "E123456", Action: audit.ActionBackupRequest, TargetType: "source", TargetID: "3", SourceID: &src, RemoteAddr: "10.135.36.89", Detail: map[string]any{"rehash": true}},
			"TimeVault: backup.request source=3 user=E123456", 200, slog.LevelInfo, "10.135.36.89",
		},
		{
			"a scheduled backup that finished",
			audit.Event{Actor: "scheduler", Action: audit.ActionBackupFinish, TargetType: "point", TargetID: "17", SourceID: &src, Duration: 12400 * time.Millisecond},
			"TimeVault: backup.finish source=3 point=17 user=scheduler 12.400(s)", 200, slog.LevelInfo, "",
		},
		{
			"a backup that failed",
			audit.Event{Actor: "scheduler", Action: audit.ActionBackupFinish, TargetType: "source", TargetID: "3", SourceID: &src, Result: audit.ResultFailed, Detail: map[string]any{"error": `D:\secret\folder is gone`}},
			"TimeVault: backup.finish source=3 user=scheduler", 500, slog.LevelError, "",
		},
		{
			"a refusal names the route, never the path",
			audit.Event{Action: audit.ActionAccessDenied, Result: audit.ResultDenied, RemoteAddr: "10.0.0.9", Detail: map[string]any{"method": "POST", "path": "/restore/start", "reason": "forbidden"}},
			"TimeVault: access.denied method=POST route=/restore/start user=guest", 403, slog.LevelWarn, "10.0.0.9",
		},
		{
			"the name typed at a failed sign-in is not copied",
			audit.Event{Actor: "my-password-1", Action: audit.ActionLoginFailed, Result: audit.ResultDenied, RemoteAddr: "10.0.0.9", Detail: map[string]any{"reason": "invalid"}},
			"TimeVault: auth.login_failed user=-", 403, slog.LevelWarn, "10.0.0.9",
		},
		{
			"a sign-in",
			audit.Event{Actor: "E123456", Action: audit.ActionLogin, RemoteAddr: "10.0.0.9", Detail: map[string]any{"method": "password", "role": "user"}},
			"TimeVault: auth.login user=E123456", 200, slog.LevelInfo, "10.0.0.9",
		},
		{
			"a step of a program update",
			audit.Event{Actor: audit.ActorUpdater, Action: audit.ActionAppUpdate, TargetType: "app", TargetID: "1.0.1", Detail: map[string]any{"stage": "staged", "from": "1.0.0", "to": "1.0.1", "sha256": "abc"}},
			"TimeVault: app.update stage=staged from=1.0.0 to=1.0.1 app=1.0.1 user=updater", 200, slog.LevelInfo, "",
		},
		{
			"a cleanup of the whole repository has no folder",
			audit.Event{Actor: "scheduler", Action: audit.ActionRetentionPrune, TargetType: "repository"},
			"TimeVault: retention.prune user=scheduler", 200, slog.LevelInfo, "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Audit(c.e)
			if got.Message != c.msg || got.Status != c.status || got.Level != c.level || got.IP != c.ip || got.Class != ClassEvent {
				t.Errorf("Audit() = {%q %d %v %q class %d}\nwant      {%q %d %v %q class %d}",
					got.Message, got.Status, got.Level, got.IP, got.Class, c.msg, c.status, c.level, c.ip, ClassEvent)
			}
		})
	}
}

func TestPageFailureAndSchedulerLines(t *testing.T) {
	p := Page("10.0.0.1", "E1", "/explorer", 200, 112*time.Millisecond)
	if p.Message != "TimeVault: /explorer user=E1 0.112(s)" || p.Level != slog.LevelInfo || p.Class != ClassPage || p.Status != 200 {
		t.Errorf("Page = %+v", p)
	}
	if g := Page("", "", "/login", 404, time.Millisecond); g.Message != "TimeVault: /login user=guest 0.001(s)" || g.Level != slog.LevelWarn {
		t.Errorf("a page that was not found is a warning for a guest: %+v", g)
	}
	f := Failure("10.0.0.1", "E1", "/jobs/12", 500, "3fa9")
	if f.Message != "TimeVault: error /jobs/12 user=E1 ref=3fa9" || f.Level != slog.LevelError || f.Status != 500 || f.Class != ClassEvent {
		t.Errorf("Failure = %+v", f)
	}
	if s := Scheduler("scheduler.queued", 3, slog.LevelInfo); s.Message != "TimeVault: scheduler.queued source=3 user=scheduler" || s.Status != 200 {
		t.Errorf("Scheduler = %+v", s)
	}
	if s := Scheduler("scheduler.failed", 3, slog.LevelWarn); s.Status != 500 || s.Level != slog.LevelWarn {
		t.Errorf("a scheduler warning has the status 500: %+v", s)
	}
	if s := Started("1.0.0"); s.Message != "TimeVault: started version=1.0.0" || Stopped().Message != "TimeVault: stopped" {
		t.Errorf("Started = %+v", s)
	}
}

// fakePusher stands in for Redis: it can fail, block, and it remembers what it was given.
type fakePusher struct {
	mu    sync.Mutex
	err   error
	block chan struct{}
	got   [][]byte
	calls atomic.Int64
}

func (p *fakePusher) RPush(ctx context.Context, key string, values ...any) *redis.IntCmd {
	p.calls.Add(1)
	cmd := redis.NewIntCmd(ctx)
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		cmd.SetErr(p.err)
		return cmd
	}
	for _, v := range values {
		p.got = append(p.got, v.([]byte))
	}
	cmd.SetVal(int64(len(values)))
	return cmd
}

func (p *fakePusher) setErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *fakePusher) values() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.got...)
}

func (w *Writer) queueEmpty() bool { return len(w.queue) == 0 }

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
