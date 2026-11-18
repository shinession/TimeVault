package server

import (
	"strings"

	"timevault/internal/access"
	"timevault/internal/audit"
)

// systemActors are the names the service itself puts in the audit log and on jobs. They are not accounts, so they
// have no picture to look for.
var systemActors = []string{"scheduler", audit.ActorUpdater, "system"}

// avatarID is the employee ID to ask for the picture of, or "" when the name has none: pictures are off, the name is
// not an employee ID, or it is one of the service's own names or the local administrator.
func (s *Server) avatarID(name string) string {
	if s.opts.AvatarURL == "" {
		return ""
	}
	id, ok := access.ValidAccount(name)
	if !ok {
		return ""
	}
	if admin := s.opts.Deployment.LocalAdmin; admin != "" && strings.EqualFold(id, admin) {
		return ""
	}
	for _, actor := range systemActors {
		if strings.EqualFold(id, actor) {
			return ""
		}
	}
	return id
}

// AvatarID is avatarID for a template: the ID the page asks the picture of, "" for no picture. The sign-in of a
// service that runs without accounts is nobody's.
func (p *Page) AvatarID(name string) string {
	if p.avatar == nil || (p.AuthDisabled && name == p.User) {
		return ""
	}
	return p.avatar(name)
}
