package server

import (
	"context"
	"net/http"
	"slices"
	"time"

	"timevault/internal/auth"
	"timevault/internal/catalog"
	"timevault/internal/cluster"
	"timevault/internal/service"
)

// serverRow is one server of the group, as the Servers page and the API show it.
type serverRow struct {
	cluster.Entry
	// Folders is how many folders of the server the visitor may browse; Counted is false when they could not be counted
	// (the history of the server is not read, or it did not answer).
	Folders int
	Counted bool
	// Latest is when the newest recovery point of those folders was made; nil when there is none.
	Latest *time.Time
}

type serversData struct{ Rows []serverRow }

// serverRows lists the servers of the group for a visitor: this server first, then the others by name, each with the
// number of its folders that the visitor may browse and the newest recovery point among them. A server whose history is
// not read, or that cannot be read right now, is listed without a count.
func (s *Server) serverRows(ctx context.Context, sess auth.Session) ([]serverRow, error) {
	entries, err := s.opts.Group.Servers(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(entries, func(a, b cluster.Entry) int {
		switch {
		case a.Self && !b.Self:
			return -1
		case !a.Self && b.Self:
			return 1
		}
		return 0
	})
	rows := make([]serverRow, 0, len(entries))
	for _, e := range entries {
		row := serverRow{Entry: e}
		rc := s.localReach()
		var err error
		if !e.Self {
			rc, err = s.reachFor(ctx, e)
		}
		var visible []catalog.SourceSummary
		if err == nil {
			visible, err = s.visibleSourcesAt(ctx, sess, rc)
		}
		if err != nil {
			if e.Readable() {
				s.log.Warn("the folders of a server of the group could not be counted", "server", e.Name, "error", err)
			}
			rows = append(rows, row)
			continue
		}
		row.Folders, row.Counted = len(visible), true
		for _, src := range visible {
			if src.Latest == nil {
				continue
			}
			at := src.Latest.CapturedAt
			if src.Latest.CommittedAt != nil {
				at = *src.Latest.CommittedAt
			}
			if row.Latest == nil || at.After(*row.Latest) {
				latest := at
				row.Latest = &latest
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if s.opts.Group == nil {
		s.handleErr(w, r, service.ErrNotFound)
		return
	}
	rows, err := s.serverRows(r.Context(), sess)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "servers.title", "servers")
	p.Data = &serversData{Rows: rows}
	s.render(w, r, "servers", http.StatusOK, p)
}

// apiServer is a server of the group in the API. The register's details of a server (its schema, its read contract, its
// repository and restore folders) are for administrators.
type apiServer struct {
	Name       string     `json:"name"`
	ThisServer bool       `json:"this_server"`
	State      string     `json:"state"`
	Version    string     `json:"version,omitempty"`
	Folders    *int       `json:"folders"`
	LatestAt   *time.Time `json:"latest_recovery_point_at,omitempty"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`

	Schema         string `json:"schema,omitempty"`
	Contract       int    `json:"contract,omitempty"`
	RepositoryID   string `json:"repository_id,omitempty"`
	RepositoryRoot string `json:"repository_root,omitempty"`
	RestoreRoot    string `json:"restore_root,omitempty"`
}

func (s *Server) apiServers(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if s.opts.Group == nil {
		writeJSON(w, http.StatusOK, map[string]any{"group": false, "servers": []apiServer{}})
		return
	}
	rows, err := s.serverRows(r.Context(), sess)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	admin := sess.Can(auth.PermManage)
	out := make([]apiServer, 0, len(rows))
	for _, row := range rows {
		a := apiServer{Name: row.Name, ThisServer: row.Self, State: string(row.State), Version: row.Version, LatestAt: row.Latest, LastSeen: row.LastSeen}
		if row.Counted {
			n := row.Folders
			a.Folders = &n
		}
		if admin {
			a.Schema, a.Contract, a.RepositoryID = row.Schema, row.Contract, row.RepositoryID
			a.RepositoryRoot, a.RestoreRoot = row.RepositoryRoot, row.RestoreRoot
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": true, "this_server": s.opts.Group.Name(), "servers": out})
}
