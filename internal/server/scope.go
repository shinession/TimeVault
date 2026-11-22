package server

import (
	"context"

	"timevault/internal/cluster"
)

// scopeTab is one server in the tabs that switch a page between this server and the other servers of the group.
type scopeTab struct {
	// Name is the other server; empty for this server.
	Name   string
	Active bool
	// State is how the server looks to this one (online, offline, waiting, incompatible); empty for this server.
	State cluster.State
	// Link says that the history of the server can be read, so the tab leads to its page.
	Link bool
}

// scopeTabs lists this server and the other servers of the group, for the tabs of a page; it is nil when this server is on
// its own, or when the register cannot be read (which is told in the log: the page itself still works).
func (s *Server) scopeTabs(ctx context.Context, rc reach) []scopeTab {
	if s.opts.Group == nil {
		return nil
	}
	entries, err := s.opts.Group.Servers(ctx)
	if err != nil {
		s.log.Warn("the servers of the group could not be listed for the tabs of a page", "error", err)
		return nil
	}
	tabs := []scopeTab{{Active: rc.local(), Link: true}}
	for _, e := range entries {
		if e.Self {
			continue
		}
		tabs = append(tabs, scopeTab{Name: e.Name, Active: e.Name == rc.name, State: e.State, Link: e.Readable()})
	}
	return tabs
}
