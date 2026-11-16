package listing_test

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"timevault/internal/listing"
)

type item struct {
	name    string
	dir     bool
	special bool
	size    int64
	mtime   int64
	tag     int // tells apart items the engine sees as equal
}

func view(i item) listing.View {
	return listing.View{Name: i.name, IsDir: i.dir, Special: i.special, Size: i.size, MTimeNS: i.mtime}
}

func names(items []item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.name
	}
	return out
}

func tags(items []item) []int {
	out := make([]int, len(items))
	for i, it := range items {
		out[i] = it.tag
	}
	return out
}

// fixture has two folders, a link whose size must not count, files with and
// without an extension, and names that differ only in letter case.
func fixture() []item {
	return []item{
		{name: "Zeta.txt", size: 30, mtime: 5},
		{name: "alpha.TXT", size: 10, mtime: 9},
		{name: "beta.pdf", size: 20, mtime: 7},
		{name: "Docs", dir: true, mtime: 3},
		{name: "archive", dir: true, mtime: 8},
		{name: "README", size: 10, mtime: 1},
		{name: "link.lnk", special: true, size: 99, mtime: 4},
		{name: "Dockerfile", size: 0, mtime: 0},
		{name: "a.tar.gz", size: 5, mtime: 6},
	}
}

const (
	byNameAsc  = "a.tar.gz alpha.TXT archive beta.pdf Dockerfile Docs link.lnk README Zeta.txt"
	byNameDesc = "Zeta.txt README link.lnk Docs Dockerfile beta.pdf archive alpha.TXT a.tar.gz"
)

func equalNames(t *testing.T, got []item, want string) {
	t.Helper()
	if w := strings.Fields(want); !slices.Equal(names(got), w) {
		t.Errorf("got %v, want %v", names(got), w)
	}
}

func TestExtOf(t *testing.T) {
	tests := []struct{ name, want string }{
		{"report.txt", "txt"},
		{"archive.tar.GZ", "gz"},
		{"UPPER.PDF", "pdf"},
		{".gitignore", "gitignore"},
		{"a.b.c", "c"},
		{"v1.2.3", "3"},
		{"x.ÄÖÜ", "äöü"},
		{"noext", ""},
		{"", ""},
		{"a.", ""},
		{"...", ""},
		{"trailing.dot.", ""},
		{"Report. Final version of the thing", ""},
		{"a.b c", ""},
		{"a.tab\there", ""},
		{"a.no\u00a0break", ""},
		{"x." + strings.Repeat("a", 16), strings.Repeat("a", 16)},
		{"x." + strings.Repeat("a", 17), ""},
		{"x." + strings.Repeat("é", 16), strings.Repeat("é", 16)},
		{"x." + strings.Repeat("é", 17), ""},
		{"notes.thisisaverylongextension", ""},
	}
	for _, tc := range tests {
		if got := listing.ExtOf(tc.name); got != tc.want {
			t.Errorf("ExtOf(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTypeKey(t *testing.T) {
	tests := []struct {
		name           string
		isDir, special bool
		want           string
	}{
		{"a.TXT", false, false, "ext:txt"},
		{".gitignore", false, false, "ext:gitignore"},
		{"README", false, false, "noext"},
		{"a.", false, false, "noext"},
		{"notes.thisisaverylongextension", false, false, "noext"},
		{"Docs", true, false, "dir"},
		{"v1.2", true, false, "dir"},
		{"shortcut.lnk", false, true, "link"},
		{"junction", true, true, "link"},
	}
	for _, tc := range tests {
		got := listing.TypeKey(tc.name, tc.isDir, tc.special)
		if got != tc.want {
			t.Errorf("TypeKey(%q, dir=%v, special=%v) = %q, want %q", tc.name, tc.isDir, tc.special, got, tc.want)
		}
		// Every key a list reports must be usable as a filter in a link.
		if n, ok := listing.NormalizeType(got); !ok || n != got {
			t.Errorf("NormalizeType(%q) = %q, %v; the key of %q must be a valid filter", got, n, ok, tc.name)
		}
	}
}

func TestNormalizeType(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"dir", "dir", true},
		{"file", "file", true},
		{"link", "link", true},
		{"noext", "noext", true},
		{"ext:txt", "ext:txt", true},
		{"ext:TXT", "ext:txt", true},
		{"ext: Txt ", "ext:txt", true},
		{"ext:" + strings.Repeat("a", 16), "ext:" + strings.Repeat("a", 16), true},
		{"ext:", "", false},
		{"ext:   ", "", false},
		{"ext:" + strings.Repeat("a", 17), "", false},
		{"ext:.txt", "", false},
		{"ext:a.b", "", false},
		{"ext:a/b", "", false},
		{`ext:a\b`, "", false},
		{"ext:a b", "", false},
		{"ext:a\tb", "", false},
		{"ext", "", false},
		{"DIR", "", false},
		{" dir", "", false},
		{"folder", "", false},
	}
	for _, tc := range tests {
		got, ok := listing.NormalizeType(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormalizeType(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseSort(t *testing.T) {
	tests := []struct {
		in   string
		want listing.Sort
	}{
		{"name", listing.SortName},
		{"type", listing.SortType},
		{"size", listing.SortSize},
		{"mtime", listing.SortMTime},
		{"", listing.SortName},
		{"Size", listing.SortName},
		{"modified", listing.SortName},
	}
	for _, tc := range tests {
		if got := listing.ParseSort(tc.in); got != tc.want {
			t.Errorf("ParseSort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMatches(t *testing.T) {
	tests := []struct {
		filter, key string
		want        bool
	}{
		{"", "dir", true},
		{"", "ext:txt", true},
		{"dir", "dir", true},
		{"dir", "link", false},
		{"dir", "ext:txt", false},
		{"file", "dir", false},
		{"file", "link", true},
		{"file", "noext", true},
		{"file", "ext:txt", true},
		{"link", "link", true},
		{"link", "dir", false},
		{"noext", "noext", true},
		{"noext", "ext:txt", false},
		{"ext:txt", "ext:txt", true},
		{"ext:TXT", "ext:txt", true},
		{"ext:txt", "ext:pdf", false},
		{"ext:txt", "noext", false},
		{"ext:txt", "dir", false},
		// A filter that is not valid filters nothing, as in Apply.
		{"bogus", "dir", true},
		{"ext:a.b", "ext:txt", true},
	}
	for _, tc := range tests {
		if got := listing.Matches(tc.filter, tc.key); got != tc.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", tc.filter, tc.key, got, tc.want)
		}
	}
}

func TestApplyOrdersEveryColumnInBothDirections(t *testing.T) {
	tests := []struct {
		name string
		opts listing.Options
		want string
	}{
		{"no sort means name", listing.Options{}, byNameAsc},
		{"unknown sort means name", listing.Options{Sort: "weight"}, byNameAsc},
		{"name", listing.Options{Sort: listing.SortName}, byNameAsc},
		{"name desc", listing.Options{Sort: listing.SortName, Desc: true}, byNameDesc},
		{"name, folders first", listing.Options{Sort: listing.SortName, DirsFirst: true},
			"archive Docs a.tar.gz alpha.TXT beta.pdf Dockerfile link.lnk README Zeta.txt"},
		{"name desc, folders first", listing.Options{Sort: listing.SortName, Desc: true, DirsFirst: true},
			"Docs archive Zeta.txt README link.lnk Dockerfile beta.pdf alpha.TXT a.tar.gz"},

		{"type", listing.Options{Sort: listing.SortType},
			"archive Docs link.lnk Dockerfile README a.tar.gz beta.pdf alpha.TXT Zeta.txt"},
		{"type desc", listing.Options{Sort: listing.SortType, Desc: true},
			"Zeta.txt alpha.TXT beta.pdf a.tar.gz README Dockerfile link.lnk Docs archive"},
		{"type, folders first", listing.Options{Sort: listing.SortType, DirsFirst: true},
			"archive Docs link.lnk Dockerfile README a.tar.gz beta.pdf alpha.TXT Zeta.txt"},
		{"type desc, folders first", listing.Options{Sort: listing.SortType, Desc: true, DirsFirst: true},
			"Docs archive Zeta.txt alpha.TXT beta.pdf a.tar.gz README Dockerfile link.lnk"},

		{"size", listing.Options{Sort: listing.SortSize},
			"archive Dockerfile Docs link.lnk a.tar.gz alpha.TXT README beta.pdf Zeta.txt"},
		{"size desc", listing.Options{Sort: listing.SortSize, Desc: true},
			"Zeta.txt beta.pdf README alpha.TXT a.tar.gz link.lnk Docs Dockerfile archive"},
		{"size, folders first", listing.Options{Sort: listing.SortSize, DirsFirst: true},
			"archive Docs Dockerfile link.lnk a.tar.gz alpha.TXT README beta.pdf Zeta.txt"},
		{"size desc, folders first", listing.Options{Sort: listing.SortSize, Desc: true, DirsFirst: true},
			"Docs archive Zeta.txt beta.pdf README alpha.TXT a.tar.gz link.lnk Dockerfile"},

		{"mtime", listing.Options{Sort: listing.SortMTime},
			"Dockerfile README Docs link.lnk Zeta.txt a.tar.gz beta.pdf archive alpha.TXT"},
		{"mtime desc", listing.Options{Sort: listing.SortMTime, Desc: true},
			"alpha.TXT archive beta.pdf a.tar.gz Zeta.txt link.lnk Docs README Dockerfile"},
		{"mtime, folders first", listing.Options{Sort: listing.SortMTime, DirsFirst: true},
			"Docs archive Dockerfile README link.lnk Zeta.txt a.tar.gz beta.pdf alpha.TXT"},
		{"mtime desc, folders first", listing.Options{Sort: listing.SortMTime, Desc: true, DirsFirst: true},
			"archive Docs alpha.TXT beta.pdf a.tar.gz Zeta.txt link.lnk README Dockerfile"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := listing.Apply(fixture(), view, tc.opts)
			equalNames(t, page.Items, tc.want)
			if page.Total != 9 || page.Unfiltered != 9 {
				t.Errorf("Total = %d, Unfiltered = %d, want 9 and 9", page.Total, page.Unfiltered)
			}
		})
	}
}

func TestApplyFiltersByType(t *testing.T) {
	tests := []struct {
		name, filter, want string
	}{
		{"everything", "", byNameAsc},
		{"folders", "dir", "archive Docs"},
		{"files include links and files without extension", "file",
			"a.tar.gz alpha.TXT beta.pdf Dockerfile link.lnk README Zeta.txt"},
		{"links", "link", "link.lnk"},
		{"files without extension", "noext", "Dockerfile README"},
		{"extension", "ext:txt", "alpha.TXT Zeta.txt"},
		{"extension in capitals", "ext:TXT", "alpha.TXT Zeta.txt"},
		{"last extension only", "ext:gz", "a.tar.gz"},
		{"extension nobody has", "ext:none", ""},
		{"unknown filter keeps everything", "bogus", byNameAsc},
		{"invalid extension keeps everything", "ext:a.b", byNameAsc},
	}
	wantTypes := listing.Apply(fixture(), view, listing.Options{}).Types
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := listing.Apply(fixture(), view, listing.Options{Type: tc.filter})
			equalNames(t, page.Items, tc.want)
			if want := len(strings.Fields(tc.want)); page.Total != want || page.Unfiltered != 9 {
				t.Errorf("Total = %d, Unfiltered = %d, want %d and 9", page.Total, page.Unfiltered, want)
			}
			if !slices.Equal(page.Types, wantTypes) {
				t.Errorf("Types = %v, want the facets of the unfiltered list %v", page.Types, wantTypes)
			}
		})
	}
}

func TestApplyCountsTypesOverTheWholeList(t *testing.T) {
	want := []listing.TypeCount{
		{Key: "dir", Count: 2}, {Key: "link", Count: 1}, {Key: "noext", Count: 2},
		{Key: "ext:txt", Count: 2}, {Key: "ext:gz", Count: 1}, {Key: "ext:pdf", Count: 1},
	}
	for _, o := range []listing.Options{{}, {Type: "dir"}, {Type: "ext:pdf", Offset: 5}, {Type: "ext:none"}, {Sort: listing.SortSize, Desc: true, Limit: 1}} {
		if got := listing.Apply(fixture(), view, o).Types; !slices.Equal(got, want) {
			t.Errorf("Types with %+v = %v, want %v", o, got, want)
		}
	}
}

func TestApplyKeepsTheMostCommonTypes(t *testing.T) {
	var items []item
	items = append(items, item{name: "folder", dir: true}, item{name: "plain"})
	for i := range 250 {
		copies := 1
		if i < 10 {
			copies = 3
		}
		for c := range copies {
			items = append(items, item{name: fmt.Sprintf("f%d.e%03d", c, i)})
		}
	}
	page := listing.Apply(items, view, listing.Options{})
	if len(page.Types) != 200 {
		t.Fatalf("len(Types) = %d, want 200", len(page.Types))
	}
	if page.Unfiltered != len(items) || page.Total != len(items) {
		t.Errorf("Total = %d, Unfiltered = %d, want %d: the cap applies to Types only", page.Total, page.Unfiltered, len(items))
	}
	head := []listing.TypeCount{{Key: "dir", Count: 1}, {Key: "noext", Count: 1}}
	for i := range 10 {
		head = append(head, listing.TypeCount{Key: fmt.Sprintf("ext:e%03d", i), Count: 3})
	}
	if !slices.Equal(page.Types[:len(head)], head) {
		t.Errorf("Types start with %v, want %v", page.Types[:len(head)], head)
	}
	// 12 entries above, then single files by extension: e010 up to e197.
	if last := page.Types[199]; last != (listing.TypeCount{Key: "ext:e197", Count: 1}) {
		t.Errorf("last type = %+v, want ext:e197 with 1 file", last)
	}
}

func TestApplyCutsTheWindowAfterFilteringAndSorting(t *testing.T) {
	tests := []struct {
		name          string
		offset, limit int
		want          string
	}{
		{"first page", 0, 4, "a.tar.gz alpha.TXT archive beta.pdf"},
		{"middle page", 4, 4, "Dockerfile Docs link.lnk README"},
		{"short last page", 8, 4, "Zeta.txt"},
		{"offset at the end", 9, 4, ""},
		{"offset past the end", 1000, 4, ""},
		{"huge offset", math.MaxInt, 4, ""},
		{"negative offset", -3, 2, "a.tar.gz alpha.TXT"},
		{"no limit means the default", 0, 0, byNameAsc},
		{"negative limit means the default", 0, -1, byNameAsc},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := listing.Apply(fixture(), view, listing.Options{Offset: tc.offset, Limit: tc.limit})
			if page.Items == nil {
				t.Error("Items is nil")
			}
			equalNames(t, page.Items, tc.want)
			if page.Total != 9 {
				t.Errorf("Total = %d, want 9", page.Total)
			}
		})
	}

	page := listing.Apply(fixture(), view, listing.Options{Type: "file", Sort: listing.SortSize, Desc: true, Offset: 1, Limit: 3})
	equalNames(t, page.Items, "beta.pdf README alpha.TXT")
	if page.Total != 7 || page.Unfiltered != 9 {
		t.Errorf("Total = %d, Unfiltered = %d, want 7 and 9", page.Total, page.Unfiltered)
	}
}

func TestApplyClampsTheLimit(t *testing.T) {
	items := make([]item, 1500)
	for i := range items {
		items[i] = item{name: fmt.Sprintf("file-%04d.txt", i)}
	}
	tests := []struct{ limit, want int }{
		{0, listing.DefaultLimit},
		{-5, listing.DefaultLimit},
		{1, 1},
		{listing.MaxLimit, listing.MaxLimit},
		{listing.MaxLimit + 1, listing.MaxLimit},
		{1 << 30, listing.MaxLimit},
	}
	for _, tc := range tests {
		page := listing.Apply(items, view, listing.Options{Limit: tc.limit})
		if len(page.Items) != tc.want || page.Total != len(items) {
			t.Errorf("Limit %d: %d items of %d, want %d of %d", tc.limit, len(page.Items), page.Total, tc.want, len(items))
		}
	}
}

func TestApplyOfNothing(t *testing.T) {
	for _, o := range []listing.Options{{}, {Type: "dir", Sort: listing.SortSize, Offset: 10, Limit: 5}} {
		page := listing.Apply[item](nil, view, o)
		if page.Items == nil || len(page.Items) != 0 || page.Types == nil || len(page.Types) != 0 || page.Total != 0 || page.Unfiltered != 0 {
			t.Errorf("Apply(nil, %+v) = %+v, want empty, non-nil slices and no counts", o, page)
		}
	}
}

func TestApplyTreatsSpecialItemsAsLinksNeverFolders(t *testing.T) {
	items := []item{{name: "plain.txt", size: 1}, {name: "junction", dir: true, special: true, size: 7}, {name: "real", dir: true}}

	page := listing.Apply(items, view, listing.Options{DirsFirst: true})
	equalNames(t, page.Items, "real junction plain.txt")
	equalNames(t, listing.Apply(items, view, listing.Options{Type: "dir"}).Items, "real")
	equalNames(t, listing.Apply(items, view, listing.Options{Type: "link"}).Items, "junction")
	// The size of the junction counts as 0, so it ties with the folder.
	equalNames(t, listing.Apply(items, view, listing.Options{Sort: listing.SortSize, Desc: true}).Items, "plain.txt real junction")
	want := []listing.TypeCount{{Key: "dir", Count: 1}, {Key: "link", Count: 1}, {Key: "ext:txt", Count: 1}}
	if !slices.Equal(page.Types, want) {
		t.Errorf("Types = %v, want %v", page.Types, want)
	}
}

func TestApplyOrdersFoldersByTheSizeTheCallerKnows(t *testing.T) {
	items := []item{
		{name: "small.txt", size: 5}, {name: "big", dir: true, size: 900}, {name: "mid", dir: true, size: 50},
		{name: "unknown", dir: true}, {name: "link", special: true, size: 99999},
	}
	equalNames(t, listing.Apply(items, view, listing.Options{Sort: listing.SortSize}).Items, "link unknown small.txt mid big")
	equalNames(t, listing.Apply(items, view, listing.Options{Sort: listing.SortSize, Desc: true}).Items, "big mid small.txt unknown link")
	equalNames(t, listing.Apply(items, view, listing.Options{Sort: listing.SortSize, Desc: true, DirsFirst: true}).Items, "big mid unknown small.txt link")
}

func TestApplyTiesFallBackToTheExactName(t *testing.T) {
	items := []item{{name: "a.txt", size: 1}, {name: "A.txt", size: 1}, {name: "b.txt", size: 1}}
	for _, sort := range []listing.Sort{listing.SortName, listing.SortType, listing.SortSize, listing.SortMTime} {
		equalNames(t, listing.Apply(items, view, listing.Options{Sort: sort}).Items, "A.txt a.txt b.txt")
		equalNames(t, listing.Apply(items, view, listing.Options{Sort: sort, Desc: true}).Items, "b.txt a.txt A.txt")
	}
}

// Items the engine cannot tell apart keep their input order in both directions.
func TestApplyKeepsEqualItemsInInputOrder(t *testing.T) {
	items := []item{
		{name: "same.txt", size: 1, tag: 1}, {name: "same.txt", size: 1, tag: 2}, {name: "same.txt", size: 1, tag: 3},
		{name: "same", dir: true, tag: 4}, {name: "same", dir: true, tag: 5},
	}
	for _, sort := range []listing.Sort{listing.SortName, listing.SortType, listing.SortSize, listing.SortMTime} {
		for _, desc := range []bool{false, true} {
			for _, dirsFirst := range []bool{false, true} {
				// Whatever the column, the folders sort before the files, or after them when
				// descending, unless they are pinned to the top.
				want := []int{4, 5, 1, 2, 3}
				if desc && !dirsFirst {
					want = []int{1, 2, 3, 4, 5}
				}
				got := tags(listing.Apply(items, view, listing.Options{Sort: sort, Desc: desc, DirsFirst: dirsFirst}).Items)
				if !slices.Equal(got, want) {
					t.Errorf("sort=%s desc=%v dirsFirst=%v: tags = %v, want %v", sort, desc, dirsFirst, got, want)
				}
			}
		}
	}
}

func TestApplyDoesNotDependOnTheInputOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, sort := range []listing.Sort{"", listing.SortName, listing.SortType, listing.SortSize, listing.SortMTime} {
		for _, desc := range []bool{false, true} {
			for _, dirsFirst := range []bool{false, true} {
				for _, filter := range []string{"", "file"} {
					o := listing.Options{Sort: sort, Desc: desc, DirsFirst: dirsFirst, Type: filter}
					want := names(listing.Apply(fixture(), view, o).Items)
					for range 5 {
						items := fixture()
						rng.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
						if got := names(listing.Apply(items, view, o).Items); !slices.Equal(got, want) {
							t.Fatalf("%+v: shuffled input gives %v, want %v", o, got, want)
						}
					}
				}
			}
		}
	}
}

func TestApplyLeavesTheInputAlone(t *testing.T) {
	items := fixture()
	before := slices.Clone(items)
	for _, sort := range []listing.Sort{listing.SortName, listing.SortType, listing.SortSize, listing.SortMTime} {
		listing.Apply(items, view, listing.Options{Sort: sort, Desc: true, DirsFirst: true, Type: "file", Offset: 1, Limit: 2})
	}
	if !slices.Equal(items, before) {
		t.Errorf("Apply changed the caller's slice: %v, want %v", names(items), names(before))
	}
}

func TestApplyLooksAtEveryItemOnce(t *testing.T) {
	calls := 0
	counting := func(i item) listing.View {
		calls++
		return view(i)
	}
	listing.Apply(fixture(), counting, listing.Options{Sort: listing.SortType, Desc: true, DirsFirst: true, Type: "file", Offset: 2, Limit: 3})
	if calls != len(fixture()) {
		t.Errorf("view was called %d times for %d items", calls, len(fixture()))
	}
}

func TestApplySortsAHundredThousandItemsQuickly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	const n = 100_000
	exts := []string{"txt", "pdf", "docx", "xlsx", "png", "jpg", "zip", "go", "md", "csv", "log", "json", "TXT", "PDF"}
	items := make([]item, n)
	for i := range items {
		it := item{name: fmt.Sprintf("Item-%06d.%s", i*7919%n, exts[i%len(exts)]), size: int64(i % 5000), mtime: int64(i%977) * 1_000_000}
		switch {
		case i%50 == 0:
			it.name, it.dir = fmt.Sprintf("Folder-%d", i), true
		case i%97 == 0:
			it.name = fmt.Sprintf("Plain-%d", i)
		case i%89 == 0:
			it.special = true
		}
		items[i] = it
	}
	start := time.Now()
	page := listing.Apply(items, view, listing.Options{Sort: listing.SortType, DirsFirst: true, Offset: 5000, Limit: 100})
	elapsed := time.Since(start)
	t.Logf("Apply on %d items sorted by type took %v", n, elapsed)
	if elapsed > time.Second {
		t.Errorf("Apply took %v for %d items, want well under a second", elapsed, n)
	}
	if page.Total != n || page.Unfiltered != n || len(page.Items) != 100 || len(page.Types) == 0 {
		t.Errorf("page = %d items of %d (unfiltered %d), %d types", len(page.Items), page.Total, page.Unfiltered, len(page.Types))
	}
}
