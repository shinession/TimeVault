package service

import (
	"context"
	"path/filepath"
	"testing"

	"timevault/internal/listing"
	"timevault/internal/search"
)

func entryNamed(t *testing.T, l LiveListing, name string) LiveEntry {
	t.Helper()
	for _, e := range l.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("%q is not in %v", name, liveNames(l))
	return LiveEntry{}
}

func TestLiveListingShowsFolderSizesFromTheSearchEngine(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	mock := search.NewMock(nil)
	svc := h.service(func(c *Config) { c.Search = mock })
	h.putSized("small/x.txt", 3)
	h.putSized("big/y.txt", 5)
	h.putSized("unknown/z.txt", 1)
	h.putSized("file.txt", 7)
	mock.SetSubfolders(h.env.SrcDir, []search.FolderInfo{
		{Name: "small", Size: 100}, {Name: "BIG", Size: 9000}, {Name: "ghost", Size: 1}, {Name: "unknown", Size: -1},
	})

	l, err := svc.ListLive(ctx, h.env.Src, "", listing.Options{Sort: listing.SortSize, Desc: true, DirsFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := liveNames(l); len(got) != 4 || got[0] != "big" || got[1] != "small" || got[2] != "unknown" || got[3] != "file.txt" {
		t.Fatalf("by size, folders first = %v, want the folders ordered by the size of what is in them", got)
	}
	for name, want := range map[string]struct {
		size  int64
		known bool
	}{
		"big": {9000, true}, "small": {100, true}, "unknown": {0, false}, "file.txt": {7, true},
	} {
		if e := entryNamed(t, l, name); e.Size != want.size || e.SizeKnown != want.known {
			t.Errorf("%s: size %d known %v, want %d %v", name, e.Size, e.SizeKnown, want.size, want.known)
		}
	}
	// What is on the disk stays what the disk says; the engine only adds the sizes.
	if e := entryNamed(t, l, "big"); e.ModTime.IsZero() || !e.IsDir {
		t.Errorf("big = %+v, want the modification time of the disk", e)
	}

	// Sorting and paging a listing asks the engine once.
	if _, err := svc.ListLive(ctx, h.env.Src, "", listing.Options{Sort: listing.SortName, Offset: 1, Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if n := mock.SubfolderCalls(); n != 1 {
		t.Errorf("the engine was asked %d times for one folder, want 1", n)
	}

	// A folder inside the source is looked up by its own place on the disk.
	mock.SetSubfolders(filepath.Join(h.env.SrcDir, "big"), []search.FolderInfo{{Name: "inner", Size: 42}})
	h.putSized("big/inner/q.txt", 2)
	inner, err := svc.ListLive(ctx, h.env.Src, "big", listing.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(t, inner, "inner"); e.Size != 42 || !e.SizeKnown {
		t.Errorf("inner = %+v, want the size of the engine", e)
	}
}

func TestLiveListingWithoutFolderSizesStillWorks(t *testing.T) {
	ctx := context.Background()
	for name, provider := range map[string]search.Provider{
		"no search engine": search.NewDisabled(),
		"engine is down": func() search.Provider {
			m := search.NewMock(nil)
			m.SetUnavailable(true)
			return m
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			svc := h.service(func(c *Config) { c.Search = provider })
			h.putSized("docs/a.txt", 3)
			h.putSized("b.txt", 5)
			for i := 0; i < 3; i++ {
				l, err := svc.ListLive(ctx, h.env.Src, "", listing.Options{})
				if err != nil {
					t.Fatal(err)
				}
				if d := entryNamed(t, l, "docs"); d.SizeKnown || d.Size != 0 || d.ModTime.IsZero() {
					t.Fatalf("docs = %+v, want no size but the modification time of the disk", d)
				}
				if f := entryNamed(t, l, "b.txt"); !f.SizeKnown || f.Size != 5 {
					t.Fatalf("b.txt = %+v", f)
				}
			}
			// A search engine that does not answer is left alone for a while instead of being asked by every page.
			if m, ok := provider.(*search.Mock); ok && m.SubfolderCalls() != 1 {
				t.Errorf("the engine that is down was asked %d times by 3 listings, want 1", m.SubfolderCalls())
			}
		})
	}
}
