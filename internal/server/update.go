package server

import (
	"net/http"
	"time"

	"timevault/internal/auth"
	"timevault/internal/update"
)

// Updater is what the server needs of the automatic update: what it is doing, and a way to ask it to look for a new
// version now. It is nil when the service does not update itself.
type Updater interface {
	Status() update.Status
	CheckNow()
}

// updateView is what the Settings page and the API say about the automatic update.
type updateView struct {
	Enabled   bool
	State     string
	Current   string
	Latest    string
	CheckedAt time.Time
	Interval  time.Duration
	// Waiting is how many jobs the service waits for before it restarts; Since is when it began to wait.
	Waiting int
	Since   time.Time
	// Address and Error name places on the server (a folder, a share) and may quote paths: only administrators
	// get them, like every other real path of the server.
	Address string
	Error   string
}

func (s *Server) updateView(sess auth.Session) updateView {
	if s.opts.Update == nil {
		return updateView{}
	}
	st := s.opts.Update.Status()
	v := updateView{
		Enabled: true, State: string(st.State), Current: st.Current, Latest: st.Latest, CheckedAt: st.CheckedAt,
		Interval: st.Interval, Waiting: st.Waiting, Since: st.Since,
	}
	if sess.Can(auth.PermManage) {
		v.Address, v.Error = st.Address, st.LastError
	}
	return v
}

// updateNotice is the sentence that tells a signed-in visitor that no new job starts until the service has restarted
// for a new version; empty when there is nothing to tell.
func (s *Server) updateNotice(sess auth.Session, loc *Locale) string {
	if s.opts.Update == nil || !sess.SignedIn() {
		return ""
	}
	if st := s.opts.Update.Status(); st.State == update.StateWaiting {
		return loc.T("update.notice", st.Latest)
	}
	return ""
}

// handleUpdateCheck asks the updater to look at the release now; the page shows the answer once it is in.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if s.opts.Update == nil {
		s.setFlash(w, "error", "flash.update_off")
	} else {
		s.opts.Update.CheckNow()
		s.setFlash(w, "ok", "flash.update_checking")
	}
	http.Redirect(w, r, "/settings#updates", http.StatusSeeOther)
}
