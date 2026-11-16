// Package restore plans and performs restores of files and folders from a
// committed recovery point.
//
// Restores only ever write below one configured restore root, into a folder
// the requester names. Every file is streamed from the repository into a
// temporary file, checked against its recorded SHA-256 and only then renamed
// into place, and every step is confined with os.Root so names from the
// catalog or from the destination can never lead outside the restore folder.
package restore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/catalog"
	"timevault/internal/pathutil"
	"timevault/internal/repository"
)

const (
	// maxSelectedPaths is as many items as the largest page of a folder listing holds, so "select all" on a page
	// can always be restored.
	maxSelectedPaths = 500
	maxPlanItems     = 2_000_000
	sampleSize       = 200
	problemSize      = 100
	tokenTTL         = 15 * time.Minute
)

var (
	// ErrDisabled means no restore root is configured.
	ErrDisabled = errors.New("restore: no restore folder is configured")
	// ErrInvalidRequest wraps every problem with the request itself.
	ErrInvalidRequest = errors.New("restore: invalid request")
	// ErrNotRestorable means the recovery point cannot be restored from.
	ErrNotRestorable = errors.New("restore: the recovery point is not restorable")
	// ErrStalePreview means the confirmation does not match the current plan.
	ErrStalePreview = errors.New("restore: the preview is no longer valid; preview again")
	// ErrNotConfirmed means a required explicit confirmation is missing.
	ErrNotConfirmed = errors.New("restore: confirmation is missing")
	// ErrBlocked means items cannot be restored and partial restores were not allowed.
	ErrBlocked = errors.New("restore: some items cannot be restored")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}

// Policy says what happens when a file already exists at the destination.
type Policy string

const (
	PolicySkip      Policy = "skip"
	PolicyOverwrite Policy = "overwrite"
	PolicyRename    Policy = "rename"
)

// Action is what a restore does with one item.
type Action string

const (
	ActionMkdir      Action = "mkdir"
	ActionDirExists  Action = "dir_exists"
	ActionCreate     Action = "create"
	ActionOverwrite  Action = "overwrite"
	ActionRename     Action = "rename"
	ActionSkipSame   Action = "skip_same"
	ActionSkipExists Action = "skip_exists"
	ActionBlocked    Action = "blocked"
)

// Request describes a restore. It is the same for the preview and the restore.
type Request struct {
	SourceID int64 `json:"source_id"`
	PointID  int64 `json:"point_id"`
	// Paths are source-relative; "" selects the whole source. Each selected item
	// is restored as <destination>/<its own name>.
	Paths []string `json:"paths"`
	// DestName is the folder, below the restore root, that receives the files.
	DestName string `json:"dest_name"`
	Policy   Policy `json:"policy"`
	// AllowPartial lets the restore proceed when some items cannot be restored.
	AllowPartial bool `json:"allow_partial"`
}

// Item is one file or folder of a plan.
type Item struct {
	Rel     string // path below the destination folder
	IsDir   bool
	Size    int64
	MTimeNS int64
	Attrs   uint32
	SHA256  []byte
	Action  Action
	// Target is the final path of a renamed item.
	Target  string
	Problem string
}

// Totals summarize a plan.
type Totals struct {
	Items       int64
	Dirs        int64
	Files       int64
	Bytes       int64
	Create      int64
	Overwrite   int64
	Rename      int64
	SkipSame    int64
	SkipExists  int64
	Blocked     int64
	Missing     int64
	NotCaptured int64
}

// Plan is the dry-run result.
type Plan struct {
	Request  Request
	Source   catalog.Source
	Point    catalog.Point
	DestDir  string
	Totals   Totals
	Sample   []Item
	Problems []Item
	Digest   string
}

// Config wires a Manager.
type Config struct {
	Catalog    *catalog.Store
	Repository *repository.Store
	Pool       *pgxpool.Pool
	// Root is the only folder restores may write to.
	Root     string
	TokenKey []byte
	Logger   *slog.Logger
	Now      func() time.Time
}

// Manager plans and performs restores.
type Manager struct {
	cat  *catalog.Store
	repo *repository.Store
	pool *pgxpool.Pool
	root string
	key  []byte
	log  *slog.Logger
	now  func() time.Time
}

// New creates the restore root when needed and returns a Manager. With an
// empty Root the Manager is disabled and every operation returns ErrDisabled.
func New(cfg Config) (*Manager, error) {
	m := &Manager{cat: cfg.Catalog, repo: cfg.Repository, pool: cfg.Pool, key: cfg.TokenKey, log: cfg.Logger, now: cfg.Now}
	if m.now == nil {
		m.now = time.Now
	}
	if m.log == nil {
		m.log = slog.New(slog.DiscardHandler)
	}
	if len(m.key) < 16 {
		return nil, errors.New("restore: the token key must have at least 16 bytes")
	}
	if strings.TrimSpace(cfg.Root) == "" {
		return m, nil
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("restore: resolve restore root: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("restore: create restore root: %w", err)
	}
	if cfg.Repository != nil && pathutil.Overlaps(pathutil.RealOrClean(cfg.Repository.Root()), pathutil.RealOrClean(root)) {
		return nil, errors.New("restore: the restore root must not overlap the repository")
	}
	m.root = root
	return m, nil
}

// Enabled reports whether restores are available.
func (m *Manager) Enabled() bool { return m.root != "" }

// Root is the restore root, or "" when disabled.
func (m *Manager) Root() string { return m.root }

// normalize validates a request and returns its cleaned form.
func (m *Manager) normalize(req Request) (Request, error) {
	if !m.Enabled() {
		return req, ErrDisabled
	}
	req.DestName = strings.TrimSpace(req.DestName)
	if err := pathutil.ValidateName(req.DestName); err != nil || strings.HasPrefix(req.DestName, ".tvpart") {
		return req, invalid("the destination folder name %q is not valid", req.DestName)
	}
	switch req.Policy {
	case "":
		req.Policy = PolicySkip
	case PolicySkip, PolicyOverwrite, PolicyRename:
	default:
		return req, invalid("unknown conflict policy %q", req.Policy)
	}
	if len(req.Paths) == 0 {
		return req, invalid("nothing is selected")
	}
	if len(req.Paths) > maxSelectedPaths {
		return req, invalid("at most %d items can be restored at once", maxSelectedPaths)
	}
	clean := make([]string, 0, len(req.Paths))
	for _, p := range req.Paths {
		c, err := pathutil.CleanRel(p)
		if err != nil {
			return req, invalid("%v", err)
		}
		clean = append(clean, c)
	}
	for i := range clean {
		for j := i + 1; j < len(clean); j++ {
			if clean[i] == clean[j] || within(clean[i], clean[j]) || within(clean[j], clean[i]) {
				return req, invalid("the selection contains overlapping items")
			}
		}
	}
	req.Paths = clean
	return req, nil
}

// within reports whether b lies inside a (a == "" is the whole source).
func within(a, b string) bool {
	if a == b {
		return false
	}
	if a == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(b), strings.ToLower(a)+"/")
}

func (m *Manager) load(ctx context.Context, req Request) (catalog.Source, catalog.Point, error) {
	point, err := m.cat.GetPoint(ctx, req.PointID)
	if err != nil {
		return catalog.Source{}, point, err
	}
	if point.SourceID != req.SourceID {
		return catalog.Source{}, point, invalid("the recovery point belongs to another source")
	}
	if !point.Restorable() {
		return catalog.Source{}, point, ErrNotRestorable
	}
	src, err := m.cat.GetSource(ctx, req.SourceID)
	if err != nil {
		return src, point, err
	}
	sources, err := m.cat.ListSources(ctx)
	if err != nil {
		return src, point, err
	}
	// Compare where the folders really are, so a junction cannot hide an overlap.
	realRoot := pathutil.RealOrClean(m.root)
	if m.repo != nil && pathutil.Overlaps(pathutil.RealOrClean(m.repo.Root()), realRoot) {
		return src, point, errors.New("restore: the restore root overlaps the repository")
	}
	for _, s := range sources {
		if pathutil.Overlaps(pathutil.RealOrClean(s.RootPath), realRoot) {
			return src, point, fmt.Errorf("restore: the restore root overlaps the source %q", s.Name)
		}
	}
	return src, point, nil
}

// checkDestination refuses a destination that already exists but is not a
// plain folder: a junction or symbolic link there could redirect the restore
// into a source or the repository.
func checkDestination(destDir string) error {
	st, err := os.Lstat(destDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("restore: inspect destination: %w", err)
	}
	if !st.IsDir() || st.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return invalid("the destination %q already exists and is not a plain folder", filepath.Base(destDir))
	}
	return nil
}

// Preview builds the dry-run plan without writing anything.
func (m *Manager) Preview(ctx context.Context, req Request) (Plan, error) {
	req, err := m.normalize(req)
	if err != nil {
		return Plan{}, err
	}
	src, point, err := m.load(ctx, req)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Request: req, Source: src, Point: point, DestDir: filepath.Join(m.root, req.DestName)}
	digest := sha256.New()
	writeDigestHeader(digest, req)
	err = m.walk(ctx, req, src, point, plan.DestDir, func(it Item) error {
		plan.Totals.add(it)
		writeDigestItem(digest, it)
		if len(plan.Sample) < sampleSize {
			plan.Sample = append(plan.Sample, it)
		}
		if it.Action == ActionBlocked && len(plan.Problems) < problemSize {
			plan.Problems = append(plan.Problems, it)
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	plan.Digest = hex.EncodeToString(digest.Sum(nil))
	return plan, nil
}

func (t *Totals) add(it Item) {
	t.Items++
	if it.IsDir {
		t.Dirs++
	} else {
		t.Files++
		t.Bytes += it.Size
	}
	switch it.Action {
	case ActionCreate:
		t.Create++
	case ActionOverwrite:
		t.Overwrite++
	case ActionRename:
		t.Rename++
	case ActionSkipSame:
		t.SkipSame++
	case ActionSkipExists:
		t.SkipExists++
	case ActionBlocked:
		t.Blocked++
		switch {
		case strings.HasPrefix(it.Problem, problemMissing):
			t.Missing++
		case strings.HasPrefix(it.Problem, problemNotCaptured):
			t.NotCaptured++
		}
	}
}

// Writes is the number of items that would change the destination.
func (t Totals) Writes() int64 { return t.Create + t.Overwrite + t.Rename }

func writeDigestHeader(h hash.Hash, req Request) {
	b, _ := json.Marshal(req)
	h.Write(b)
	h.Write([]byte{'\n'})
}

func writeDigestItem(h hash.Hash, it Item) {
	// Target is left out on purpose: a renamed file's final name carries a timestamp.
	fmt.Fprintf(h, "%s\x00%t\x00%d\x00%d\x00%x\x00%s\x00%s\n", it.Rel, it.IsDir, it.Size, it.MTimeNS, it.SHA256, it.Action, it.Problem)
}

// token binds a confirmation to the user and to the exact plan that was shown.
type token struct {
	Actor   string `json:"a"`
	Digest  string `json:"d"`
	Expires int64  `json:"e"`
}

func (m *Manager) mac(payload string) string {
	h := hmac.New(sha256.New, m.key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// IssueToken returns the confirmation token for a previewed plan.
func (m *Manager) IssueToken(plan Plan, actor string) string {
	b, _ := json.Marshal(token{Actor: actor, Digest: plan.Digest, Expires: m.now().Add(tokenTTL).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + m.mac(payload)
}

// Confirm re-plans the request and checks that tok was issued to actor for
// exactly this plan. It returns the fresh plan.
func (m *Manager) Confirm(ctx context.Context, req Request, tok, actor string) (Plan, error) {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(m.mac(payload))) {
		return Plan{}, fmt.Errorf("%w: the confirmation token is not valid", ErrNotConfirmed)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	var t token
	if err != nil || json.Unmarshal(raw, &t) != nil {
		return Plan{}, fmt.Errorf("%w: the confirmation token is not valid", ErrNotConfirmed)
	}
	if t.Actor != actor {
		return Plan{}, fmt.Errorf("%w: the confirmation was issued to another user", ErrNotConfirmed)
	}
	if !m.now().Before(time.Unix(t.Expires, 0)) {
		return Plan{}, fmt.Errorf("%w: the confirmation has expired", ErrStalePreview)
	}
	plan, err := m.Preview(ctx, req)
	if err != nil {
		return Plan{}, err
	}
	if plan.Digest != t.Digest {
		return Plan{}, ErrStalePreview
	}
	return plan, nil
}

const (
	problemMissing     = "object missing"
	problemNotCaptured = "not captured"
)

// walk enumerates the plan in a stable order and classifies each item against
// the destination folder, which may not exist yet.
func (m *Manager) walk(ctx context.Context, req Request, src catalog.Source, point catalog.Point, destDir string, fn func(Item) error) error {
	w := &walker{m: m, req: req, destDir: destDir, objects: map[repository.Hash]string{}, used: map[string]struct{}{}}
	if err := checkDestination(destDir); err != nil {
		return err
	}
	root, err := os.OpenRoot(destDir)
	switch {
	case err == nil:
		w.dest = root
		defer root.Close()
	case errors.Is(err, fs.ErrNotExist):
	default:
		return fmt.Errorf("restore: open destination: %w", err)
	}

	seenTop := map[string]struct{}{}
	for _, p := range req.Paths {
		entry, err := m.cat.ResolveAt(ctx, req.SourceID, point.Seq, p)
		if err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				return invalid("%q does not exist at this recovery point", "/"+p)
			}
			return err
		}
		base := pathutil.BaseRel(p)
		if p == "" {
			base = safeName(src.Name)
		}
		key := strings.ToLower(base)
		if _, dup := seenTop[key]; dup {
			return invalid("two selected items would both be restored as %q", base)
		}
		seenTop[key] = struct{}{}

		if err := w.emit(ctx, entry, base, fn); err != nil {
			return err
		}
		if entry.IsDir {
			err := m.cat.WalkAt(ctx, entry.NodeID, point.Seq, base, func(rel string, e catalog.Entry) error {
				return w.emit(ctx, e, rel, fn)
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func safeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), " .")
	if pathutil.ValidateName(out) != nil {
		return "source"
	}
	return out
}

type walker struct {
	m       *Manager
	req     Request
	destDir string
	dest    *os.Root
	objects map[repository.Hash]string
	used    map[string]struct{}
	blocked string
	count   int64
	buf     []byte
}

func (w *walker) emit(ctx context.Context, e catalog.Entry, rel string, fn func(Item) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.count++
	if w.count > maxPlanItems {
		return invalid("the selection has more than %d items; restore it in smaller parts", maxPlanItems)
	}
	it := Item{Rel: rel, IsDir: e.IsDir, Size: e.Size, MTimeNS: e.MTimeNS, Attrs: e.Attrs, SHA256: e.SHA256}
	if e.IsDir {
		it.Size, it.MTimeNS = 0, 0
	}
	w.classify(ctx, &it, e)
	return fn(it)
}

// sameContent reports whether the existing destination file really holds the
// recorded content. Equal size and modification time do not prove that: silent
// corruption, or a tool that keeps the date, leaves both unchanged.
func (w *walker) sameContent(ctx context.Context, it *Item) bool {
	f, err := w.dest.Open(filepath.FromSlash(it.Rel))
	if err != nil {
		return false
	}
	defer f.Close()
	if w.buf == nil {
		w.buf = make([]byte, copyBufSize)
	}
	h := sha256.New()
	if _, err := io.CopyBuffer(h, &ctxReader{ctx: ctx, r: f}, w.buf); err != nil {
		return false
	}
	return bytes.Equal(h.Sum(nil), it.SHA256)
}

func (w *walker) classify(ctx context.Context, it *Item, e catalog.Entry) {
	if w.blocked != "" {
		if strings.HasPrefix(it.Rel, w.blocked+"/") {
			it.Action, it.Problem = ActionBlocked, "its folder cannot be restored"
			return
		}
		w.blocked = ""
	}
	if it.IsDir {
		defer func() {
			if it.Action == ActionBlocked {
				w.blocked = it.Rel
			}
		}()
	}
	for _, comp := range strings.Split(it.Rel, "/") {
		if err := pathutil.ValidateName(comp); err != nil {
			it.Action, it.Problem = ActionBlocked, "unsafe name: "+err.Error()
			return
		}
	}
	if !it.IsDir {
		if e.State != catalog.VersionOK {
			reason := e.SkipReason
			if reason == "" {
				reason = "no content was recorded"
			}
			it.Action, it.Problem = ActionBlocked, problemNotCaptured+": "+reason
			return
		}
		if p := w.objectProblem(it); p != "" {
			it.Action, it.Problem = ActionBlocked, p
			return
		}
	}
	if w.dest == nil {
		if it.IsDir {
			it.Action = ActionMkdir
		} else {
			it.Action = ActionCreate
		}
		return
	}
	st, err := w.dest.Lstat(filepath.FromSlash(it.Rel))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if it.IsDir {
			it.Action = ActionMkdir
		} else {
			it.Action = ActionCreate
		}
	case err != nil:
		it.Action, it.Problem = ActionBlocked, "the destination cannot be inspected: "+err.Error()
	case it.IsDir && st.IsDir():
		it.Action = ActionDirExists
	case it.IsDir:
		it.Action, it.Problem = ActionBlocked, "the destination has a file with this folder's name"
	case st.IsDir():
		it.Action, it.Problem = ActionBlocked, "the destination has a folder with this file's name"
	case !st.Mode().IsRegular():
		it.Action, it.Problem = ActionBlocked, "the destination is not a regular file"
	case st.Size() == it.Size && st.ModTime().UnixNano() == it.MTimeNS && w.sameContent(ctx, it):
		it.Action = ActionSkipSame
	default:
		switch w.req.Policy {
		case PolicyOverwrite:
			it.Action = ActionOverwrite
		case PolicyRename:
			if target := w.uniqueName(it.Rel); target != "" {
				it.Action, it.Target = ActionRename, target
			} else {
				it.Action, it.Problem = ActionBlocked, "no free name for the restored copy"
			}
		default:
			it.Action = ActionSkipExists
		}
	}
}

func (w *walker) objectProblem(it *Item) string {
	h, err := repository.HashFromBytes(it.SHA256)
	if err != nil {
		return "invalid content hash in the catalog"
	}
	if cached, ok := w.objects[h]; ok {
		return cached
	}
	problem := ""
	size, err := w.m.repo.Stat(h)
	switch {
	case errors.Is(err, repository.ErrMissing):
		problem = problemMissing + " from the repository"
	case err != nil:
		problem = "the repository cannot be read: " + err.Error()
	case size != it.Size:
		problem = fmt.Sprintf("object is damaged (%d bytes, expected %d)", size, it.Size)
	}
	w.objects[h] = problem
	return problem
}

func (w *walker) uniqueName(rel string) string {
	dir, base := path.Split(rel)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	stamp := w.m.now().Format("20060102-150405")
	for i := 0; i < 1000; i++ {
		suffix := " (restored " + stamp + ")"
		if i > 0 {
			suffix = fmt.Sprintf(" (restored %s %d)", stamp, i+1)
		}
		candidate := dir + stem + suffix + ext
		if pathutil.ValidateName(path.Base(candidate)) != nil {
			return ""
		}
		key := strings.ToLower(candidate)
		if _, taken := w.used[key]; taken {
			continue
		}
		if _, err := w.dest.Lstat(filepath.FromSlash(candidate)); err == nil {
			continue
		}
		w.used[key] = struct{}{}
		return candidate
	}
	return ""
}
