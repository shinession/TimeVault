package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/pathutil"
	"timevault/internal/restore"
	"timevault/internal/service"
)

func (s *Server) apiRoutes() {
	m := s.mux
	// Reading is open to everyone.
	m.HandleFunc("GET /api/v1/session", s.open(s.apiSession))
	m.HandleFunc("GET /api/v1/sources", s.openChecked(s.apiSources))
	m.HandleFunc("GET /api/v1/files", s.open(s.apiFiles))
	m.HandleFunc("GET /api/v1/search", s.open(s.apiSearch))
	m.HandleFunc("GET /api/v1/recovery-points", s.open(s.apiPoints))
	m.HandleFunc("GET /api/v1/history/tree", s.open(s.apiHistoryTree))
	m.HandleFunc("GET /api/v1/history/versions", s.open(s.apiHistoryVersions))
	m.HandleFunc("GET /api/v1/jobs/{id}", s.openChecked(s.apiJob))
	// Everything that works on data, and what reveals the server, needs an account.
	m.HandleFunc("GET /api/v1/health", s.need(auth.PermAudit, s.apiHealth))
	m.HandleFunc("GET /api/v1/settings", s.need(auth.PermAudit, s.apiSettings))
	m.HandleFunc("GET /api/v1/audit", s.need(auth.PermAudit, s.apiAudit))
	m.HandleFunc("POST /api/v1/backups", s.need(auth.PermBackup, s.apiStartBackup))
	m.HandleFunc("POST /api/v1/restores/preview", s.need(auth.PermRestore, s.apiRestorePreview))
	m.HandleFunc("POST /api/v1/restores", s.need(auth.PermRestore, s.apiRestoreStart))
	m.HandleFunc("POST /api/v1/sources", s.need(auth.PermManage, s.apiAddSource))
	m.HandleFunc("PATCH /api/v1/sources/{id}", s.need(auth.PermManage, s.apiUpdateSource))
}

// --- JSON plumbing -------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	var e apiError
	e.Error.Code, e.Error.Message = code, message
	writeJSON(w, status, e)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "the request body is not valid for this endpoint")
		return false
	}
	return true
}

// --- DTOs ------------------------------------------------------------------------------

type apiPoint struct {
	ID             int64      `json:"id"`
	SourceID       int64      `json:"source_id"`
	Seq            int        `json:"seq"`
	Kind           string     `json:"kind"`
	State          string     `json:"state"`
	Consistency    string     `json:"consistency"`
	CapturedAt     time.Time  `json:"captured_at"`
	CommittedAt    *time.Time `json:"committed_at,omitempty"`
	Files          int64      `json:"files"`
	Folders        int64      `json:"folders"`
	TotalBytes     int64      `json:"total_bytes"`
	Added          int64      `json:"added"`
	Modified       int64      `json:"modified"`
	Deleted        int64      `json:"deleted"`
	NewObjects     int64      `json:"new_objects"`
	NewObjectBytes int64      `json:"new_object_bytes"`
	NotCaptured    int64      `json:"not_captured"`
	Verification   string     `json:"verification"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	Restorable     bool       `json:"restorable"`
	ManifestSHA256 string     `json:"manifest_sha256,omitempty"`
	FailureReason  string     `json:"failure_reason,omitempty"`
}

func toAPIPoint(p catalog.Point) apiPoint {
	return apiPoint{
		ID: p.ID, SourceID: p.SourceID, Seq: p.Seq, Kind: p.Kind, State: p.State, Consistency: p.Consistency,
		CapturedAt: p.CapturedAt, CommittedAt: p.CommittedAt, Files: p.FileCount, Folders: p.DirCount, TotalBytes: p.TotalBytes,
		Added: p.AddedCount, Modified: p.ModifiedCount, Deleted: p.DeletedCount, NewObjects: p.NewObjectCount,
		NewObjectBytes: p.NewObjectBytes, NotCaptured: p.SkippedCount, Verification: p.Verification, VerifiedAt: p.VerifiedAt,
		Restorable: p.Restorable(), ManifestSHA256: p.ManifestHex(), FailureReason: p.FailureReason,
	}
}

type apiSource struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// RootPath is the folder on the server's disk; anonymous visitors do not get it.
	RootPath        string     `json:"root_path,omitempty"`
	Excludes        []string   `json:"excludes"`
	Enabled         bool       `json:"enabled"`
	ScheduleMinutes *int       `json:"schedule_minutes"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LatestPoint     *apiPoint  `json:"latest_point,omitempty"`
}

func toAPISource(src catalog.Source) apiSource {
	return apiSource{
		ID: src.ID, Name: src.Name, RootPath: src.RootPath, Excludes: src.Excludes, Enabled: src.Enabled,
		ScheduleMinutes: src.ScheduleMinutes, LastRunAt: src.LastRunAt, CreatedAt: src.CreatedAt,
	}
}

type apiEntry struct {
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	IsDir      bool       `json:"is_dir"`
	Size       int64      `json:"size"`
	ModifiedAt *time.Time `json:"modified_at,omitempty"`
	Status     string     `json:"status,omitempty"`
	State      string     `json:"state,omitempty"`
	SkipReason string     `json:"skip_reason,omitempty"`
	SHA256     string     `json:"sha256,omitempty"`
	FirstPoint int        `json:"first_point,omitempty"`
	LastPoint  *int       `json:"last_point,omitempty"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func histEntry(rel string, e catalog.Entry) apiEntry {
	return apiEntry{
		Name: e.Name, Path: pathutil.JoinRel(rel, e.Name), IsDir: e.IsDir, Size: e.Size, ModifiedAt: timePtr(e.ModTime()),
		Status: string(e.Now), State: e.State, SkipReason: e.SkipReason, SHA256: hexOf(e.SHA256), FirstPoint: e.FirstPoint, LastPoint: e.LastPoint,
	}
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }

type apiJob struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	SourceID    *int64          `json:"source_id,omitempty"`
	PointID     *int64          `json:"point_id,omitempty"`
	State       string          `json:"state"`
	Phase       string          `json:"phase"`
	Progress    json.RawMessage `json:"progress,omitempty"`
	Error       string          `json:"error,omitempty"`
	RequestedBy string          `json:"requested_by"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Active      bool            `json:"active"`
}

func toAPIJob(j jobs.Job) apiJob {
	return apiJob{
		ID: j.ID, Kind: j.Kind, SourceID: j.SourceID, PointID: j.PointID, State: j.State, Phase: j.Phase, Progress: j.Progress,
		Error: j.Error, RequestedBy: j.RequestedBy, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, Active: j.Active(),
	}
}

// --- Handlers --------------------------------------------------------------------------------

func (s *Server) apiSession(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if !sess.SignedIn() {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true, "user": sess.User, "name": sess.Name, "role": sess.Role, "csrf_token": sess.CSRF, "auth_disabled": sess.AuthDisabled,
	})
}

// apiSettings describes the running configuration. It holds no secret.
func (s *Server) apiSettings(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	d := s.opts.Deployment
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.opts.Version, "listen": d.ListenAddr, "database": d.Database, "user_directory": d.UserDirectory,
		"repository": d.RepositoryPath, "restore_root": d.RestoreRoot, "env_file": d.EnvFile, "search": d.SearchHost,
		"local_admin": d.LocalAdmin != "", "sign_in_disabled": d.AuthDisabled, "session_ttl": d.SessionTTL.String(), "cookie_secure": d.CookieSecure,
	})
}

func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	rep := s.svc.Health(r.Context())
	type comp struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Detail string `json:"detail,omitempty"`
	}
	out := struct {
		OK         bool      `json:"ok"`
		Components []comp    `json:"components"`
		Queued     int       `json:"jobs_queued"`
		Running    int       `json:"jobs_running"`
		CheckedAt  time.Time `json:"checked_at"`
	}{OK: rep.OK(), Queued: rep.Queued, Running: rep.Running, CheckedAt: rep.CheckedAt}
	for _, c := range rep.Components {
		out.Components = append(out.Components, comp{c.Name, c.State, c.Detail})
	}
	status := http.StatusOK
	if !out.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, out)
}

func (s *Server) apiSources(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	sources, err := s.svc.Sources(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	out := make([]apiSource, 0, len(sources))
	for _, src := range sources {
		a := toAPISource(src.Source)
		if !sess.SignedIn() {
			a.RootPath = ""
		}
		if src.Latest != nil {
			p := toAPIPoint(*src.Latest)
			a.LatestPoint = &p
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out})
}

func (s *Server) apiAddSource(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body struct {
		Name            string   `json:"name"`
		RootPath        string   `json:"root_path"`
		Excludes        []string `json:"excludes"`
		ScheduleMinutes *int     `json:"schedule_minutes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Excludes == nil {
		body.Excludes = backup.DefaultExcludes
	}
	src, err := s.svc.AddSource(r.Context(), s.actor(r, sess), backup.SourceRequest{
		Name: body.Name, RootPath: body.RootPath, Excludes: body.Excludes, ScheduleMinutes: body.ScheduleMinutes,
	})
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/sources")
	writeJSON(w, http.StatusCreated, toAPISource(src))
}

// optionalInt tells a field that is missing from the JSON from an explicit null.
type optionalInt struct {
	Set   bool
	Value *int
}

func (o *optionalInt) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	o.Value = &n
	return nil
}

// apiUpdateSource changes the fields that are present in the body and leaves the others as they are.
func (s *Server) apiUpdateSource(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	cur, err := s.svc.Source(r.Context(), id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	var body struct {
		Name            *string     `json:"name"`
		RootPath        *string     `json:"root_path"`
		Excludes        *[]string   `json:"excludes"`
		Enabled         *bool       `json:"enabled"`
		ScheduleMinutes optionalInt `json:"schedule_minutes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	req := backup.SourceRequest{Name: cur.Name, RootPath: cur.RootPath, Excludes: cur.Excludes, ScheduleMinutes: cur.ScheduleMinutes}
	enabled := cur.Enabled
	if body.Name != nil {
		req.Name = *body.Name
	}
	if body.RootPath != nil {
		req.RootPath = *body.RootPath
	}
	if body.Excludes != nil {
		req.Excludes = *body.Excludes
	}
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if body.ScheduleMinutes.Set {
		req.ScheduleMinutes = body.ScheduleMinutes.Value
	}
	src, err := s.svc.UpdateSource(r.Context(), s.actor(r, sess), id, req, enabled)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPISource(src))
}

func (s *Server) sourceFromQuery(w http.ResponseWriter, r *http.Request) (catalog.Source, bool) {
	src, err := s.svc.Source(r.Context(), intParam(r, "source_id"))
	if err != nil {
		s.handleErr(w, r, err)
		return catalog.Source{}, false
	}
	return src, true
}

func (s *Server) apiFiles(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	src, ok := s.sourceFromQuery(w, r)
	if !ok {
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	l, err := s.svc.BrowseLive(r.Context(), src, rel, intParamDefault(r, "offset", 0, 0, 1<<30), intParamDefault(r, "limit", 200, 1, 1000))
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	entries := make([]apiEntry, 0, len(l.Entries))
	for _, e := range l.Entries {
		entries = append(entries, apiEntry{Name: e.Name, Path: pathutil.JoinRel(rel, e.Name), IsDir: e.IsDir, Size: e.Size, ModifiedAt: timePtr(e.ModTime), Status: string(e.Status)})
	}
	deleted := make([]apiEntry, 0, len(l.Deleted))
	for _, e := range l.Deleted {
		deleted = append(deleted, histEntry(rel, e))
	}
	out := map[string]any{"path": rel, "entries": entries, "total": l.Total, "deleted": deleted}
	if l.NextOffset >= 0 {
		out["next_offset"] = l.NextOffset
	}
	if l.Latest != nil {
		p := toAPIPoint(*l.Latest)
		out["latest_point"] = p
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	src, ok := s.sourceFromQuery(w, r)
	if !ok {
		return
	}
	page, err := s.svc.Search(r.Context(), src, r.FormValue("q"), intParamDefault(r, "offset", 0, 0, 1<<30), intParamDefault(r, "limit", 50, 1, 200))
	if errors.Is(err, service.ErrSearchUnavailable) {
		writeJSONError(w, http.StatusServiceUnavailable, "search_unavailable", "the search engine is not available; browsing and history still work")
		return
	}
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	results := make([]apiEntry, 0, len(page.Hits))
	for _, h := range page.Hits {
		results = append(results, apiEntry{Name: h.Name, Path: h.Rel, IsDir: h.IsDir, Size: h.Size, ModifiedAt: timePtr(h.ModTime)})
	}
	out := map[string]any{"results": results, "total": page.Total}
	if page.NextOffset >= 0 {
		out["next_offset"] = page.NextOffset
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiPoints(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	src, ok := s.sourceFromQuery(w, r)
	if !ok {
		return
	}
	pts, err := s.svc.Points(r.Context(), src.ID, intParamDefault(r, "limit", 50, 1, 500), intParamDefault(r, "before_seq", 0, 0, 1<<30))
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	out := make([]apiPoint, 0, len(pts))
	for _, p := range pts {
		out = append(out, toAPIPoint(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_points": out})
}

func (s *Server) apiHistoryTree(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	src, ok := s.sourceFromQuery(w, r)
	if !ok {
		return
	}
	point, err := s.svc.Point(r.Context(), src.ID, intParam(r, "point_id"))
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if point.State != catalog.StateCommitted {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	l, err := s.svc.BrowseHistory(r.Context(), src, point, rel, r.FormValue("cursor"), intParamDefault(r, "limit", 200, 1, 1000))
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	entries := make([]apiEntry, 0, len(l.Entries))
	for _, e := range l.Entries {
		entries = append(entries, histEntry(rel, e))
	}
	out := map[string]any{"point": toAPIPoint(point), "path": rel, "entries": entries}
	if l.Next != "" {
		out["next_cursor"] = l.Next
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiHistoryVersions(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	src, ok := s.sourceFromQuery(w, r)
	if !ok {
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	h, err := s.svc.FileVersions(r.Context(), src, rel)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	type version struct {
		apiEntry
		PointID       int64      `json:"first_point_id"`
		CapturedAt    time.Time  `json:"captured_at"`
		UntilAt       *time.Time `json:"until,omitempty"`
		Current       bool       `json:"current"`
		ObjectPresent bool       `json:"object_present"`
	}
	out := make([]version, 0, len(h.Versions))
	for _, v := range h.Versions {
		e := histEntry(pathutil.ParentRel(rel), v.Entry)
		e.Name = h.Name
		e.Path = rel
		out = append(out, version{apiEntry: e, PointID: v.FirstPointID, CapturedAt: v.FirstAt, UntilAt: v.LastAt, Current: v.StillCurrent,
			ObjectPresent: len(v.SHA256) == 0 || h.Present[hexOf(v.SHA256)]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "versions": out})
}

func (s *Server) apiStartBackup(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body struct {
		SourceID int64 `json:"source_id"`
		Rehash   bool  `json:"rehash"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	job, created, err := s.svc.StartBackup(r.Context(), s.actor(r, sess), body.SourceID, body.Rehash)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/api/v1/jobs/"+strconv.FormatInt(job.ID, 10))
	writeJSON(w, status, map[string]any{"job": toAPIJob(job), "already_active": !created})
}

func (s *Server) apiJob(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	job, err := s.svc.Job(r.Context(), id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if job.Kind == jobs.KindRestore {
		if _, ok := s.requireSignIn(w, r); !ok {
			return
		}
	}
	var events []jobs.Event
	if sess.SignedIn() {
		events, err = s.svc.JobEvents(r.Context(), id, intParam(r, "after_event"), 200)
		if err != nil {
			s.handleErr(w, r, err)
			return
		}
	} else {
		// Error texts and the log can name folders and settings of the server; only accounts see them.
		job.RequestedBy, job.Error = "", ""
	}
	type ev struct {
		ID      int64     `json:"id"`
		At      time.Time `json:"at"`
		Level   string    `json:"level"`
		Message string    `json:"message"`
	}
	outEvents := make([]ev, 0, len(events))
	for _, e := range events {
		outEvents = append(outEvents, ev{e.ID, e.At, e.Level, e.Message})
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": toAPIJob(job), "events": outEvents})
}

type restoreBody struct {
	SourceID     int64    `json:"source_id"`
	PointID      int64    `json:"point_id"`
	Paths        []string `json:"paths"`
	DestName     string   `json:"dest_name"`
	Policy       string   `json:"policy"`
	AllowPartial bool     `json:"allow_partial"`
}

func (b restoreBody) request() restore.Request {
	return restore.Request{SourceID: b.SourceID, PointID: b.PointID, Paths: b.Paths, DestName: b.DestName, Policy: restore.Policy(b.Policy), AllowPartial: b.AllowPartial}
}

func (s *Server) apiRestorePreview(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body restoreBody
	if !decodeJSON(w, r, &body) {
		return
	}
	plan, token, err := s.svc.PreviewRestore(r.Context(), s.actor(r, sess), body.request())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	type item struct {
		Path    string `json:"path"`
		IsDir   bool   `json:"is_dir"`
		Size    int64  `json:"size"`
		Action  string `json:"action"`
		Target  string `json:"target,omitempty"`
		Problem string `json:"problem,omitempty"`
	}
	conv := func(in []restore.Item) []item {
		out := make([]item, 0, len(in))
		for _, it := range in {
			out = append(out, item{it.Rel, it.IsDir, it.Size, string(it.Action), it.Target, it.Problem})
		}
		return out
	}
	t := plan.Totals
	writeJSON(w, http.StatusOK, map[string]any{
		"destination": plan.DestDir, "point": toAPIPoint(plan.Point), "digest": plan.Digest, "confirmation_token": token,
		"totals": map[string]int64{
			"items": t.Items, "folders": t.Dirs, "files": t.Files, "bytes": t.Bytes, "create": t.Create, "overwrite": t.Overwrite,
			"rename": t.Rename, "skip_same": t.SkipSame, "skip_exists": t.SkipExists, "blocked": t.Blocked,
			"missing_objects": t.Missing, "not_captured": t.NotCaptured,
		},
		"sample": conv(plan.Sample), "problems": conv(plan.Problems),
	})
}

func (s *Server) apiRestoreStart(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body struct {
		restoreBody
		ConfirmationToken string `json:"confirmation_token"`
		IdempotencyKey    string `json:"idempotency_key"`
		ConfirmOverwrite  bool   `json:"confirm_overwrite"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	job, err := s.svc.StartRestore(r.Context(), s.actor(r, sess), body.request(), body.ConfirmationToken, body.IdempotencyKey, body.ConfirmOverwrite)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+strconv.FormatInt(job.ID, 10))
	writeJSON(w, http.StatusAccepted, map[string]any{"job": toAPIJob(job)})
}

func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	events, err := s.svc.AuditEvents(r.Context(), audit.Filter{
		Action: r.FormValue("action"), Actor: r.FormValue("actor"), BeforeID: intParam(r, "before"), Limit: intParamDefault(r, "limit", 50, 1, 500),
	})
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	type ev struct {
		ID         int64          `json:"id"`
		At         time.Time      `json:"at"`
		Actor      string         `json:"actor"`
		Action     string         `json:"action"`
		TargetType string         `json:"target_type,omitempty"`
		TargetID   string         `json:"target_id,omitempty"`
		SourceID   *int64         `json:"source_id,omitempty"`
		Result     string         `json:"result"`
		RemoteAddr string         `json:"remote_addr,omitempty"`
		Detail     map[string]any `json:"detail,omitempty"`
	}
	out := make([]ev, 0, len(events))
	for _, e := range events {
		out = append(out, ev{e.ID, e.At, e.Actor, e.Action, e.TargetType, e.TargetID, e.SourceID, e.Result, e.RemoteAddr, e.Detail})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
