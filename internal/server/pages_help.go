package server

import (
	"net/http"

	"timevault/internal/auth"
)

// handleHelp serves the user guide: what the application is for and how every page is used. It is a page of the
// application, with its header and its round buttons, and it opens in a tab of its own from the Documentation button.
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.render(w, r, "help", http.StatusOK, s.newPage(r, sess, "help.title", "help"))
}
