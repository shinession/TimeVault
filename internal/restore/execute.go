package restore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"

	"timevault/internal/platform"
	"timevault/internal/repository"
)

const (
	copyBufSize     = 1 << 20
	maxStoredOKRows = 20_000
	flushRows       = 500
	partPrefix      = ".tvpart-"
)

// Progress is a snapshot of a running restore.
type Progress struct {
	Phase       string `json:"phase"`
	Current     string `json:"current,omitempty"`
	ItemsTotal  int64  `json:"items_total"`
	Items       int64  `json:"items"`
	BytesTotal  int64  `json:"bytes_total"`
	Bytes       int64  `json:"bytes"`
	Created     int64  `json:"created"`
	Overwritten int64  `json:"overwritten"`
	Renamed     int64  `json:"renamed"`
	Skipped     int64  `json:"skipped"`
	Failed      int64  `json:"failed"`
}

// Phases of a restore.
const (
	PhasePlanning  = "planning"
	PhaseRestoring = "restoring"
	PhaseDone      = "done"
)

// Reporter receives the status of a running restore.
type Reporter interface {
	Phase(name string)
	Progress(p Progress)
	Event(level, message string)
}

// NopReporter discards everything.
type NopReporter struct{}

func (NopReporter) Phase(string)         {}
func (NopReporter) Progress(Progress)    {}
func (NopReporter) Event(string, string) {}

// Result totals what a restore did.
type Result struct {
	Created     int64
	Overwritten int64
	Renamed     int64
	Skipped     int64
	Failed      int64
	Dirs        int64
	Bytes       int64
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Execute restores the request below the restore root. It never writes before
// a complete planning pass has shown that every item can be restored, unless
// the request allows a partial restore. Every file is hash-checked while it is
// written; the per-item outcomes are stored for the job's result page.
func (m *Manager) Execute(ctx context.Context, jobID int64, req Request, rep Reporter) (Result, error) {
	var res Result
	if rep == nil {
		rep = NopReporter{}
	}
	req, err := m.normalize(req)
	if err != nil {
		return res, err
	}
	src, point, err := m.load(ctx, req)
	if err != nil {
		return res, err
	}
	destDir := filepath.Join(m.root, req.DestName)

	rep.Phase(PhasePlanning)
	var totals Totals
	var firstProblems []string
	err = m.walk(ctx, req, src, point, destDir, func(it Item) error {
		totals.add(it)
		if it.Action == ActionBlocked && len(firstProblems) < 5 {
			firstProblems = append(firstProblems, fmt.Sprintf("%s: %s", it.Rel, it.Problem))
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if totals.Blocked > 0 && !req.AllowPartial {
		for _, p := range firstProblems {
			rep.Event("error", p)
		}
		return res, fmt.Errorf("%w: %d item(s) are blocked and a partial restore was not allowed", ErrBlocked, totals.Blocked)
	}
	if totals.Items == 0 {
		return res, invalid("nothing to restore")
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return res, fmt.Errorf("restore: create destination: %w", err)
	}
	if err := checkDestination(destDir); err != nil {
		return res, err
	}
	dest, err := os.OpenRoot(destDir)
	if err != nil {
		return res, fmt.Errorf("restore: open destination: %w", err)
	}
	defer dest.Close()

	rep.Phase(PhaseRestoring)
	rep.Event("info", fmt.Sprintf("restoring %d item(s), %d bytes, from recovery point #%d to %s", totals.Items, totals.Bytes, point.Seq, destDir))
	sink := &itemSink{m: m, jobID: jobID}
	prog := Progress{Phase: PhaseRestoring, ItemsTotal: totals.Items, BytesTotal: totals.Bytes}
	err = m.walk(ctx, req, src, point, destDir, func(it Item) error {
		status, bytes, msg := m.apply(ctx, dest, jobID, it)
		if status == statusFailed {
			rep.Event("error", fmt.Sprintf("%s: %s", it.Rel, msg))
		}
		prog.Items++
		prog.Current = it.Rel
		switch {
		case status == statusFailed:
			res.Failed++
			prog.Failed++
		case it.Action == ActionCreate:
			res.Created++
			prog.Created++
		case it.Action == ActionOverwrite:
			res.Overwritten++
			prog.Overwritten++
		case it.Action == ActionRename:
			res.Renamed++
			prog.Renamed++
		case it.IsDir && (it.Action == ActionMkdir || it.Action == ActionDirExists):
			res.Dirs++
		default:
			res.Skipped++
			prog.Skipped++
		}
		res.Bytes += bytes
		prog.Bytes += bytes
		rep.Progress(prog)
		return sink.add(ctx, it, status, bytes, msg)
	})
	if flushErr := sink.flush(context.WithoutCancel(ctx)); err == nil {
		err = flushErr
	}
	if err != nil {
		return res, err
	}
	prog.Phase = PhaseDone
	rep.Progress(prog)
	if res.Failed > 0 {
		return res, fmt.Errorf("restore: %d item(s) could not be restored; see the item list", res.Failed)
	}
	return res, nil
}

const (
	statusOK      = "ok"
	statusSkipped = "skipped"
	statusFailed  = "failed"
)

// apply performs one item and reports its status, bytes written and a message.
func (m *Manager) apply(ctx context.Context, dest *os.Root, jobID int64, it Item) (status string, written int64, msg string) {
	if err := ctx.Err(); err != nil {
		return statusFailed, 0, err.Error()
	}
	rel := filepath.FromSlash(it.Rel)
	switch it.Action {
	case ActionMkdir:
		if err := dest.MkdirAll(rel, 0o755); err != nil {
			return statusFailed, 0, "cannot create the folder: " + err.Error()
		}
		return statusOK, 0, ""
	case ActionDirExists:
		return statusOK, 0, ""
	case ActionSkipSame:
		return statusSkipped, 0, "already present with identical content"
	case ActionSkipExists:
		return statusSkipped, 0, "already exists; kept as it is"
	case ActionBlocked:
		return statusFailed, 0, it.Problem
	case ActionCreate, ActionOverwrite, ActionRename:
		target := it.Rel
		if it.Action == ActionRename {
			target = it.Target
		}
		n, warn, err := m.writeFile(ctx, dest, jobID, it, target)
		if err != nil {
			return statusFailed, 0, err.Error()
		}
		msg = warn
		if it.Action == ActionRename {
			msg = joinMsg("restored as "+target, warn)
		}
		return statusOK, n, msg
	}
	return statusFailed, 0, "unknown action " + string(it.Action)
}

func joinMsg(a, b string) string {
	if b == "" {
		return a
	}
	return a + "; " + b
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeFile copies one object into place through a temporary file. warn is set
// when the content is fine but a piece of metadata could not be restored.
func (m *Manager) writeFile(ctx context.Context, dest *os.Root, jobID int64, it Item, target string) (written int64, warn string, err error) {
	h, err := repository.HashFromBytes(it.SHA256)
	if err != nil {
		return 0, "", errors.New("invalid content hash in the catalog")
	}
	src, err := m.repo.Open(h)
	if err != nil {
		return 0, "", err
	}
	defer src.Close()

	dir := path.Dir(target)
	if dir != "." {
		if err := dest.MkdirAll(filepath.FromSlash(dir), 0o755); err != nil {
			return 0, "", fmt.Errorf("cannot create the folder: %w", err)
		}
	}
	tmp := path.Join(dir, fmt.Sprintf("%s%d-%s", partPrefix, jobID, randomHex(6)))
	tmpOS, targetOS := filepath.FromSlash(tmp), filepath.FromSlash(target)
	f, err := dest.OpenFile(tmpOS, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("cannot create a temporary file: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = f.Close()
			_ = dest.Remove(tmpOS)
		}
	}()

	hasher := sha256.New()
	buf := make([]byte, copyBufSize)
	n, err := io.CopyBuffer(io.MultiWriter(f, hasher), &ctxReader{ctx: ctx, r: src}, buf)
	if err != nil {
		return 0, "", fmt.Errorf("cannot write the file: %w", err)
	}
	var got repository.Hash
	copy(got[:], hasher.Sum(nil))
	if n != it.Size || got != h {
		return 0, "", errors.New("the repository content does not match its recorded checksum; the file was not restored")
	}
	if err := f.Sync(); err != nil {
		return 0, "", fmt.Errorf("cannot flush the file: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, "", fmt.Errorf("cannot close the file: %w", err)
	}
	if it.MTimeNS != 0 {
		mt := time.Unix(0, it.MTimeNS)
		if err := dest.Chtimes(tmpOS, mt, mt); err != nil {
			warn = joinMsg(warn, "modification time was not restored")
		}
	}
	abs := filepath.Join(dest.Name(), targetOS)
	if it.Action == ActionOverwrite {
		_ = platform.SetFileAttributes(abs, 0) // a read-only file would block the replacement
	}
	if err := dest.Rename(tmpOS, targetOS); err != nil {
		return 0, "", fmt.Errorf("cannot move the file into place: %w", err)
	}
	done = true
	if st, err := dest.Lstat(targetOS); err != nil || st.Size() != it.Size {
		return 0, "", errors.New("the restored file does not have the expected size")
	}
	if it.Attrs != 0 {
		if err := platform.SetFileAttributes(abs, it.Attrs); err != nil {
			warn = joinMsg(warn, "file attributes were not restored")
		}
	}
	return n, warn, nil
}

// itemSink stores per-item outcomes in batches. Failures are always stored;
// successful items only up to a cap so a huge restore cannot flood the table.
type itemSink struct {
	m        *Manager
	jobID    int64
	rows     [][]any
	storedOK int
	capped   bool
}

func (s *itemSink) add(ctx context.Context, it Item, status string, bytes int64, msg string) error {
	if status != statusFailed {
		if s.storedOK >= maxStoredOKRows {
			s.capped = true
			return nil
		}
		s.storedOK++
	}
	rel := it.Rel
	if it.Action == ActionRename && it.Target != "" {
		rel = it.Target
	}
	var message any
	if msg != "" {
		message = msg
	}
	s.rows = append(s.rows, []any{s.jobID, rel, it.IsDir, string(it.Action), status, bytes, message})
	if len(s.rows) >= flushRows {
		return s.flush(ctx)
	}
	return nil
}

func (s *itemSink) flush(ctx context.Context) error {
	if len(s.rows) == 0 || s.m.pool == nil {
		s.rows = nil
		return nil
	}
	_, err := s.m.pool.CopyFrom(ctx, pgx.Identifier{"restore_items"},
		[]string{"job_id", "rel_path", "is_dir", "action", "status", "bytes", "message"}, pgx.CopyFromRows(s.rows))
	s.rows = nil
	if err != nil {
		return fmt.Errorf("restore: store item results: %w", err)
	}
	return nil
}

// StoredItem is one persisted per-item outcome.
type StoredItem struct {
	ID      int64
	Rel     string
	IsDir   bool
	Action  string
	Status  string
	Bytes   int64
	Message string
}

// ListItems returns the stored outcomes of a restore job in order. status may
// be "" for all, or ok, skipped or failed.
func (m *Manager) ListItems(ctx context.Context, jobID int64, status string, afterID int64, limit int) ([]StoredItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := m.pool.Query(ctx,
		`SELECT id, rel_path, is_dir, action, status, bytes, coalesce(message, '')
		 FROM restore_items
		 WHERE job_id = $1 AND ($2 = '' OR status = $2) AND id > $3
		 ORDER BY id LIMIT $4`, jobID, status, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("restore: list items: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (StoredItem, error) {
		var it StoredItem
		err := r.Scan(&it.ID, &it.Rel, &it.IsDir, &it.Action, &it.Status, &it.Bytes, &it.Message)
		return it, err
	})
	if err != nil {
		return nil, fmt.Errorf("restore: list items: %w", err)
	}
	return items, nil
}
