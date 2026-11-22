package search

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"timevault/internal/ntfsindex"
)

// fakeIndex records what the provider asks of the index and answers what the test says.
type fakeIndex struct {
	path   string
	terms  []string
	offset int
	limit  int
	since  time.Time
	page   ntfsindex.Page
	folds  []ntfsindex.Folder
	more   bool
	probe  ntfsindex.ProbeResult
	err    error
	health ntfsindex.Health
}

func (f *fakeIndex) Search(path string, terms []string, offset, limit int) (ntfsindex.Page, error) {
	f.path, f.terms, f.offset, f.limit = path, terms, offset, limit
	return f.page, f.err
}

func (f *fakeIndex) Subfolders(path string, limit int) ([]ntfsindex.Folder, bool, error) {
	f.path, f.limit = path, limit
	return f.folds, f.more, f.err
}

func (f *fakeIndex) Probe(path string, since time.Time) (ntfsindex.ProbeResult, error) {
	f.path, f.since = path, since
	return f.probe, f.err
}

func (f *fakeIndex) Health() ntfsindex.Health { return f.health }

func TestNTFSSearchAsksForThePlainTermsBelowTheRootAndConfinesTheAnswer(t *testing.T) {
	ft := int64(133_000_000_000_000_000)
	idx := &fakeIndex{page: ntfsindex.Page{Total: 30, Items: []ntfsindex.Entry{
		{Name: "a.txt", Path: `D:\data\team\docs\a.txt`, Size: 7, MTime: ft},
		{Name: "docs", Path: `D:\data\team\docs`, IsDir: true, Size: -1},
		{Name: "escaped", Path: `D:\data\team2\escaped.txt`, Size: 1},
		{Name: "root", Path: `D:\data\team`, IsDir: true},
	}}}
	n := NewNTFS(idx)
	page, err := n.Search(context.Background(), Query{Text: `"Annual report" 2024 a|b *`, Root: `d:/data/team/`, Offset: 10, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if idx.path != `D:\data\team\` || !reflect.DeepEqual(idx.terms, []string{"Annual report", "2024", "a|b"}) || idx.offset != 10 || idx.limit != 4 {
		t.Errorf("the index was asked %q %q %d %d", idx.path, idx.terms, idx.offset, idx.limit)
	}
	if page.Total != 30 || page.Offset != 10 || page.NextOffset != 14 || page.Dropped != 2 || len(page.Items) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if got := page.Items[0]; got.Name != "a.txt" || got.Size != 7 || got.IsDir || !got.ModTime.Equal(ntfsindex.Time(ft)) || got.ModTime.IsZero() {
		t.Errorf("first result = %+v", got)
	}
	if got := page.Items[1]; !got.IsDir || got.Size != -1 || !got.ModTime.IsZero() {
		t.Errorf("a folder = %+v", got)
	}
	// The last page has no next one.
	idx.page = ntfsindex.Page{Total: 2, Items: idx.page.Items[:2]}
	if page, _ = n.Search(context.Background(), Query{Text: "a", Root: `D:\data\team`}); page.NextOffset != -1 {
		t.Errorf("NextOffset of the last page = %d", page.NextOffset)
	}
}

func TestNTFSRefusesWhatIsNotAValidRequestWithoutAskingTheIndex(t *testing.T) {
	idx := &fakeIndex{}
	n := NewNTFS(idx)
	for name, q := range map[string]Query{
		"empty text":        {Text: "  ", Root: `D:\x`},
		"control character": {Text: "a\nb", Root: `D:\x`},
		"relative root":     {Text: "a", Root: `x\y`},
		"traversal":         {Text: "a", Root: `D:\a\..\b`},
		"bad offset":        {Text: "a", Root: `D:\x`, Offset: -1},
		"too large a page":  {Text: "a", Root: `D:\x`, Limit: MaxLimit + 1},
	} {
		if _, err := n.Search(context.Background(), q); !errors.Is(err, ErrBadQuery) {
			t.Errorf("%s: error = %v, want ErrBadQuery", name, err)
		}
	}
	for _, dir := range []string{``, `x`, `D:\a\..\b`, "D:\\a\nb", `D:\a<b>`} {
		_, _, err1 := n.Subfolders(context.Background(), dir, 0)
		_, err2 := n.Probe(context.Background(), dir, time.Now())
		if !errors.Is(err1, ErrBadQuery) || !errors.Is(err2, ErrBadQuery) {
			t.Errorf("%q: Subfolders = %v, Probe = %v; want ErrBadQuery", dir, err1, err2)
		}
	}
	if idx.path != "" {
		t.Errorf("an invalid request reached the index: %q", idx.path)
	}
}

func TestNTFSMapsTheErrorsOfTheIndex(t *testing.T) {
	idx := &fakeIndex{}
	n := NewNTFS(idx)
	ask := func() (error, error, error) {
		_, e1 := n.Search(context.Background(), Query{Text: "a", Root: `D:\x`})
		_, _, e2 := n.Subfolders(context.Background(), `D:\x`, 0)
		_, e3 := n.Probe(context.Background(), `D:\x`, time.Time{})
		return e1, e2, e3
	}
	idx.err = ntfsindex.ErrNotIndexed
	for _, err := range []error{first3(ask())[0], first3(ask())[1], first3(ask())[2]} {
		if !errors.Is(err, ErrNotIndexed) || errors.Is(err, ErrUnavailable) {
			t.Errorf("a folder that is not indexed = %v, want ErrNotIndexed only", err)
		}
	}
	idx.err = ntfsindex.ErrNotReady
	for _, err := range first3(ask()) {
		if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotIndexed) || !strings.Contains(err.Error(), "not ready") {
			t.Errorf("an index that is not ready = %v, want ErrUnavailable", err)
		}
	}
	idx.err = errors.New("the volume went away")
	for _, err := range first3(ask()) {
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("another failure = %v, want ErrUnavailable", err)
		}
	}
}

func first3(a, b, c error) []error { return []error{a, b, c} }

func TestNTFSAnswersForFoldersAndChanges(t *testing.T) {
	ft := int64(133_000_000_000_000_000)
	idx := &fakeIndex{
		folds: []ntfsindex.Folder{{Name: "docs", Size: 90, MTime: ft}, {Name: "other", Size: 0}}, more: true,
		probe: ntfsindex.ProbeResult{Items: 12, Changed: true},
	}
	n := NewNTFS(idx)
	folders, more, err := n.Subfolders(context.Background(), `D:\data\team\`, 2)
	if err != nil || !more || idx.path != `D:\data\team\` || idx.limit != 2 {
		t.Fatalf("Subfolders = %+v %v %v (asked %q %d)", folders, more, err, idx.path, idx.limit)
	}
	if len(folders) != 2 || folders[0].Name != "docs" || folders[0].Size != 90 || folders[0].ModTime.IsZero() || !folders[1].ModTime.IsZero() {
		t.Errorf("folders = %+v", folders)
	}
	since := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	res, err := n.Probe(context.Background(), `D:\data\team`, since)
	if err != nil || res.Items != 12 || !res.Changed || !idx.since.Equal(since) || idx.path != `D:\data\team\` {
		t.Errorf("Probe = %+v, %v (asked %q %v)", res, err, idx.path, idx.since)
	}
}

func TestNTFSHealthFollowsTheIndex(t *testing.T) {
	idx := &fakeIndex{health: ntfsindex.Health{State: ntfsindex.StateOK, Key: "index_ok", Args: []any{10, "D:"}, Detail: "10 entries on D:"}}
	n := NewNTFS(idx)
	if h := n.Health(context.Background()); h.State != StateOK || h.Key != "index_ok" || h.Detail != "10 entries on D:" || !reflect.DeepEqual(h.Args, []any{10, "D:"}) || h.CheckedAt.IsZero() {
		t.Errorf("ok: %+v", h)
	}
	idx.health = ntfsindex.Health{State: ntfsindex.StateIdle, Key: "index_idle", Detail: "there are no folders to index"}
	if h := n.Health(context.Background()); h.State != StateOK || h.Key != "index_idle" {
		t.Errorf("nothing to index is not a failure: %+v", h)
	}
	idx.health = ntfsindex.Health{State: ntfsindex.StateDegraded, Key: "index_no_admin", Detail: "needs administrator rights"}
	if h := n.Health(context.Background()); h.State != StateDegraded || h.Key != "index_no_admin" {
		t.Errorf("degraded: %+v", h)
	}
	if n.Name() != "ntfs" {
		t.Errorf("Name = %q", n.Name())
	}
}

func TestNTFSHonoursCancellation(t *testing.T) {
	n := NewNTFS(&fakeIndex{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Search(ctx, Query{Text: "a", Root: `D:\x`}); !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Errorf("Search = %v", err)
	}
	if _, _, err := n.Subfolders(ctx, `D:\x`, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("Subfolders = %v", err)
	}
	if _, err := n.Probe(ctx, `D:\x`, time.Time{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Probe = %v", err)
	}
}
