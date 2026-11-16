package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/restore"
)

type previewData struct {
	Plan    restore.Plan
	Token   string
	Idem    string
	Paths   []string
	Policy  string
	Partial bool
	// CanStart is false while blocked items exist and a partial restore is not allowed.
	CanStart bool
	BackURL  string
	// Dest is where the restore would write, as people reach it over the network; DestReal is the folder on the
	// server's disk, for administrators.
	Dest     string
	DestReal string
}

func parseRestoreForm(r *http.Request) restore.Request {
	return restore.Request{
		SourceID:     intParam(r, "source_id"),
		PointID:      intParam(r, "point_id"),
		Paths:        r.PostForm["paths"],
		DestName:     r.PostFormValue("dest_name"),
		Policy:       restore.Policy(r.PostFormValue("policy")),
		AllowPartial: r.PostFormValue("allow_partial") != "",
	}
}

func (s *Server) renderPreview(w http.ResponseWriter, r *http.Request, sess auth.Session, req restore.Request, status int, flashKey string) {
	plan, token, err := s.svc.PreviewRestore(r.Context(), s.actor(r, sess), req)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "restore.title", "history")
	if flashKey != "" {
		p.Flash = &Flash{Kind: "error", Message: p.T(flashKey)}
	}
	view := s.viewerFor(r.Context(), sess)
	plan.Problems = view.problems(plan.Problems)
	data := &previewData{
		Plan: plan, Token: token, Idem: newID() + newID(), Paths: plan.Request.Paths,
		Policy: string(plan.Request.Policy), Partial: plan.Request.AllowPartial,
		CanStart: plan.Totals.Blocked == 0 || plan.Request.AllowPartial,
		BackURL:  buildURL("/history", "source", plan.Request.SourceID, "point", plan.Request.PointID),
	}
	data.Dest, data.DestReal = view.destination(plan.Request.DestName)
	p.Data = data
	p.RequestURI = data.BackURL
	s.render(w, r, "restore_preview", status, p)
}

func (s *Server) handleRestorePreview(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	req := parseRestoreForm(r)
	if r.PostFormValue("scope") == "folder" {
		// "Restore this folder" restores the folder being viewed instead of the ticked items.
		req.Paths = []string{r.PostFormValue("current_path")}
	}
	if strings.TrimSpace(req.DestName) == "" {
		req.DestName = defaultDest(time.Now())
	}
	s.renderPreview(w, r, sess, req, http.StatusOK, "")
}

func (s *Server) handleRestoreStart(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "errors.generic")
		return
	}
	req := parseRestoreForm(r)
	job, err := s.svc.StartRestore(r.Context(), s.actor(r, sess), req, r.PostFormValue("token"), r.PostFormValue("idem"), r.PostFormValue("confirm_overwrite") != "")
	switch {
	case err == nil:
		s.setFlash(w, "ok", "flash.restore_queued")
		http.Redirect(w, r, "/jobs/"+strconv.FormatInt(job.ID, 10), http.StatusSeeOther)
	case errors.Is(err, restore.ErrNotConfirmed), errors.Is(err, restore.ErrStalePreview), errors.Is(err, restore.ErrBlocked):
		_, key := classify(err)
		s.renderPreview(w, r, sess, req, http.StatusUnprocessableEntity, key)
	default:
		s.handleErr(w, r, err)
	}
}

// --- Audit and status -----------------------------------------------------------------------

type auditData struct {
	Events  []audit.Event
	Action  string
	Actions []string
	NextURL string
}

var auditActions = []string{
	audit.ActionLogin, audit.ActionLoginFailed, audit.ActionLogout, audit.ActionSourceCreate, audit.ActionSourceUpdate,
	audit.ActionBackupRequest, audit.ActionBackupFinish, audit.ActionVerifyRequest, audit.ActionVerifyFinish,
	audit.ActionRestorePreview, audit.ActionRestoreRequest, audit.ActionRestoreFinish, audit.ActionJobCancel, audit.ActionAccessDenied,
	audit.ActionRetentionPrune,
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	action := r.FormValue("action")
	known := false
	for _, a := range auditActions {
		if a == action {
			known = true
		}
	}
	if !known {
		action = ""
	}
	before := intParam(r, "before")
	const pageSize = 50
	events, err := s.svc.AuditEvents(r.Context(), audit.Filter{Action: action, BeforeID: before, Limit: pageSize})
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	events = s.viewerFor(r.Context(), sess).audit(events)
	d := &auditData{Events: events, Action: action, Actions: auditActions}
	if len(events) == pageSize {
		d.NextURL = buildURL("/audit", "action", action, "before", events[len(events)-1].ID)
	}
	p := s.newPage(r, sess, "audit.title", "audit")
	p.Data = d
	s.render(w, r, "audit", http.StatusOK, p)
}

type statusData struct {
	Report any
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	report := s.viewerFor(r.Context(), sess).health(s.svc.Health(r.Context()))
	p := s.newPage(r, sess, "status.title", "status")
	p.Data = report
	s.render(w, r, "status", http.StatusOK, p)
}
