package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"timevault/internal/access"
	"timevault/internal/auth"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/service"
)

// What a visitor may do with a monitored folder is decided here, one folder at a time. The service layer does
// not know who is asking, so every handler that reaches a folder goes through loadSource (or source), which
// answers "not found" for a folder the visitor may not even browse, and ErrForbidden for one they may browse but
// not use in the way they asked. Handlers must not read a folder with Service.Source or Service.Sources
// directly; TestHandlersReachFoldersThroughTheAccessChecks makes sure of it.

// whoIs describes the visitor of a session for the access package.
func whoIs(sess auth.Session) access.Who {
	return access.Who{SignedIn: sess.SignedIn(), Account: sess.User, Admin: sess.Can(auth.PermManage)}
}

// isAdmin reports whether the visitor is an administrator, who has full control of every folder and sees the
// folders on the server's disk.
func isAdmin(sess auth.Session) bool { return sess.Can(auth.PermManage) }

// levelOn is the level the visitor has on a folder.
func levelOn(sess auth.Session, src catalog.Source) access.Level {
	return src.Access.LevelFor(whoIs(sess))
}

// loadSource returns a folder when the visitor has at least min on it. A folder that the visitor may not browse is
// reported as service.ErrNotFound, the answer for a folder that does not exist, so that nobody can find out which
// hidden folders there are; a folder they may browse but not use that way as service.ErrForbidden.
func (s *Server) loadSource(ctx context.Context, sess auth.Session, id int64, min access.Level) (catalog.Source, error) {
	src, err := s.svc.Source(ctx, id)
	if err != nil {
		return catalog.Source{}, err
	}
	switch level := levelOn(sess, src); {
	case level < access.Browse:
		return catalog.Source{}, service.ErrNotFound
	case level < min:
		return catalog.Source{}, service.ErrForbidden
	}
	return src, nil
}

// source is loadSource for a handler: when the folder cannot be used it answers the request itself and reports false.
func (s *Server) source(w http.ResponseWriter, r *http.Request, sess auth.Session, id int64, min access.Level) (catalog.Source, bool) {
	src, err := s.loadSource(r.Context(), sess, id, min)
	if err != nil {
		s.folderRefused(w, r, sess, id, min, err)
		return catalog.Source{}, false
	}
	return src, true
}

// folderRefused answers a request for a folder that loadSource (or jobSource) did not give: "not found" for a hidden
// one, and an audited 403 for one the visitor may see but not use that way.
func (s *Server) folderRefused(w http.ResponseWriter, r *http.Request, sess auth.Session, id int64, min access.Level, err error) {
	if errors.Is(err, service.ErrForbidden) {
		s.levelDenied(w, r, sess, &id, min)
		return
	}
	s.handleErr(w, r, err)
}

// levelDenied audits and answers a request that the visitor's level on the folder does not allow.
func (s *Server) levelDenied(w http.ResponseWriter, r *http.Request, sess auth.Session, source *int64, min access.Level) {
	s.refused(w, r, sess, source, map[string]any{"reason": "folder_level", "needs": min.String()},
		"errors.forbidden", http.StatusForbidden, "forbidden", "you are not allowed to do this")
}

// visibleSources lists the folders the visitor may browse, with their newest recovery point.
func (s *Server) visibleSources(ctx context.Context, sess auth.Session) ([]catalog.SourceSummary, error) {
	all, err := s.svc.Sources(ctx)
	if err != nil {
		return nil, err
	}
	who := whoIs(sess)
	visible := make([]catalog.SourceSummary, 0, len(all))
	for _, src := range all {
		if src.Access.LevelFor(who) >= access.Browse {
			visible = append(visible, src)
		}
	}
	return visible, nil
}

// restrictTo is the part of a job or audit filter that limits it to the folders the visitor may see: nothing for an
// administrator, who sees everything, otherwise the visible folders.
func restrictTo(sess auth.Session, visible []catalog.SourceSummary) (only bool, ids []int64) {
	if isAdmin(sess) {
		return false, nil
	}
	ids = make([]int64, 0, len(visible))
	for _, src := range visible {
		ids = append(ids, src.ID)
	}
	return true, ids
}

// jobSource finds the folder of a job and checks the visitor's level on it, like loadSource. A job that belongs to
// no folder (none is created today) is for administrators; they get a nil folder.
func (s *Server) jobSource(ctx context.Context, sess auth.Session, job jobs.Job, min access.Level) (*catalog.Source, error) {
	if job.SourceID == nil {
		if isAdmin(sess) {
			return nil, nil
		}
		return nil, service.ErrNotFound
	}
	src, err := s.loadSource(ctx, sess, *job.SourceID, min)
	if err != nil {
		return nil, err
	}
	return &src, nil
}

// errLockout is returned for a change of the lists that takes away the full control of the one who makes it, when
// they have not said that it is meant.
var errLockout = errors.New("this change takes away your own full control of the folder, so you could no longer change it; confirm that it is meant")

// lockout checks a new list against the one who is about to save it. Somebody who is not an administrator and
// would no longer have full control of the folder is asked to confirm: an administrator can always put it right,
// but they may not be at hand. It is a warning, not a ban: handing a folder over is a normal thing to do.
func (s *Server) lockout(sess auth.Session, next access.List, confirmed bool) error {
	if confirmed || isAdmin(sess) {
		return nil
	}
	if next.LevelFor(whoIs(sess)) < access.Full {
		return errLockout
	}
	return nil
}

// stillAllowed is the Authorize of an update of a folder: the grants are looked at once more when the change is
// applied, under the lock of the folder, so a grant that was taken away since the request was checked cannot be
// used (and a change that was refused cannot be put back by a request that was already on its way).
func stillAllowed(sess auth.Session, min access.Level) func(access.List) error {
	who := whoIs(sess)
	return func(current access.List) error {
		switch level := current.LevelFor(who); {
		case level < access.Browse:
			return service.ErrNotFound
		case level < min:
			return service.ErrForbidden
		}
		return nil
	}
}

// accessChangedError says that the grants of a folder are no longer the ones that a form or a request was made from:
// somebody else changed them in between. Current are the grants as they are now.
type accessChangedError struct{ Current access.List }

func (e *accessChangedError) Error() string {
	return "the permissions of this folder were changed by somebody else after you read them; read them again and repeat your change"
}

// authorizeChange is the Authorize of a change of a folder that may replace its grants: stillAllowed, and, when
// rev says which grants the change was made from (their access.List.Digest), a check that they are still the
// grants of the folder. A list that is saved from an old form or an old read would otherwise bring back what somebody
// else has taken away meanwhile, and nothing would show it. Without rev the grants are replaced as they are.
func authorizeChange(sess auth.Session, min access.Level, rev string) func(access.List) error {
	allowed := stillAllowed(sess, min)
	return func(current access.List) error {
		if err := allowed(current); err != nil {
			return err
		}
		if rev != "" && current.Digest() != rev {
			return &accessChangedError{Current: current}
		}
		return nil
	}
}

// mayCancel reports whether the visitor may stop a job of a folder they have the given level on: the folder's
// backups and verifications are for those who may back it up, and whoever started a restore may stop it again.
func mayCancel(sess auth.Session, level access.Level, job jobs.Job) bool {
	if !sess.SignedIn() {
		return false
	}
	return level >= access.Backup || (job.Kind == jobs.KindRestore && job.RequestedBy == sess.User)
}

// rootPathDenied answers a request of somebody who has full control of a folder, but is not an administrator, to
// change the folder on the server's disk: pointing a folder at another place is for administrators.
func (s *Server) rootPathDenied(w http.ResponseWriter, r *http.Request, sess auth.Session, id int64) {
	s.refused(w, r, sess, &id, map[string]any{"reason": "root_path"},
		"errors.forbidden", http.StatusForbidden, "forbidden", "only an administrator can change the folder on the server's disk")
}

// grantsOf turns a list that was entered, in the settings form or through the API, into the grants of a folder whose
// grants are current now. It must be well formed (access.Spec.List), and every account it names that is not named in
// current must be an active account of the user directory, so that only people who exist are given access. Accounts
// that already have a grant stay as they are, so a folder can still be edited while one of them is deactivated or
// the directory cannot be reached.
func (s *Server) grantsOf(ctx context.Context, spec access.Spec, current access.List) (access.List, error) {
	list, err := spec.List()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, id := range current.Accounts() {
		known[id] = true
	}
	var listed map[string]bool
	for _, id := range list.Accounts() {
		if known[id] {
			continue
		}
		// The people of an editor come from the list of accounts, which the directory keeps for a minute: one
		// look at it settles them all. Only an ID that is not in it (an account made since) is asked for itself.
		if listed == nil {
			listed = map[string]bool{}
			accounts, _ := s.directoryAccounts(ctx)
			for _, a := range accounts {
				listed[a.ID] = true
			}
		}
		if listed[id] {
			continue
		}
		if _, err := s.auth.FindAccount(ctx, id); err != nil {
			return nil, accountError(id, err)
		}
	}
	return list, nil
}

// accountError says why an employee ID could not be given access.
func accountError(id string, err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return &access.SpecError{Key: "errors.access_unknown_account", Args: []any{id},
			Msg: fmt.Sprintf("%s is not an active account of the user directory", id)}
	case errors.Is(err, auth.ErrUnavailable), errors.Is(err, auth.ErrNoAccountList):
		return &access.SpecError{Key: "errors.access_directory_down",
			Msg: "the user directory cannot be asked, so accounts cannot be added to the list now"}
	}
	return err
}
