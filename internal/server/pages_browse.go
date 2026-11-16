package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"timevault/internal/auth"
	"timevault/internal/catalog"
	"timevault/internal/pathutil"
	"timevault/internal/service"
)

type sourceOption struct {
	ID       int64
	Name     string
	Selected bool
}

// pickSource returns the sources and the one selected by the "source" parameter
// (the first when absent). selected is nil when no source is registered.
func (s *Server) pickSource(r *http.Request) (opts []sourceOption, selected *catalog.SourceSummary, err error) {
	sources, err := s.svc.Sources(r.Context())
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
	// List holds the controls (type filter, sort links, pager) of the folder listing or of the search results.
	List listView
}

func (s *Server) handleExplorer(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	opts, sel, err := s.pickSource(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "explorer.title", "explorer")
	if sel == nil {
		p.Data = &explorerData{NoSources: true}
		s.render(w, r, "explorer", http.StatusOK, p)
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	d := &explorerData{Sources: opts, Source: sel.Source, Rel: rel, Crumbs: crumbsFor(rel), Latest: sel.Latest}
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
	NoSources      bool
	NoPoints       bool
	Sources        []sourceOption
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

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	opts, sel, err := s.pickSource(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	p := s.newPage(r, sess, "history.title", "history")
	if sel == nil {
		p.Data = &historyData{NoSources: true}
		s.render(w, r, "history", http.StatusOK, p)
		return
	}
	d := &historyData{Sources: opts, Source: sel.Source, RestoreEnabled: s.svc.RestoreEnabled(), DefaultDest: defaultDest(time.Now())}
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
		if point, err = s.svc.Point(r.Context(), sel.ID, id); err != nil {
			s.handleErr(w, r, err)
			return
		}
		if point.State != catalog.StateCommitted {
			s.handleErr(w, r, service.ErrNotFound)
			return
		}
	}
	points, err := s.svc.Points(r.Context(), sel.ID, 80, 0)
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
	listing, err := s.svc.ListHistory(r.Context(), sel.Source, point, rel, lq.options())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	d.Point, d.Latest, d.Rel, d.Crumbs, d.Listing = point, *sel.Latest, rel, crumbsFor(rel), &listing
	s.viewerFor(r.Context(), sess).entries(listing.Entries)
	d.IsLatest = point.ID == sel.Latest.ID
	d.List = p.newListView(lq, listSpec{
		Path: "/history", Fixed: []any{"source", sel.ID, "point", point.ID, "path", rel}, Choices: folderPerPage,
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
	Source         catalog.Source
	Hist           service.FileHistory
	Crumbs         []crumb
	ParentRel      string
	DefaultDest    string
	RestoreEnabled bool
	BackURL        string
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	srcID := intParam(r, "source")
	src, err := s.svc.Source(r.Context(), srcID)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	rel, err := cleanPathParam(r)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	hist, err := s.svc.FileVersions(r.Context(), src, rel)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	parent := pathutil.ParentRel(rel)
	back := buildURL("/explorer", "source", src.ID, "path", parent)
	if ref := s.local(r.FormValue("back")); strings.HasPrefix(ref, "/history") || strings.HasPrefix(ref, "/explorer") {
		if u, err := url.Parse(ref); err == nil && u.Host == "" && u.Scheme == "" {
			back = ref
		}
	}
	p := s.newPage(r, sess, "versions.title", "history")
	p.locate(src, rel)
	s.viewerFor(r.Context(), sess).versions(hist.Versions)
	p.Data = &versionsData{
		Source: src, Hist: hist, Crumbs: crumbsFor(rel), ParentRel: parent,
		DefaultDest: defaultDest(time.Now()), RestoreEnabled: s.svc.RestoreEnabled(), BackURL: back,
	}
	s.render(w, r, "versions", http.StatusOK, p)
}
