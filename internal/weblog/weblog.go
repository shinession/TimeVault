// Package weblog appends lines to the web log that the company applications share: the Redis list web_log, which
// every application writes its notable events to and which a log page reads.
//
// An element of the list is zlib.compress(pickle.dumps(line)), see Encode, and a line is six fields told apart by
// " | ":
//
//	ip | time | message | status | level | host
//	127.0.0.1 | 2026-11-18 13:58:06.446524 | home_page: access | 200 | info | sgmeslt595
//
// The status is the HTTP status of the request, or 200 (500 when it failed) for what the service does on its own; the
// level is info, warning, error or debug; the host is the name of the computer. Readers that split a line by position
// must keep working, so TimeVault never adds a field: the account goes into the message, as user=<employee ID>.
//
// The web log is best effort and never gets in the way of the service. Write only queues a line; a worker sends the
// queue to Redis in batches. A flood of lines is cut at the door (a tighter limit for pages than for events), a full
// queue and a Redis that does not answer drop lines instead of waiting, and the writer stays away from Redis for a
// while after a failure. The list is shared and never expires, so what is written must stay small and must not hold
// what a visitor typed, a path or an error text: messages carry the route that matched, numbers (folder, recovery
// point, job) and employee IDs, nothing else.
package weblog

import (
	"context"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// DefaultKey is the Redis list that the company applications share.
const DefaultKey = "web_log"

const (
	timeLayout     = "2006-01-02 15:04:05.000000" // str(datetime.now()) in Python
	maxMessage     = 400                          // runes of the message field
	queueSize      = 1024                         // lines waiting for the worker
	maxBatch       = 100                          // lines sent with one RPUSH
	warnEvery      = 5 * time.Minute              // the longest silence about a Redis that does not answer
	reportEvery    = time.Minute                  // how often lines cut by the limits are reported
	minBackoff     = 30 * time.Second
	maxBackoff     = 5 * time.Minute
	pageRate       = 10 // pages per second the writer takes, on average
	pageBurst      = 100
	eventRate      = 20 // events per second
	eventBurst     = 200
	defaultTimeout = 3 * time.Second
)

// Class says what a line is about, which decides the limit that applies to it.
type Class int

const (
	// ClassEvent is something that happened and is recorded elsewhere too: an audit event, a scheduler run, an error.
	ClassEvent Class = iota
	// ClassPage is a page that somebody opened. A visitor can open as many as they like, so the limit is tighter.
	ClassPage
)

// Entry is one line before it is written.
type Entry struct {
	// At is when it happened; zero means the moment it is written.
	At time.Time
	// IP is the address of the browser. Empty means the work the service does on its own, 127.0.0.1.
	IP      string
	Message string
	// Status is the HTTP status, zero meaning 200.
	Status int
	Level  slog.Level
	Class  Class
}

// Sink takes lines. A *Writer is one; a test can use another.
type Sink interface{ Write(Entry) }

// Pusher is the part of a Redis client that the writer uses.
type Pusher interface {
	RPush(ctx context.Context, key string, values ...any) *redis.IntCmd
}

// Options configures a Writer.
type Options struct {
	// Key is the Redis list; empty means DefaultKey.
	Key string
	// Host is the last field of every line; empty means the name of this computer in lower case.
	Host string
	// MinLevel is the lowest level that is written: slog.LevelInfo leaves out the debug lines.
	MinLevel slog.Level
	// Timeout is how long one send to Redis may take; zero means 3 seconds.
	Timeout time.Duration
	// Log receives the few messages the writer has about itself; nil means none.
	Log *slog.Logger
	// Redact removes secrets from an error before it is logged, for example Conn.Redact; nil means as it is.
	Redact func(error) error
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// The limits and delays, for tests; zero means the defaults.
	pageRate, eventRate   float64
	pageBurst, eventBurst int
	queue                 int
	minBackoff            time.Duration
	maxBackoff            time.Duration
}

// Writer queues lines and sends them to Redis. It is safe for concurrent use.
type Writer struct {
	rdb     Pusher
	key     string
	host    string
	min     slog.Level
	timeout time.Duration
	log     *slog.Logger
	redact  func(error) error
	now     func() time.Time

	queue  chan Entry
	stop   chan struct{}
	done   chan struct{}
	closed atomic.Bool
	page   *bucket
	event  *bucket

	written atomic.Int64
	dropped atomic.Int64

	// Only the worker touches these.
	minWait, maxWait time.Duration
	failing          bool
	wait             time.Duration
	retryAt          time.Time
	lastWarn         time.Time
	reported         int64
}

// New starts the worker. Nothing is sent until there is a line, so a Redis that is down does not stop the service
// from starting. Call Close to send what is still queued.
func New(rdb Pusher, o Options) *Writer {
	w := &Writer{
		rdb: rdb, key: o.Key, min: o.MinLevel, timeout: o.Timeout, log: o.Log, redact: o.Redact, now: o.Now,
		stop: make(chan struct{}), done: make(chan struct{}), minWait: o.minBackoff, maxWait: o.maxBackoff,
	}
	host := o.Host
	if host == "" {
		host = defaultHost()
	}
	w.host = cleanHost(host)
	if w.key == "" {
		w.key = DefaultKey
	}
	if w.timeout <= 0 {
		w.timeout = defaultTimeout
	}
	if w.log == nil {
		w.log = slog.New(slog.DiscardHandler)
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.minWait <= 0 {
		w.minWait = minBackoff
	}
	if w.maxWait <= 0 {
		w.maxWait = maxBackoff
	}
	queue := o.queue
	if queue <= 0 {
		queue = queueSize
	}
	w.queue = make(chan Entry, queue)
	now := w.now()
	w.page = newBucket(orDefault(o.pageRate, pageRate), int(orDefault(float64(o.pageBurst), pageBurst)), now)
	w.event = newBucket(orDefault(o.eventRate, eventRate), int(orDefault(float64(o.eventBurst), eventBurst)), now)
	go w.run()
	return w
}

func orDefault(v, def float64) float64 {
	if v > 0 {
		return v
	}
	return def
}

// Host is the host name that ends every line.
func (w *Writer) Host() string { return w.host }

// Key is the Redis list the lines go to.
func (w *Writer) Key() string { return w.key }

// Stats counts the lines that were written to Redis and the ones that were dropped (a limit, a full queue, or a
// Redis that did not take them).
func (w *Writer) Stats() (written, dropped int64) { return w.written.Load(), w.dropped.Load() }

// Write queues e and returns at once. A line below MinLevel, over the limit of its class, or for which there is no room
// is dropped. A nil Writer drops everything, so the web log can be switched off by not having one.
func (w *Writer) Write(e Entry) {
	if w == nil || w.closed.Load() || e.Level < w.min {
		return
	}
	now := w.now()
	if e.At.IsZero() {
		e.At = now
	}
	limit := w.event
	if e.Class == ClassPage {
		limit = w.page
	}
	if !limit.allow(now) {
		w.dropped.Add(1)
		return
	}
	select {
	case w.queue <- e:
	default:
		w.dropped.Add(1)
	}
}

// Close sends what is queued and stops the worker, waiting no longer than ctx allows. It does not close the Redis
// client, which belongs to whoever made it. Lines written afterwards are dropped.
func (w *Writer) Close(ctx context.Context) error {
	if w == nil || w.closed.Swap(true) {
		return nil
	}
	close(w.stop)
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) run() {
	defer close(w.done)
	batch := make([]any, 0, maxBatch)
	report := time.NewTicker(reportEvery)
	defer report.Stop()
	for {
		select {
		case e := <-w.queue:
			batch = w.fill(append(batch[:0], w.encode(e)))
			w.push(batch)
		case <-report.C:
			w.reportCut()
		case <-w.stop:
			for {
				if batch = w.fill(batch[:0]); len(batch) == 0 {
					w.reportCut()
					return
				}
				w.push(batch)
			}
		}
	}
}

// fill adds the lines that are waiting, up to a batch, without waiting for more.
func (w *Writer) fill(batch []any) []any {
	for len(batch) < maxBatch {
		select {
		case e := <-w.queue:
			batch = append(batch, w.encode(e))
		default:
			return batch
		}
	}
	return batch
}

func (w *Writer) encode(e Entry) []byte { return Encode(w.Line(e)) }

// push sends a batch with one RPUSH. After a failure the worker leaves Redis alone for a while, 30 seconds first and
// then longer up to 5 minutes, and drops what comes meanwhile: waiting for a Redis that does not answer would only
// hold the queue.
func (w *Writer) push(batch []any) {
	n := int64(len(batch))
	if len(batch) == 0 {
		return
	}
	now := w.now()
	if now.Before(w.retryAt) {
		w.dropped.Add(n)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	err := w.rdb.RPush(ctx, w.key, batch...).Err()
	cancel()
	if err != nil {
		w.dropped.Add(n)
		w.failed(now, err)
		return
	}
	w.written.Add(n)
	if w.failing {
		w.failing, w.wait, w.retryAt = false, 0, time.Time{}
		w.log.Info("web log: Redis takes the lines again", "key", w.key)
	}
}

func (w *Writer) failed(now time.Time, err error) {
	if w.wait == 0 {
		w.wait = w.minWait
	} else {
		w.wait = min(2*w.wait, w.maxWait)
	}
	w.retryAt = now.Add(w.wait)
	if w.failing && now.Sub(w.lastWarn) < warnEvery {
		return
	}
	w.failing, w.lastWarn = true, now
	if w.redact != nil {
		err = w.redact(err)
	}
	w.log.Warn("web log: the lines cannot be written to Redis, so they are dropped until it answers",
		"key", w.key, "retry_in", w.wait.String(), "error", err)
}

// reportCut says how many lines the limits and a full queue cut since the last report. Lines that Redis did not take
// are reported by failed.
func (w *Writer) reportCut() {
	_, dropped := w.Stats()
	if dropped > w.reported && !w.failing {
		w.log.Warn("web log: lines were dropped because too many came at once", "lines", dropped-w.reported, "key", w.key)
	}
	w.reported = dropped
}

// Line is the text of e as it is stored: six fields told apart by " | ".
func (w *Writer) Line(e Entry) string {
	at := e.At
	if at.IsZero() {
		at = w.now()
	}
	status := e.Status
	if status == 0 {
		status = 200
	}
	return strings.Join([]string{cleanIP(e.IP), at.In(time.Local).Format(timeLayout), cleanText(e.Message), strconv.Itoa(status), levelName(e.Level), w.host}, " | ")
}

// levelName is the type of a line: info, warning, error or debug.
func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warning"
	case l >= slog.LevelInfo:
		return "info"
	}
	return "debug"
}

// cleanIP writes an address the way Python prints it; text that is no address becomes "-", and no address at all is
// the work of the service itself.
func cleanIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "127.0.0.1"
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "-"
	}
	return a.WithZone("").Unmap().String()
}

// cleanText makes s safe for a field: no control character, no line end, no " | " that would start another field,
// and not longer than maxMessage runes.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	var b strings.Builder
	n := 0
	for _, r := range s {
		switch {
		case r == '|':
			r = '/'
		case unicode.IsControl(r) || r == '\u2028' || r == '\u2029':
			r = ' '
		}
		if n++; n > maxMessage {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// cleanHost keeps the characters of a host name; nothing left means "unknown".
func cleanHost(h string) string {
	var b strings.Builder
	for _, r := range h {
		if r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_') {
			b.WriteRune(r)
		}
		if b.Len() == 63 {
			break
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// defaultHost is the name of this computer in lower case, the way the other applications write it.
func defaultHost() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.ToLower(h)
}

// bucket is a token bucket: it lets rate lines per second through on average, and burst at once after a quiet time.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(rate float64, burst int, now time.Time) *bucket {
	return &bucket{rate: rate, burst: float64(burst), tokens: float64(burst), last: now}
}

func (b *bucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d := now.Sub(b.last).Seconds(); d > 0 {
		b.tokens = min(b.burst, b.tokens+d*b.rate)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
