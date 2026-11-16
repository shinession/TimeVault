package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"timevault/internal/catalog"
	"timevault/internal/listing"
	"timevault/internal/pathutil"
)

// longTail has a "tail" after its last dot that is too long to be an extension.
const longTail = "memo.thisextensionistoolong"

func liveNames(l LiveListing) []string {
	out := make([]string, len(l.Entries))
	for i, e := range l.Entries {
		out[i] = e.Name
	}
	return out
}

func deletedNames(l LiveListing) []string {
	out := make([]string, len(l.Deleted))
	for i, e := range l.Deleted {
		out[i] = e.Name
	}
	return out
}

func historyNames(l HistoryList) []string {
	out := make([]string, len(l.Entries))
	for i, e := range l.Entries {
		out[i] = e.Name
	}
	return out
}

// putSized writes a source file of a given size. The write order sets the
// modification times, oldest first.
func (h *harness) putSized(rel string, size int) {
	h.t.Helper()
	h.env.Write(rel, strings.Repeat("x", size))
}

// changedFolder is a source that was backed up once and changed afterwards:
// b.txt is modified, new.txt is added, gone.TXT and oldfolder are deleted.
func changedFolder(t *testing.T) (*harness, catalog.Point) {
	t.Helper()
	h := newHarness(t)
	h.putSized("README", 6)
	h.putSized("c.pdf", 11)
	h.putSized("a.TXT", 5)
	h.putSized(longTail, 1)
	h.putSized("docs/inner.md", 3)
	h.putSized("b.txt", 2)
	h.putSized("gone.TXT", 4)
	h.putSized("oldfolder/x.txt", 4)
	first := h.env.Backup()
	h.putSized("b.txt", 20)
	h.putSized("new.txt", 13)
	h.env.Remove("gone.TXT")
	h.env.Remove("oldfolder")
	return h, first
}

func TestListLive(t *testing.T) {
	ctx := context.Background()
	h, first := changedFolder(t)
	svc := h.service(nil)
	src := h.env.Src

	t.Run("orders, filters and pages", func(t *testing.T) {
		wantTypes := []listing.TypeCount{
			{Key: "dir", Count: 1}, {Key: "noext", Count: 2}, {Key: "ext:txt", Count: 3}, {Key: "ext:pdf", Count: 1},
		}
		tests := []struct {
			name  string
			opts  listing.Options
			want  []string
			total int
			next  int
		}{
			{"by name", listing.Options{},
				[]string{"a.TXT", "b.txt", "c.pdf", "docs", longTail, "new.txt", "README"}, 7, -1},
			{"by type", listing.Options{Sort: listing.SortType},
				[]string{"docs", longTail, "README", "c.pdf", "a.TXT", "b.txt", "new.txt"}, 7, -1},
			{"by type descending", listing.Options{Sort: listing.SortType, Desc: true},
				[]string{"new.txt", "b.txt", "a.TXT", "c.pdf", "README", longTail, "docs"}, 7, -1},
			{"by type descending, folders first", listing.Options{Sort: listing.SortType, Desc: true, DirsFirst: true},
				[]string{"docs", "new.txt", "b.txt", "a.TXT", "c.pdf", "README", longTail}, 7, -1},
			{"by size", listing.Options{Sort: listing.SortSize},
				[]string{"docs", longTail, "a.TXT", "README", "c.pdf", "new.txt", "b.txt"}, 7, -1},
			{"by size descending", listing.Options{Sort: listing.SortSize, Desc: true},
				[]string{"b.txt", "new.txt", "c.pdf", "README", "a.TXT", longTail, "docs"}, 7, -1},
			{"files by modification time", listing.Options{Sort: listing.SortMTime, Type: "file"},
				[]string{"README", "c.pdf", "a.TXT", longTail, "b.txt", "new.txt"}, 6, -1},
			{"files by modification time descending", listing.Options{Sort: listing.SortMTime, Type: "file", Desc: true},
				[]string{"new.txt", "b.txt", longTail, "a.TXT", "c.pdf", "README"}, 6, -1},
			{"extension", listing.Options{Type: "ext:txt"}, []string{"a.TXT", "b.txt", "new.txt"}, 3, -1},
			{"extension in capitals", listing.Options{Type: "ext:TXT"}, []string{"a.TXT", "b.txt", "new.txt"}, 3, -1},
			{"folders", listing.Options{Type: "dir"}, []string{"docs"}, 1, -1},
			{"files", listing.Options{Type: "file"},
				[]string{"a.TXT", "b.txt", "c.pdf", longTail, "new.txt", "README"}, 6, -1},
			{"files without extension", listing.Options{Type: "noext"}, []string{longTail, "README"}, 2, -1},
			{"links", listing.Options{Type: "link"}, nil, 0, -1},
			{"first page", listing.Options{Sort: listing.SortType, Limit: 3}, []string{"docs", longTail, "README"}, 7, 3},
			{"second page", listing.Options{Sort: listing.SortType, Limit: 3, Offset: 3}, []string{"c.pdf", "a.TXT", "b.txt"}, 7, 6},
			{"last page", listing.Options{Sort: listing.SortType, Limit: 3, Offset: 6}, []string{"new.txt"}, 7, -1},
			{"past the end", listing.Options{Offset: 100}, nil, 7, -1},
			{"page of a filtered list", listing.Options{Type: "ext:txt", Sort: listing.SortSize, Desc: true, Limit: 2},
				[]string{"b.txt", "new.txt"}, 3, 2},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got, err := svc.ListLive(ctx, src, "", tc.opts)
				if err != nil {
					t.Fatal(err)
				}
				if names := liveNames(got); !slices.Equal(names, tc.want) {
					t.Errorf("entries = %v, want %v", names, tc.want)
				}
				if got.Total != tc.total || got.Unfiltered != 7 || got.NextOffset != tc.next {
					t.Errorf("Total = %d, Unfiltered = %d, NextOffset = %d, want %d, 7 and %d", got.Total, got.Unfiltered, got.NextOffset, tc.total, tc.next)
				}
				if !slices.Equal(got.Types, wantTypes) {
					t.Errorf("Types = %v, want %v of the whole folder", got.Types, wantTypes)
				}
			})
		}
	})

	t.Run("keeps the status of every entry", func(t *testing.T) {
		got, err := svc.ListLive(ctx, src, "", listing.Options{Sort: listing.SortSize, Desc: true})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]LiveStatus{
			"a.TXT": LiveProtected, "b.txt": LiveChanged, "c.pdf": LiveProtected, "docs": LiveProtected,
			longTail: LiveProtected, "new.txt": LiveNew, "README": LiveProtected,
		}
		if len(got.Entries) != len(want) {
			t.Fatalf("entries = %v", liveNames(got))
		}
		for _, e := range got.Entries {
			if e.Status != want[e.Name] {
				t.Errorf("%s: status %q, want %q", e.Name, e.Status, want[e.Name])
			}
		}
		if got.Latest == nil || got.Latest.Seq != first.Seq {
			t.Errorf("Latest = %+v, want the first point", got.Latest)
		}
	})

	t.Run("lists what was deleted by the same filter", func(t *testing.T) {
		tests := []struct {
			filter string
			want   []string
		}{
			{"", []string{"gone.TXT", "oldfolder"}},
			{"ext:txt", []string{"gone.TXT"}},
			{"ext:TXT", []string{"gone.TXT"}},
			{"dir", []string{"oldfolder"}},
			{"file", []string{"gone.TXT"}},
			{"noext", nil},
			{"ext:pdf", nil},
			{"link", nil},
			{"not a filter", []string{"gone.TXT", "oldfolder"}},
		}
		for _, tc := range tests {
			// Neither the order nor the window of the page change what was deleted.
			got, err := svc.ListLive(ctx, src, "", listing.Options{Type: tc.filter, Sort: listing.SortSize, Desc: true, Offset: 5, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if names := deletedNames(got); !slices.Equal(names, tc.want) || got.DeletedMore {
				t.Errorf("filter %q: deleted = %v (more: %v), want %v", tc.filter, names, got.DeletedMore, tc.want)
			}
		}
	})

	t.Run("lists a subfolder", func(t *testing.T) {
		got, err := svc.ListLive(ctx, src, "docs", listing.Options{Sort: listing.SortType})
		if err != nil {
			t.Fatal(err)
		}
		if names := liveNames(got); !slices.Equal(names, []string{"inner.md"}) || got.Rel != "docs" || got.Total != 1 || got.Unfiltered != 1 {
			t.Errorf("docs = %v (rel %q, %d of %d)", names, got.Rel, got.Total, got.Unfiltered)
		}
		if want := []listing.TypeCount{{Key: "ext:md", Count: 1}}; !slices.Equal(got.Types, want) {
			t.Errorf("Types = %v, want %v", got.Types, want)
		}
	})

	t.Run("refuses what it cannot list", func(t *testing.T) {
		for _, rel := range []string{"missing", "docs/inner.md", "oldfolder"} {
			if _, err := svc.ListLive(ctx, src, rel, listing.Options{}); !errors.Is(err, ErrNotFound) {
				t.Errorf("ListLive(%q) = %v, want ErrNotFound", rel, err)
			}
		}
		if _, err := svc.ListLive(ctx, src, "../outside", listing.Options{}); !errors.Is(err, pathutil.ErrUnsafePath) {
			t.Errorf("ListLive(../outside) = %v, want ErrUnsafePath", err)
		}
		gone := src
		gone.RootPath = filepath.Join(t.TempDir(), "absent")
		if _, err := svc.ListLive(ctx, gone, "", listing.Options{}); !errors.Is(err, ErrSourceUnavailable) {
			t.Errorf("ListLive of a folder that is not there = %v, want ErrSourceUnavailable", err)
		}
	})

	t.Run("BrowseLive lists by name without grouping folders", func(t *testing.T) {
		all := []string{"a.TXT", "b.txt", "c.pdf", "docs", longTail, "new.txt", "README"}
		tests := []struct {
			name          string
			offset, limit int
			want          []string
			next          int
		}{
			{"everything", 0, 0, all, -1},
			{"first page", 0, 3, all[:3], 3},
			{"middle page", 2, 3, all[2:5], 5},
			{"limit over 1000 means 200", 5, 5000, all[5:], -1},
			{"negative offset", -4, 2, all[:2], 2},
			{"past the end", 7, 10, nil, -1},
			{"negative limit means 200", 0, -1, all, -1},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got, err := svc.BrowseLive(ctx, src, "", tc.offset, tc.limit)
				if err != nil {
					t.Fatal(err)
				}
				if names := liveNames(got); !slices.Equal(names, tc.want) {
					t.Errorf("entries = %v, want %v", names, tc.want)
				}
				if got.Total != 7 || got.Unfiltered != 7 || got.NextOffset != tc.next {
					t.Errorf("Total = %d, Unfiltered = %d, NextOffset = %d, want 7, 7 and %d", got.Total, got.Unfiltered, got.NextOffset, tc.next)
				}
				if names := deletedNames(got); !slices.Equal(names, []string{"gone.TXT", "oldfolder"}) {
					t.Errorf("deleted = %v", names)
				}
				if len(tc.want) == 0 && got.Entries != nil {
					t.Errorf("Entries = %v, want nil when the page is empty", got.Entries)
				}

				limit := tc.limit
				if limit <= 0 || limit > 1000 {
					limit = 200
				}
				same, err := svc.ListLive(ctx, src, "", listing.Options{Sort: listing.SortName, Offset: tc.offset, Limit: limit})
				if err != nil || !reflect.DeepEqual(got, same) {
					t.Errorf("ListLive with the same options differs from BrowseLive: %+v, %v", same, err)
				}
			})
		}
	})
}

func TestBrowseLiveCutsPagesAtTheLimitRules(t *testing.T) {
	h := newHarness(t)
	for i := range 250 {
		h.putSized(fmt.Sprintf("%03d.txt", i), 1)
	}
	svc := h.service(nil)
	tests := []struct {
		limit, wantLen, wantNext int
	}{
		{0, 200, 200},
		{-1, 200, 200},
		{5000, 200, 200},
		{1001, 200, 200},
		{1000, 250, -1},
		{10, 10, 10},
	}
	for _, tc := range tests {
		got, err := svc.BrowseLive(context.Background(), h.env.Src, "", 0, tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Entries) != tc.wantLen || got.NextOffset != tc.wantNext || got.Total != 250 || got.Latest != nil {
			t.Errorf("limit %d: %d entries, next %d, total %d, latest %v; want %d entries, next %d, 250 and no recovery point yet",
				tc.limit, len(got.Entries), got.NextOffset, got.Total, got.Latest, tc.wantLen, tc.wantNext)
		}
		for _, e := range got.Entries {
			if e.Status != LiveNew {
				t.Fatalf("%s: status %q, want new before the first backup", e.Name, e.Status)
			}
		}
	}
}

func TestListLiveFilesLinksUnderTheirOwnType(t *testing.T) {
	h := newHarness(t)
	h.putSized("a.txt", 1)
	h.putSized("sub/inner.txt", 1)
	if err := os.Symlink("a.txt", filepath.Join(h.env.SrcDir, "shortcut.txt")); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	h.env.Backup()
	svc := h.service(nil)
	ctx := context.Background()

	tests := []struct {
		name string
		opts listing.Options
		want []string
	}{
		{"links", listing.Options{Type: "link"}, []string{"shortcut.txt"}},
		{"a link with an extension is not of that extension", listing.Options{Type: "ext:txt"}, []string{"a.txt"}},
		{"files include links", listing.Options{Type: "file"}, []string{"a.txt", "shortcut.txt"}},
		{"folders", listing.Options{Type: "dir"}, []string{"sub"}},
		{"by type", listing.Options{Sort: listing.SortType}, []string{"sub", "shortcut.txt", "a.txt"}},
	}
	for _, tc := range tests {
		got, err := svc.ListLive(ctx, h.env.Src, "", tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if names := liveNames(got); !slices.Equal(names, tc.want) {
			t.Errorf("%s: entries = %v, want %v", tc.name, names, tc.want)
		}
		wantTypes := []listing.TypeCount{{Key: "dir", Count: 1}, {Key: "link", Count: 1}, {Key: "ext:txt", Count: 1}}
		if !slices.Equal(got.Types, wantTypes) {
			t.Errorf("%s: Types = %v, want %v", tc.name, got.Types, wantTypes)
		}
		for _, e := range got.Entries {
			if e.Special != (e.Name == "shortcut.txt") || (e.Special && e.IsDir) {
				t.Errorf("%s: %+v, only shortcut.txt is special and a special item is never a folder", tc.name, e)
			}
		}
	}
}

func TestListHistory(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	svc := h.service(nil)
	src := h.env.Src
	h.putSized("proj/c.pdf", 11)
	h.putSized("proj/README", 6)
	h.putSized("proj/a.TXT", 5)
	h.putSized("proj/b.txt", 2)
	h.putSized("proj/sub/x.go", 3)
	h.putSized("top.txt", 1)
	first := h.env.Backup()
	h.putSized("proj/b.txt", 20)
	h.env.Remove("proj/c.pdf")
	h.putSized("proj/new.txt", 13)
	h.putSized("later/x.txt", 1)
	second := h.env.Backup()

	t.Run("orders, filters and pages an older point", func(t *testing.T) {
		wantTypes := []listing.TypeCount{
			{Key: "dir", Count: 1}, {Key: "noext", Count: 1}, {Key: "ext:txt", Count: 2}, {Key: "ext:pdf", Count: 1},
		}
		tests := []struct {
			name  string
			opts  listing.Options
			want  []string
			total int
		}{
			{"by name", listing.Options{}, []string{"a.TXT", "b.txt", "c.pdf", "README", "sub"}, 5},
			{"by size descending", listing.Options{Sort: listing.SortSize, Desc: true}, []string{"c.pdf", "README", "a.TXT", "b.txt", "sub"}, 5},
			{"by type", listing.Options{Sort: listing.SortType}, []string{"sub", "README", "c.pdf", "a.TXT", "b.txt"}, 5},
			{"by type descending, folders first", listing.Options{Sort: listing.SortType, Desc: true, DirsFirst: true},
				[]string{"sub", "b.txt", "a.TXT", "c.pdf", "README"}, 5},
			{"files by modification time", listing.Options{Sort: listing.SortMTime, Type: "file"}, []string{"c.pdf", "README", "a.TXT", "b.txt"}, 4},
			{"extension", listing.Options{Type: "ext:txt"}, []string{"a.TXT", "b.txt"}, 2},
			{"extension in capitals", listing.Options{Type: "ext:TXT"}, []string{"a.TXT", "b.txt"}, 2},
			{"folders", listing.Options{Type: "dir"}, []string{"sub"}, 1},
			{"files without extension", listing.Options{Type: "noext"}, []string{"README"}, 1},
			{"a page of the files", listing.Options{Type: "file", Offset: 1, Limit: 2}, []string{"b.txt", "c.pdf"}, 4},
			{"past the end", listing.Options{Offset: 50}, nil, 5},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got, err := svc.ListHistory(ctx, src, first, "proj", tc.opts)
				if err != nil {
					t.Fatal(err)
				}
				if names := historyNames(got); !slices.Equal(names, tc.want) {
					t.Errorf("entries = %v, want %v", names, tc.want)
				}
				if got.Total != tc.total || got.Unfiltered != 5 {
					t.Errorf("Total = %d, Unfiltered = %d, want %d and 5", got.Total, got.Unfiltered, tc.total)
				}
				if !slices.Equal(got.Types, wantTypes) {
					t.Errorf("Types = %v, want %v of the whole folder", got.Types, wantTypes)
				}
				if got.Point.ID != first.ID || got.Latest.Seq != second.Seq || got.Rel != "proj" {
					t.Errorf("Point = %d, Latest = %d, Rel = %q", got.Point.Seq, got.Latest.Seq, got.Rel)
				}
			})
		}
	})

	t.Run("compares every entry with the newest point", func(t *testing.T) {
		got, err := svc.ListHistory(ctx, src, first, "proj", listing.Options{Sort: listing.SortSize, Desc: true})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]catalog.NowStatus{
			"a.TXT": catalog.NowSame, "b.txt": catalog.NowModified, "c.pdf": catalog.NowDeleted,
			"README": catalog.NowSame, "sub": catalog.NowSame,
		}
		if len(got.Entries) != len(want) {
			t.Fatalf("entries = %v", historyNames(got))
		}
		for _, e := range got.Entries {
			if e.Now != want[e.Name] {
				t.Errorf("%s: now %q, want %q", e.Name, e.Now, want[e.Name])
			}
		}
	})

	t.Run("at the newest point everything is the same", func(t *testing.T) {
		got, err := svc.ListHistory(ctx, src, second, "proj", listing.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if names := historyNames(got); !slices.Equal(names, []string{"a.TXT", "b.txt", "new.txt", "README", "sub"}) {
			t.Errorf("entries = %v", names)
		}
		for _, e := range got.Entries {
			if e.Now != catalog.NowSame {
				t.Errorf("%s: now %q, want same", e.Name, e.Now)
			}
		}
	})

	t.Run("refuses what did not exist", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			point catalog.Point
			rel   string
		}{
			{"missing folder", first, "missing"},
			{"a file", first, "proj/a.TXT"},
			{"a folder that was added later", first, "later"},
		} {
			if _, err := svc.ListHistory(ctx, src, tc.point, tc.rel, listing.Options{}); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: ListHistory = %v, want ErrNotFound", tc.name, err)
			}
		}
		if got, err := svc.ListHistory(ctx, src, second, "later", listing.Options{}); err != nil || !slices.Equal(historyNames(got), []string{"x.txt"}) {
			t.Errorf("the folder that was added later, at the newest point: %v, %v", historyNames(got), err)
		}
		if _, err := svc.ListHistory(ctx, src, first, "../outside", listing.Options{}); !errors.Is(err, pathutil.ErrUnsafePath) {
			t.Errorf("ListHistory(../outside) = %v, want ErrUnsafePath", err)
		}
	})
}
