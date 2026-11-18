// Package audit records security-relevant actions in an append-only table.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Actions written to the audit trail.
const (
	ActionLogin          = "auth.login"
	ActionLoginFailed    = "auth.login_failed"
	ActionLogout         = "auth.logout"
	ActionSourceCreate   = "source.create"
	ActionSourceUpdate   = "source.update"
	ActionBackupRequest  = "backup.request"
	ActionBackupFinish   = "backup.finish"
	ActionVerifyRequest  = "verify.request"
	ActionVerifyFinish   = "verify.finish"
	ActionRestorePreview = "restore.preview"
	ActionRestoreRequest = "restore.request"
	ActionRestoreFinish  = "restore.finish"
	ActionJobCancel      = "job.cancel"
	ActionAccessDenied   = "access.denied"
	ActionRetentionPrune = "retention.prune"
	// ActionAppUpdate is a step of the replacement of the program by a newer one; the detail names the stage
	// (staged, restart, done, cancelled or failed).
	ActionAppUpdate = "app.update"
)

// ActorUpdater is the actor of what the service does to itself: the replacement of its program by a newer one.
const ActorUpdater = "updater"

// Results.
const (
	ResultOK     = "ok"
	ResultDenied = "denied"
	ResultFailed = "failed"
)

// Event is one audit record. Detail must never contain secrets.
type Event struct {
	ID         int64
	At         time.Time
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	SourceID   *int64
	Result     string
	RemoteAddr string
	Detail     map[string]any
	// Duration is how long the audited operation ran; zero when it has no duration.
	Duration time.Duration
}

// HasDuration reports whether the event records how long its operation ran.
func (e Event) HasDuration() bool { return e.Duration > 0 }

// DetailText is the detail as compact JSON, for display.
func (e Event) DetailText() string {
	if len(e.Detail) == 0 {
		return ""
	}
	b, err := json.Marshal(e.Detail)
	if err != nil {
		return ""
	}
	return string(b)
}

// Logger writes and reads the audit trail.
type Logger struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// New creates a Logger.
func New(pool *pgxpool.Pool, log *slog.Logger) *Logger { return &Logger{pool: pool, log: log} }

// Record appends an event. Callers whose operation must not proceed without an
// audit record (restores) check the returned error; others may ignore it, it is
// already logged.
func (l *Logger) Record(ctx context.Context, e Event) error {
	if e.Result == "" {
		e.Result = ResultOK
	}
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("audit: encode detail: %w", err)
	}
	// A duration of zero means the event has none: sign-ins and requests are not timed. A real operation that
	// took less than a millisecond is stored as 1 ms, so it still shows.
	var durationMS *int64
	if e.Duration > 0 {
		ms := max(e.Duration.Milliseconds(), 1)
		durationMS = &ms
	}
	_, err = l.pool.Exec(context.WithoutCancel(ctx),
		`INSERT INTO audit_events (actor, action, target_type, target_id, source_id, result, remote_addr, detail, duration_ms)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.Actor, e.Action, e.TargetType, e.TargetID, e.SourceID, e.Result, e.RemoteAddr, b, durationMS)
	if err != nil {
		l.log.Error("write audit event", "action", e.Action, "actor", e.Actor, "error", err)
		return fmt.Errorf("audit: write event: %w", err)
	}
	return nil
}

// Filter narrows List.
type Filter struct {
	Action string
	Actor  string
	// BeforeID continues after a previous page (events with a smaller id).
	BeforeID int64
	Limit    int
	// OnlySources limits the list to the events of the sources in SourceIDs and the events that belong to no
	// source (sign-ins and the like). With an empty SourceIDs only the latter are listed. The cancellation of a job
	// that an older version recorded without its source belongs to the source of the job; when the job is gone it
	// cannot be placed, and it is left out.
	OnlySources bool
	SourceIDs   []int64
}

// List returns events newest first.
func (l *Logger) List(ctx context.Context, f Filter) ([]Event, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	if f.SourceIDs == nil {
		f.SourceIDs = []int64{}
	}
	rows, err := l.pool.Query(ctx,
		`SELECT e.id, e.at, e.actor, e.action, e.target_type, e.target_id, e.source_id, e.result, e.remote_addr, e.detail, e.duration_ms
		 FROM audit_events e
		 LEFT JOIN jobs j ON e.source_id IS NULL AND e.target_type = 'job'
		                 AND j.id = CASE WHEN e.target_id ~ '^[0-9]{1,18}$' THEN e.target_id::bigint END
		 WHERE ($1 = '' OR e.action = $1) AND ($2 = '' OR e.actor = $2) AND ($3 = 0 OR e.id < $3)
		   AND (NOT $5::boolean
		        OR COALESCE(e.source_id, j.source_id) = ANY($6::bigint[])
		        OR (e.source_id IS NULL AND e.target_type <> 'job'))
		 ORDER BY e.id DESC LIMIT $4`, f.Action, f.Actor, f.BeforeID, f.Limit, f.OnlySources, f.SourceIDs)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		var detail []byte
		var durationMS *int64
		err := r.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.SourceID, &e.Result, &e.RemoteAddr, &detail, &durationMS)
		if durationMS != nil {
			e.Duration = time.Duration(*durationMS) * time.Millisecond
		}
		if err == nil && len(detail) > 0 {
			err = json.Unmarshal(detail, &e.Detail)
		}
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	return out, nil
}
