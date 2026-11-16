package server

import (
	"fmt"
	"net/http"
	"strings"

	"timevault/internal/listing"
)

// defaultPerPage is the page size of a file list when the URL names none.
const defaultPerPage = 100

// folderPerPage and searchPerPage are the page sizes the pager offers. A search page holds at most 200 hits.
var (
	folderPerPage = []int{50, 100, 200, 500}
	searchPerPage = []int{50, 100, 200}
)

// listQuery holds the controls of a file list as the URL carries them. Everything
// is validated, so a hand-made URL falls back to the defaults instead of failing.
type listQuery struct {
	Sort    listing.Sort
	Desc    bool
	Type    string
	Page    int
	PerPage int
}

func parseListQuery(r *http.Request, choices []int) listQuery {
	q := listQuery{
		Sort:    listing.ParseSort(r.FormValue("sort")),
		Desc:    r.FormValue("order") == "desc",
		Page:    intParamDefault(r, "page", 1, 1, 1<<20),
		PerPage: defaultPerPage,
	}
	if typ, ok := listing.NormalizeType(r.FormValue("type")); ok {
		q.Type = typ
	}
	if n := intParamDefault(r, "per_page", defaultPerPage, 1, 1000); containsInt(choices, n) {
		q.PerPage = n
	}
	return q
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

// options is the request to the list engine for the page the query names.
func (q listQuery) options() listing.Options {
	return listing.Options{
		Sort: q.Sort, Desc: q.Desc, Type: q.Type, DirsFirst: true,
		Offset: (q.Page - 1) * q.PerPage, Limit: q.PerPage,
	}
}

// values are the URL parameters of the query; defaults are left out (see buildURL).
func (q listQuery) values() []any {
	sortKey, order, page, perPage := "", "", 0, 0
	if q.Sort != listing.SortName {
		sortKey = string(q.Sort)
	}
	if q.Desc {
		order = "desc"
	}
	if q.Page > 1 {
		page = q.Page
	}
	if q.PerPage != defaultPerPage {
		perPage = q.PerPage
	}
	return []any{"sort", sortKey, "order", order, "type", q.Type, "page", page, "per_page", perPage}
}

type hiddenField struct{ Name, Value string }

// sortHead is the link of one column header.
type sortHead struct {
	URL string
	// Aria is "ascending" or "descending" for the column the list is sorted by, else "".
	Aria string
}

type typeOption struct {
	Value, Label string
	Selected     bool
}

type perPageOption struct {
	Value    int
	Selected bool
}

type pageLink struct {
	Label   string
	URL     string
	Current bool
	Gap     bool
}

// pager is the position in a list and the links around it.
type pager struct {
	Page, Pages, PerPage, Total, From, To int
	PrevURL, NextURL, LastURL             string
	Links                                 []pageLink
}

// listView is everything a template needs to draw the controls of a file list.
type listView struct {
	Action     string
	Hidden     []hiddenField
	Query      listQuery
	Total      int
	Unfiltered int
	Filtered   bool
	Pager      pager
	Sort       map[string]sortHead
	Types      []typeOption
	PerPage    []perPageOption
	// HasTypeFilter is false for lists that cannot be filtered by type (search results).
	HasTypeFilter bool
}

// listSpec describes one list for newListView.
type listSpec struct {
	// Path is the page of the list, for example /explorer.
	Path string
	// Fixed are key/value pairs that identify the list (source, folder, point, search text); they travel with every link.
	Fixed   []any
	Choices []int
	// Facets are the types present in the folder; nil switches the type filter off.
	Facets            []listing.TypeCount
	Total, Unfiltered int
}

func (p *Page) newListView(q listQuery, spec listSpec) listView {
	href := func(q listQuery) string {
		return buildURL(spec.Path, append(append([]any{}, spec.Fixed...), q.values()...)...)
	}
	v := listView{
		Action: spec.Path, Query: q, Total: spec.Total, Unfiltered: spec.Unfiltered,
		Filtered: q.Type != "", HasTypeFilter: spec.Facets != nil,
	}
	for i := 0; i+1 < len(spec.Fixed); i += 2 {
		name, _ := spec.Fixed[i].(string)
		if text := fmt.Sprint(spec.Fixed[i+1]); name != "" && text != "" && text != "0" {
			v.Hidden = append(v.Hidden, hiddenField{name, text})
		}
	}
	if q.Sort != listing.SortName {
		v.Hidden = append(v.Hidden, hiddenField{"sort", string(q.Sort)})
	}
	if q.Desc {
		v.Hidden = append(v.Hidden, hiddenField{"order", "desc"})
	}

	v.Pager = newPager(q, spec.Total, func(page int) string {
		next := q
		next.Page = page
		return href(next)
	})
	v.Sort = map[string]sortHead{}
	for _, key := range []listing.Sort{listing.SortName, listing.SortType, listing.SortSize, listing.SortMTime} {
		next := q
		next.Page = 1
		head := sortHead{}
		if q.Sort == key {
			next.Desc = !q.Desc
			head.Aria = "ascending"
			if q.Desc {
				head.Aria = "descending"
			}
		} else {
			next.Sort, next.Desc = key, false
		}
		head.URL = href(next)
		v.Sort[string(key)] = head
	}
	if spec.Facets != nil {
		v.Types = p.typeOptions(spec.Facets, q.Type)
	}
	for _, n := range spec.Choices {
		v.PerPage = append(v.PerPage, perPageOption{Value: n, Selected: n == q.PerPage})
	}
	return v
}

// newPager lays out the page links: the first and last page and two pages on each side of the current one.
func newPager(q listQuery, total int, href func(page int) string) pager {
	p := pager{PerPage: q.PerPage, Total: total, Pages: max(1, (total+q.PerPage-1)/q.PerPage)}
	p.Page = min(q.Page, p.Pages)
	p.LastURL = href(p.Pages)
	if total > 0 {
		p.From = (p.Page-1)*q.PerPage + 1
		p.To = min(p.Page*q.PerPage, total)
	}
	if p.Page > 1 {
		p.PrevURL = href(p.Page - 1)
	}
	if p.Page < p.Pages {
		p.NextURL = href(p.Page + 1)
	}
	last := 0
	for n := 1; n <= p.Pages; n++ {
		if n != 1 && n != p.Pages && (n < p.Page-2 || n > p.Page+2) {
			continue
		}
		if last != 0 && n > last+1 {
			p.Links = append(p.Links, pageLink{Gap: true})
		}
		p.Links = append(p.Links, pageLink{Label: fmt.Sprint(n), URL: href(n), Current: n == p.Page})
		last = n
	}
	return p
}

// typeLabel is the name of a type key in the interface language: extensions are shown in capitals.
func (p *Page) typeLabel(key string) string {
	switch {
	case key == "dir":
		return p.T("type.dir")
	case key == "link":
		return p.T("type.link")
	case key == "noext":
		return p.T("type.noext")
	case strings.HasPrefix(key, "ext:"):
		return strings.ToUpper(key[len("ext:"):])
	}
	return key
}

// TypeOf is the text of the Type column for an item.
func (p *Page) TypeOf(name string, isDir, special bool) string {
	return p.typeLabel(listing.TypeKey(name, isDir, special))
}

// typeOptions are the entries of the type filter: every type present in the folder with its count (folders, then
// all files, then each kind of file), and the current choice even when nothing of that type is left.
func (p *Page) typeOptions(facets []listing.TypeCount, selected string) []typeOption {
	label := func(key string) string {
		switch key {
		case "file":
			return p.T("type.files")
		case "dir":
			return p.T("type.folders")
		case "noext":
			return p.T("type.no_extension")
		}
		return p.typeLabel(key)
	}
	files, folders := 0, 0
	for _, f := range facets {
		if f.Key == "dir" {
			folders = f.Count
		} else {
			files += f.Count
		}
	}
	opts := []typeOption{{Value: "", Label: p.T("type.all")}}
	seen := map[string]bool{"": true}
	add := func(key string, n int) {
		seen[key] = true
		opts = append(opts, typeOption{Value: key, Label: fmt.Sprintf("%s (%d)", label(key), n)})
	}
	if folders > 0 {
		add("dir", folders)
	}
	if files > 0 {
		add("file", files)
	}
	for _, f := range facets {
		if f.Key != "dir" {
			add(f.Key, f.Count)
		}
	}
	if !seen[selected] {
		opts = append(opts, typeOption{Value: selected, Label: fmt.Sprintf("%s (0)", label(selected))})
	}
	for i := range opts {
		opts[i].Selected = opts[i].Value == selected
	}
	return opts
}
