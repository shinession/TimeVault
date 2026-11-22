package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"timevault/internal/auth"
	"timevault/internal/cluster"
	"timevault/internal/service"
)

// Several servers can share a database, each in a schema of its own (internal/cluster). A request is about this server
// unless its "server" parameter names another one of the group; then the pages show the history of that server's
// folders, which this server reads from the schema of that server through a read-only pool. What changes something, and
// what needs the disk of the server (the live view, the search), is done on this server only, in this version: a
// request that asks for it for another server is refused (errOtherServer), because it must not be taken for the folder of this server that has the same number.

// reach is the server that a request is about. It only has a Reader: nothing of another server can be changed through it.
type reach struct {
	// name is the name of the other server; empty for this one.
	name string
	rd   *service.Reader
	// entry is how the other server looks (its version, its folders, whether it is online); zero for this server.
	entry cluster.Entry
}

func (rc reach) local() bool { return rc.name == "" }

// errOtherServer is returned for something that is only done on this server in this version, when the request is about
// another server's folder.
var errOtherServer = errors.New("this is not available for the folders of other servers in this version")

// localReach is this server.
func (s *Server) localReach() reach { return reach{rd: s.svc.Reader} }

// serverParam is the name of the server a request is about, from its "server" parameter; empty means this server.
func serverParam(r *http.Request) string { return strings.TrimSpace(r.FormValue("server")) }

// isThisServer says whether name is this server: empty, or the name that this server has in the register.
func (s *Server) isThisServer(name string) bool {
	return name == "" || (s.opts.Group != nil && name == s.opts.Group.Name())
}

// reachOf resolves the server a request is about. A server that is not in the register, or when there is no group at all,
// is "not found". Another server whose history cannot be read gives the errors of cluster.Group.Reader.
func (s *Server) reachOf(ctx context.Context, name string) (reach, error) {
	if s.isThisServer(name) {
		return s.localReach(), nil
	}
	if s.opts.Group == nil {
		return reach{}, service.ErrNotFound
	}
	rd, entry, err := s.opts.Group.Reader(ctx, name)
	if err != nil {
		return reach{}, err
	}
	return reach{name: name, rd: rd, entry: entry}, nil
}

// reachFor is the reach of another server of the group, for an entry that the register gave.
func (s *Server) reachFor(ctx context.Context, e cluster.Entry) (reach, error) {
	if s.opts.Group == nil {
		return reach{}, service.ErrNotFound
	}
	rd, err := s.opts.Group.ReaderOf(ctx, e)
	if err != nil {
		return reach{}, err
	}
	return reach{name: e.Name, rd: rd, entry: e}, nil
}

// reachOfRequest is reachOf for the server that the "server" parameter of a request names.
func (s *Server) reachOfRequest(r *http.Request) (reach, error) {
	return s.reachOf(r.Context(), serverParam(r))
}

// folderRef is the folder a request names: the server it is on ("" for this one) and its number there. The server comes
// from the "server" parameter, or from the "source" parameter written as server:number, which is what the folder selector
// of the pages sends.
func folderRef(r *http.Request) (server string, id int64) {
	server = serverParam(r)
	raw := strings.TrimSpace(r.FormValue("source"))
	if name, num, ok := strings.Cut(raw, ":"); ok {
		server, raw = strings.TrimSpace(name), num
	}
	id, _ = strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return server, id
}

// thisServerOnly wraps a handler that only works on this server: a request that names another server is refused before the
// handler runs, so that the number of a folder of another server is never taken for the folder of this server that has
// the same number.
func (s *Server) thisServerOnly(next sessionHandler) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, sess auth.Session) {
		if !s.isThisServer(serverParam(r)) {
			s.handleErr(w, r, errOtherServer)
			return
		}
		next(w, r, sess)
	}
}

// physicalAt says whether the visitor may see the folders on the disk of the server of a reach: administrators of this
// server on its own folders, and the administrators of the user directory on the folders of another server. The local
// administrator is not known on another server.
func physicalAt(sess auth.Session, rc reach) bool {
	if rc.local() {
		return sess.Can(auth.PermManage)
	}
	return sess.Can(auth.PermManage) && !sess.Can(auth.PermSite)
}
