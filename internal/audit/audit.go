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
)

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
}

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
	_, err = l.pool.Exec(context.WithoutCancel(ctx),
		`INSERT INTO audit_events (actor, action, target_type, target_id, source_id, result, remote_addr, detail)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.Actor, e.Action, e.TargetType, e.TargetID, e.SourceID, e.Result, e.RemoteAddr, b)
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
}

// List returns events newest first.
func (l *Logger) List(ctx context.Context, f Filter) ([]Event, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	rows, err := l.pool.Query(ctx,
		`SELECT id, at, actor, action, target_type, target_id, source_id, result, remote_addr, detail
		 FROM audit_events
		 WHERE ($1 = '' OR action = $1) AND ($2 = '' OR actor = $2) AND ($3 = 0 OR id < $3)
		 ORDER BY id DESC LIMIT $4`, f.Action, f.Actor, f.BeforeID, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		var detail []byte
		err := r.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.SourceID, &e.Result, &e.RemoteAddr, &detail)
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
