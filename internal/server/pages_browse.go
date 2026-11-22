package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"timevault/internal/access"
	"timevault/internal/auth"
	"timevault/internal/catalog"
	"timevault/internal/pathutil"
	"timevault/internal/service"
)

type sourceOption struct {
	ID       int64
	Name     string
	Selected bool
	// Server is the other server the folder is on; empty for a folder of this server. Value is what the folder selector
	// sends for it: the number, or server:number for a folder of another server.
	Server string
	Value  string
}

// sourceGroup is the folders of one server, for the folder selector. Server is empty for this server.
type sourceGroup struct {
	Server  string
	Options []sourceOption
}

// pickSource returns the folders the visitor may browse and the one selected by the "source" parameter
// (the first when absent). selected is nil when there is none to show. A folder that is not among them, hidden
// or not, is "not found".
func (s *Server) pickSource(r *http.Request, sess auth.Session) (opts []sourceOption, selected *catalog.SourceSummary, err error) {
	sources, err := s.visibleSources(r.Context(), sess)
	if err != nil {
		return nil, nil, err
	}
	if len(sources) == 0 {
		return nil, nil, nil
	}
	want := intParam(r, "source")
	idx := 0
	if want != 0 {
		idx = -1
		for i, src := range sources {
			if src.ID == want {
				idx = i
			}
		}
		if idx < 0 {
			return nil, nil, service.ErrNotFound
		}
	}
	for i, src := range sources {
		opts = append(opts, sourceOption{ID: src.ID, Name: src.Name, Selected: i == idx})
	}
	return opts, &sources[idx], nil
}

func cleanPathParam(r *http.Request) (string, error) {
	return pathutil.CleanRel(r.FormValue("path"))
}

// --- Explorer (live) -----------------------------------------------------------------

type explorerData struct {
	NoSources  bool
	Sources    []sourceOption
	Source     catalog.Source
	Rel        string
	Crumbs     []crumb
	Listing    *service.LiveListing
	Latest     *catalog.Point
	Query      string
	Search     *service.SearchPage
	SearchMsg  string
	SourceDown bool
	// OtherServers says that this server is in a group: the page then tells that the folders of the other servers have
	// no live view yet, and where their history is.
	OtherServers bool
	// List holds the controls (type filter, sort links, pager) of the folder listing or of the search results.
	List listView
}

func (s *Server) handleExplorer(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	opts, sel, err := s.pickSource(r, sess)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "explorer.title", "explorer")
	if sel == nil {
		p.Data = &explorerData{NoSources: true, OtherServers: s.opts.Group != nil}
		s.render(w, r, "explorer", http.StatusOK, p)
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	d := &explorerData{Sources: opts, Source: sel.Source, Rel: rel, Crumbs: crumbsFor(rel), Latest: sel.Latest, OtherServers: s.opts.Group != nil}
	p.Data = d
	p.locate(sel.Source, rel)

	if q := strings.TrimSpace(r.FormValue("q")); q != "" {
		d.Query = q
		lq := parseListQuery(r, searchPerPage)
		page, err := s.svc.Search(r.Context(), sel.Source, q, (lq.Page-1)*lq.PerPage, lq.PerPage)
		switch {
		case err == nil:
			d.Search = &page
			d.List = p.newListView(lq, listSpec{
				Path: "/explorer", Fixed: []any{"source", sel.ID, "q", q}, Choices: searchPerPage,
				Total: page.Total, Unfiltered: page.Total,
			})
			if d.List.Pager.Page != lq.Page {
				http.Redirect(w, r, d.List.Pager.LastURL, http.StatusSeeOther)
				return
			}
		case errors.Is(err, service.ErrSearchUnavailable):
			d.SearchMsg = p.T("search.unavailable")
		default:
			if status, _ := classify(err); status == http.StatusBadRequest {
				d.SearchMsg = p.T("search.invalid")
				break
			}
			s.handleErr(w, r, err)
			return
		}
		s.render(w, r, "explorer", http.StatusOK, p)
		return
	}

	lq := parseListQuery(r, folderPerPage)
	listing, err := s.svc.ListLive(r.Context(), sel.Source, rel, lq.options())
	switch {
	case err == nil:
		d.Listing = &listing
		d.List = p.newListView(lq, listSpec{
			Path: "/explorer", Fixed: []any{"source", sel.ID, "path", rel}, Choices: folderPerPage,
			Facets: listing.Types, Total: listing.Total, Unfiltered: listing.Unfiltered,
		})
		if d.List.Pager.Page != lq.Page {
			http.Redirect(w, r, d.List.Pager.LastURL, http.StatusSeeOther)
			return
		}
	case errors.Is(err, service.ErrSourceUnavailable):
		d.SourceDown = true
	default:
		s.handleErr(w, r, err)
		return
	}
	s.render(w, r, "explorer", http.StatusOK, p)
}

// --- Time machine (history) ----------------------------------------------------------

type pointChip struct {
	Point    catalog.Point
	Selected bool
	Latest   bool
}

type historyData struct {
	NoSources bool
	NoPoints  bool
	// Groups are the folders the visitor may browse, by server, for the folder selector. Server is the other server that
	// the selected folder is on, empty for a folder of this server. Unavailable names the servers of the group whose
	// folders could not be listed right now.
	Groups         []sourceGroup
	Server         string
	Unavailable    []string
	Source         catalog.Source
	Chips          []pointChip
	Point          catalog.Point
	Latest         catalog.Point
	Rel            string
	Crumbs         []crumb
	Listing        *service.HistoryList
	DefaultDest    string
	RestoreEnabled bool
	IsLatest       bool
	// List holds the controls (type filter, sort links, pager) of the folder listing.
	List listView
}

func defaultDest(now time.Time) string { return "restore-" + now.Format("20060102-150405") }

// pickFolder returns the folders the visitor may browse, on this server and on the other servers of the group, grouped by
// server for the folder selector, and the folder that the "source" and "server" parameters select: the first folder of
// this server when they say nothing, and then the first one of the other servers. rc is the server of the selection (or
// the one that was asked for, when it has no folder to show) and selected is nil when there is nothing to show. A server
// of the group whose folders cannot be listed is named in unavailable; the one that was asked for has to answer, or its
// error is returned. A folder that is not among those the visitor may browse, hidden or not, is "not found".
func (s *Server) pickFolder(r *http.Request, sess auth.Session) (groups []sourceGroup, rc reach, selected *catalog.SourceSummary, unavailable []string, err error) {
	ctx := r.Context()
	wantServer, wantID := folderRef(r)
	if s.isThisServer(wantServer) {
		wantServer = ""
	}
	type listed struct {
		rc      reach
		sources []catalog.SourceSummary
	}
	local := s.localReach()
	mine, err := s.visibleSourcesAt(ctx, sess, local)
	if err != nil {
		return nil, reach{}, nil, nil, err
	}
	all := []listed{{local, mine}}
	if s.opts.Group != nil {
		entries, err := s.opts.Group.Servers(ctx)
		if err != nil {
			return nil, reach{}, nil, nil, err
		}
		known := wantServer == ""
		for _, e := range entries {
			if e.Self {
				continue
			}
			asked := e.Name == wantServer
			known = known || asked
			prc, err := s.reachFor(ctx, e)
			var list []catalog.SourceSummary
			if err == nil {
				list, err = s.visibleSourcesAt(ctx, sess, prc)
			}
			if err != nil {
				if asked {
					return nil, reach{}, nil, nil, err
				}
				if e.Readable() {
					unavailable = append(unavailable, e.Name)
				}
				continue
			}
			all = append(all, listed{prc, list})
		}
		if !known {
			return nil, reach{}, nil, nil, service.ErrNotFound
		}
	}

	li, si := -1, -1
	switch {
	case wantID != 0:
		for i, l := range all {
			if l.rc.name != wantServer {
				continue
			}
			for j, src := range l.sources {
				if src.ID == wantID {
					li, si = i, j
				}
			}
		}
		if li < 0 {
			return nil, reach{}, nil, nil, service.ErrNotFound
		}
	case wantServer != "":
		for i, l := range all {
			if l.rc.name == wantServer {
				li = i
				if len(l.sources) > 0 {
					si = 0
				}
			}
		}
		if li < 0 {
			return nil, reach{}, nil, nil, service.ErrNotFound
		}
	default:
		for i, l := range all {
			if len(l.sources) > 0 {
				li, si = i, 0
				break
			}
		}
	}

	for i, l := range all {
		if len(l.sources) == 0 {
			continue
		}
		g := sourceGroup{Server: l.rc.name}
		for j, src := range l.sources {
			opt := sourceOption{ID: src.ID, Name: src.Name, Server: l.rc.name, Selected: i == li && j == si, Value: strconv.FormatInt(src.ID, 10)}
			if l.rc.name != "" {
				opt.Value = l.rc.name + ":" + opt.Value
			}
			g.Options = append(g.Options, opt)
		}
		groups = append(groups, g)
	}
	rc = local
	if li >= 0 {
		rc = all[li].rc
		if si >= 0 {
			sel := all[li].sources[si]
			selected = &sel
		}
	}
	return groups, rc, selected, unavailable, nil
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	groups, rc, sel, unavailable, err := s.pickFolder(r, sess)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	ctx := r.Context()
	p := s.newPage(r, sess, "history.title", "history")
	p.Scope = rc.name
	if sel == nil {
		p.Data = &historyData{NoSources: true, Groups: groups, Server: rc.name, Unavailable: unavailable}
		s.render(w, r, "history", http.StatusOK, p)
		return
	}
	d := &historyData{
		Groups: groups, Server: rc.name, Unavailable: unavailable, Source: sel.Source,
		RestoreEnabled: rc.local() && s.svc.RestoreEnabled(), DefaultDest: defaultDest(time.Now()),
	}
	p.Data = d
	if sel.Latest == nil {
		d.NoPoints = true
		s.render(w, r, "history", http.StatusOK, p)
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p.locate(sel.Source, rel)
	point := *sel.Latest
	if id := intParam(r, "point"); id != 0 {
		if point, err = rc.rd.Point(ctx, sel.ID, id); err != nil {
			s.handleErr(w, r, err)
			return
		}
		if point.State != catalog.StateCommitted {
			s.handleErr(w, r, service.ErrNotFound)
			return
		}
	}
	points, err := rc.rd.Points(ctx, sel.ID, 80, 0)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	found := false
	for i := len(points) - 1; i >= 0; i-- {
		pt := points[i]
		if pt.ID == point.ID {
			found = true
		}
		d.Chips = append(d.Chips, pointChip{Point: pt, Selected: pt.ID == point.ID, Latest: pt.ID == sel.Latest.ID})
	}
	if !found {
		d.Chips = append([]pointChip{{Point: point, Selected: true}}, d.Chips...)
	}

	lq := parseListQuery(r, folderPerPage)
	listing, err := rc.rd.ListHistory(ctx, sel.Source, point, rel, lq.options())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	d.Point, d.Latest, d.Rel, d.Crumbs, d.Listing = point, *sel.Latest, rel, crumbsFor(rel), &listing
	s.viewerAt(ctx, sess, rc).entries(listing.Entries)
	d.IsLatest = point.ID == sel.Latest.ID
	d.List = p.newListView(lq, listSpec{
		Path: "/history", Fixed: []any{"source", sel.ID, "server", rc.name, "point", point.ID, "path", rel}, Choices: folderPerPage,
		Facets: listing.Types, Total: listing.Total, Unfiltered: listing.Unfiltered,
	})
	if d.List.Pager.Page != lq.Page {
		http.Redirect(w, r, d.List.Pager.LastURL, http.StatusSeeOther)
		return
	}
	s.render(w, r, "history", http.StatusOK, p)
}

// --- Versions of one path -------------------------------------------------------------

type versionsData struct {
	Source catalog.Source
	// Server is the other server the folder is on; empty for a folder of this server.
	Server         string
	Hist           service.FileHistory
	Crumbs         []crumb
	ParentRel      string
	DefaultDest    string
	RestoreEnabled bool
	BackURL        string
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	ctx := r.Context()
	server, id := folderRef(r)
	rc, err := s.reachOf(ctx, server)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	src, err := s.loadSourceAt(ctx, sess, rc, id, access.Browse)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	hist, err := rc.rd.FileVersions(ctx, src, rel)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	parent := pathutil.ParentRel(rel)
	// The live view is only for the folders of this server; the folder of another server goes back to its history.
	back := buildURL("/explorer", "source", src.ID, "path", parent)
	if !rc.local() {
		back = buildURL("/history", "source", src.ID, "server", rc.name, "path", parent)
	}
	if ref := s.local(r.FormValue("back")); strings.HasPrefix(ref, "/history") || strings.HasPrefix(ref, "/explorer") {
		if u, err := url.Parse(ref); err == nil && u.Host == "" && u.Scheme == "" {
			back = ref
		}
	}
	p := s.newPage(r, sess, "versions.title", "history")
	p.Scope = rc.name
	p.locate(src, rel)
	s.viewerAt(ctx, sess, rc).versions(hist.Versions)
	p.Data = &versionsData{
		Source: src, Server: rc.name, Hist: hist, Crumbs: crumbsFor(rel), ParentRel: parent,
		DefaultDest: defaultDest(time.Now()), RestoreEnabled: rc.local() && s.svc.RestoreEnabled(), BackURL: back,
	}
	s.render(w, r, "versions", http.StatusOK, p)
}
