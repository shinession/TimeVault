package catalog_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"timevault/internal/catalog"
	"timevault/internal/testenv"
)

func TestListAllChildrenReturnsTheWholeFolderInNameOrder(t *testing.T) {
	ctx := context.Background()
	env := testenv.New(t)
	for i := range 30 {
		env.Write(fmt.Sprintf("file-%02d.txt", i), "content")
	}
	env.Write("UPPER.txt", "upper") // sorts case-insensitively among the others
	env.Write("sub/inner.txt", "inner")
	first := env.Backup()
	env.Write("file-03.txt", "changed")
	env.Remove("file-07.txt")
	second := env.Backup()
	root, err := env.Cat.RootNode(ctx, env.Src.ID)
	if err != nil {
		t.Fatal(err)
	}

	var paged []catalog.Entry
	var after *catalog.Cursor
	for {
		page, err := env.Cat.ListChildren(ctx, catalog.ListParams{Parent: root, Seq: first.Seq, LatestSeq: second.Seq, After: after, Limit: 7})
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, page.Entries...)
		if page.Next == nil {
			break
		}
		after = page.Next
	}

	// The limit and the cursor are the paging of ListChildren; this call reads everything.
	all, err := env.Cat.ListAllChildren(ctx, catalog.ListParams{
		Parent: root, Seq: first.Seq, LatestSeq: second.Seq, Limit: 3, After: &catalog.Cursor{Key: "file-10.txt", Name: "file-10.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 32 || !reflect.DeepEqual(all, paged) {
		t.Fatalf("ListAllChildren returned %d entries, want the %d that ListChildren pages through, in the same order", len(all), len(paged))
	}
	if !slices.IsSortedFunc(all, func(a, b catalog.Entry) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) }) {
		t.Errorf("entries are not in name order")
	}
	for _, e := range all {
		want := catalog.NowSame
		switch e.Name {
		case "file-03.txt":
			want = catalog.NowModified
		case "file-07.txt":
			want = catalog.NowDeleted
		}
		if e.Now != want {
			t.Errorf("%s: now %q, want %q", e.Name, e.Now, want)
		}
	}

	// Without the newest sequence number the entries are not compared with it.
	plain, err := env.Cat.ListAllChildren(ctx, catalog.ListParams{Parent: root, Seq: first.Seq})
	if err != nil || len(plain) != 32 {
		t.Fatalf("without LatestSeq: %d entries, %v", len(plain), err)
	}
	for _, e := range plain {
		if e.Now != catalog.NowUnknown {
			t.Errorf("%s: now %q, want it unset without LatestSeq", e.Name, e.Now)
		}
	}

	// At the newest point the deleted file is gone.
	current, err := env.Cat.ListAllChildren(ctx, catalog.ListParams{Parent: root, Seq: second.Seq, LatestSeq: second.Seq})
	if err != nil || len(current) != 31 {
		t.Fatalf("at the newest point: %d entries, %v", len(current), err)
	}
}
