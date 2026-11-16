package server

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"timevault/internal/listing"
)

func queryOf(t *testing.T, rawQuery string, choices []int) listQuery {
	t.Helper()
	return parseListQuery(httptest.NewRequest("GET", "/explorer?"+rawQuery, nil), choices)
}

func TestParseListQueryFallsBackToTheDefaults(t *testing.T) {
	def := listQuery{Sort: listing.SortName, Page: 1, PerPage: defaultPerPage}
	for _, raw := range []string{
		"", "sort=nonsense", "order=sideways", "type=ext:a.b", "type=whatever", "page=0", "page=-3", "page=abc",
		"per_page=7", "per_page=100000", "per_page=abc",
	} {
		if got := queryOf(t, raw, folderPerPage); got != def {
			t.Errorf("%q = %+v, want the defaults %+v", raw, got, def)
		}
	}
	got := queryOf(t, "sort=size&order=desc&type=ext:PDF&page=3&per_page=200", folderPerPage)
	want := listQuery{Sort: listing.SortSize, Desc: true, Type: "ext:pdf", Page: 3, PerPage: 200}
	if got != want {
		t.Errorf("a full query = %+v, want %+v", got, want)
	}
	// A search page holds at most 200 hits, so 500 is not a choice there.
	if got := queryOf(t, "per_page=500", searchPerPage); got.PerPage != defaultPerPage {
		t.Errorf("per_page=500 on a search = %d", got.PerPage)
	}
}

func TestListQueryLeavesDefaultsOutOfTheURL(t *testing.T) {
	href := func(q listQuery) string { return buildURL("/explorer", append([]any{"source", 1}, q.values()...)...) }
	if got := href(listQuery{Sort: listing.SortName, Page: 1, PerPage: defaultPerPage}); got != "/explorer?source=1" {
		t.Errorf("defaults = %q", got)
	}
	got := href(listQuery{Sort: listing.SortType, Desc: true, Type: "ext:pdf", Page: 4, PerPage: 50})
	for _, want := range []string{"sort=type", "order=desc", "type=ext%3Apdf", "page=4", "per_page=50"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not contain %q", got, want)
		}
	}
	if got := href(listQuery{Sort: listing.SortName, Desc: true, Page: 1, PerPage: defaultPerPage}); !strings.Contains(got, "order=desc") || strings.Contains(got, "sort=") {
		t.Errorf("descending by name = %q", got)
	}
}

func TestPagerLayout(t *testing.T) {
	href := func(page int) string { return "/p" + string(rune('0'+page%10)) }
	labels := func(p pager) string {
		var parts []string
		for _, l := range p.Links {
			switch {
			case l.Gap:
				parts = append(parts, "…")
			case l.Current:
				parts = append(parts, "["+l.Label+"]")
			default:
				parts = append(parts, l.Label)
			}
		}
		return strings.Join(parts, " ")
	}
	q := listQuery{Page: 6, PerPage: 100}
	p := newPager(q, 1043, href)
	if p.Pages != 11 || p.Page != 6 || p.From != 501 || p.To != 600 || p.Total != 1043 {
		t.Fatalf("position = %+v", p)
	}
	if got := labels(p); got != "1 … 4 5 [6] 7 8 … 11" {
		t.Errorf("links = %q", got)
	}
	if p.PrevURL == "" || p.NextURL == "" || p.LastURL != href(11) {
		t.Errorf("urls = %q %q %q", p.PrevURL, p.NextURL, p.LastURL)
	}

	first := newPager(listQuery{Page: 1, PerPage: 100}, 1043, href)
	if first.PrevURL != "" || first.NextURL == "" || labels(first) != "[1] 2 3 … 11" {
		t.Errorf("first page = %q prev=%q next=%q", labels(first), first.PrevURL, first.NextURL)
	}
	last := newPager(listQuery{Page: 11, PerPage: 100}, 1043, href)
	if last.NextURL != "" || last.From != 1001 || last.To != 1043 || labels(last) != "1 … 9 10 [11]" {
		t.Errorf("last page = %+v %q", last, labels(last))
	}

	// A page past the end is pulled back to the last one; the caller redirects when Page differs.
	beyond := newPager(listQuery{Page: 99, PerPage: 100}, 1043, href)
	if beyond.Page != 11 {
		t.Errorf("beyond the end = page %d", beyond.Page)
	}

	one := newPager(listQuery{Page: 1, PerPage: 100}, 100, href)
	if one.Pages != 1 || one.From != 1 || one.To != 100 || one.PrevURL != "" || one.NextURL != "" {
		t.Errorf("a full single page = %+v", one)
	}
	empty := newPager(listQuery{Page: 1, PerPage: 100}, 0, href)
	if empty.Pages != 1 || empty.From != 0 || empty.To != 0 {
		t.Errorf("no items = %+v", empty)
	}
}

func queryOfURL(t *testing.T, href string) url.Values {
	t.Helper()
	u, err := url.Parse(href)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestSortLinksToggleTheDirectionAndStartOverOnTheFirstPage(t *testing.T) {
	p := &Page{Lang: "en", loc: &Locale{lang: "en"}}
	q := listQuery{Sort: listing.SortSize, Desc: false, Page: 3, PerPage: 50}
	v := p.newListView(q, listSpec{Path: "/explorer", Fixed: []any{"source", 7, "path", "a/b"}, Choices: folderPerPage, Total: 900, Unfiltered: 900})

	size := v.Sort["size"]
	if got := queryOfURL(t, size.URL); size.Aria != "ascending" || got.Get("sort") != "size" || got.Get("order") != "desc" || got.Has("page") || got.Get("per_page") != "50" {
		t.Errorf("the current column = %+v", size)
	}
	name := v.Sort["name"]
	if got := queryOfURL(t, name.URL); name.Aria != "" || got.Has("sort") || got.Has("order") || got.Has("page") || got.Get("source") != "7" {
		t.Errorf("another column = %+v", name)
	}
	if got := queryOfURL(t, v.Sort["type"].URL); got.Get("sort") != "type" || got.Get("per_page") != "50" || got.Get("path") != "a/b" || got.Has("page") {
		t.Errorf("type column = %v", got)
	}

	q.Desc = true
	got := p.newListView(q, listSpec{Path: "/explorer", Choices: folderPerPage, Total: 900}).Sort["size"]
	if params := queryOfURL(t, got.URL); got.Aria != "descending" || params.Get("sort") != "size" || params.Has("order") {
		t.Errorf("a descending column offers ascending next: %+v", got)
	}
}

func TestListViewCarriesTheFormFields(t *testing.T) {
	p := &Page{Lang: "en", loc: &Locale{lang: "en"}}
	q := listQuery{Sort: listing.SortMTime, Desc: true, Type: "dir", Page: 2, PerPage: 200}
	v := p.newListView(q, listSpec{Path: "/history", Fixed: []any{"source", 1, "point", 9, "path", "", "q", ""}, Choices: folderPerPage, Facets: []listing.TypeCount{{Key: "dir", Count: 2}}, Total: 2, Unfiltered: 2})
	got := map[string]string{}
	for _, h := range v.Hidden {
		got[h.Name] = h.Value
	}
	want := map[string]string{"source": "1", "point": "9", "sort": "mtime", "order": "desc"}
	if len(got) != len(want) {
		t.Fatalf("hidden fields = %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("hidden %s = %q, want %q", k, got[k], w)
		}
	}
	if !v.Filtered || !v.HasTypeFilter || v.Action != "/history" {
		t.Errorf("view = %+v", v)
	}
	var selected int
	for _, o := range v.PerPage {
		if o.Selected {
			selected = o.Value
		}
	}
	if selected != 200 {
		t.Errorf("selected page size = %d", selected)
	}
	if p.newListView(listQuery{Page: 1, PerPage: 100}, listSpec{Path: "/explorer", Choices: searchPerPage, Total: 5}).HasTypeFilter {
		t.Error("a list without facets (search results) must not offer a type filter")
	}
}

func TestTypeOptionsListWhatTheFolderHoldsAndKeepTheCurrentChoice(t *testing.T) {
	en := &Page{Lang: "en", loc: &Locale{lang: "en"}}
	facets := []listing.TypeCount{{Key: "dir", Count: 3}, {Key: "noext", Count: 2}, {Key: "ext:pdf", Count: 40}, {Key: "ext:txt", Count: 5}}
	opts := en.typeOptions(facets, "ext:pdf")
	var labels []string
	for _, o := range opts {
		labels = append(labels, o.Value+"="+o.Label)
		if o.Selected != (o.Value == "ext:pdf") {
			t.Errorf("option %q selected = %v", o.Value, o.Selected)
		}
	}
	want := "=All types|dir=Folders (3)|file=Files (47)|noext=No extension (2)|ext:pdf=PDF (40)|ext:txt=TXT (5)"
	if got := strings.Join(labels, "|"); got != want {
		t.Errorf("options =\n%s\nwant\n%s", got, want)
	}

	// A filter that nothing matches any more stays selectable, so the control does not silently change.
	gone := en.typeOptions(facets, "ext:zip")
	if last := gone[len(gone)-1]; last.Value != "ext:zip" || !last.Selected || last.Label != "ZIP (0)" {
		t.Errorf("a vanished type = %+v", last)
	}

	zh := &Page{Lang: "zh", loc: &Locale{lang: "zh"}}
	if got := zh.TypeOf("a.docx", false, false); got != "DOCX" {
		t.Errorf("extension = %q", got)
	}
	if got := zh.TypeOf("sub", true, false); got != "文件夹" {
		t.Errorf("folder = %q", got)
	}
	if got := en.TypeOf("README", false, false); got != "File" {
		t.Errorf("no extension = %q", got)
	}
	if got := en.TypeOf("x.lnk", false, true); got != "Link" {
		t.Errorf("link = %q", got)
	}
}
