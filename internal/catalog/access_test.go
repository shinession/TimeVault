package catalog_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"timevault/internal/access"
	"timevault/internal/catalog"
)

func grants(t *testing.T, browse, backup, full string) access.List {
	t.Helper()
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, ",")
	}
	list, err := access.Spec{Browse: split(browse), Backup: split(backup), Full: split(full)}.List()
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestSourceAccessIsStoredWithTheSource(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)

	none, err := cat.CreateSource(ctx, catalog.NewSource{Name: "private", RootPath: folder("private"), CreatedBy: "t"})
	if err != nil || len(none.Access) != 0 {
		t.Fatalf("a folder created without grants has none (only administrators see it): %+v, %v", none, err)
	}
	want := grants(t, "everyone", "E2002,E1001", "")
	shared, err := cat.CreateSource(ctx, catalog.NewSource{Name: "shared", RootPath: folder("shared"), CreatedBy: "t", Access: want})
	if err != nil || !shared.Access.Equal(want) {
		t.Fatalf("created = %+v, %v; want the grants %+v", shared.Access, err, want)
	}

	got, err := cat.GetSource(ctx, shared.ID)
	if err != nil || !got.Access.Equal(want) {
		t.Errorf("GetSource access = %+v, %v; want %+v", got.Access, err, want)
	}
	list, err := cat.ListSources(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListSources = %+v, %v", list, err)
	}
	for _, s := range list {
		switch s.Name {
		case "private":
			if len(s.Access) != 0 {
				t.Errorf("private folder has grants %+v", s.Access)
			}
		case "shared":
			if !s.Access.Equal(want) {
				t.Errorf("shared folder grants = %+v, want %+v", s.Access, want)
			}
		}
	}
	if a, err := cat.SourceAccess(ctx, shared.ID); err != nil || !a.Equal(want) {
		t.Errorf("SourceAccess = %+v, %v", a, err)
	}
	if _, err := cat.SourceAccess(ctx, 424242); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("SourceAccess of an unknown source = %v, want ErrNotFound", err)
	}
}

func TestUpdateSourceReplacesOrKeepsTheAccess(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	first := grants(t, "guest", "everyone", "E1001")
	src, err := cat.CreateSource(ctx, catalog.NewSource{Name: "docs", RootPath: folder("docs"), CreatedBy: "t", Access: first})
	if err != nil {
		t.Fatal(err)
	}
	update := func(a *access.List) (catalog.Source, error) {
		return cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "docs", RootPath: src.RootPath, Enabled: true, Access: a})
	}

	// Nil leaves the grants as they are.
	kept, err := update(nil)
	if err != nil || !kept.Access.Equal(first) {
		t.Fatalf("an update without Access changed the grants: %+v, %v", kept.Access, err)
	}

	// A list replaces them, and a principal that is no longer in it loses what it had.
	next := grants(t, "E3003", "E2002", "")
	replaced, err := update(&next)
	if err != nil || !replaced.Access.Equal(next) {
		t.Fatalf("replaced = %+v, %v; want %+v", replaced.Access, err, next)
	}
	if got, _ := cat.GetSource(ctx, src.ID); !got.Access.Equal(next) {
		t.Errorf("stored grants = %+v, want %+v", got.Access, next)
	}
	if lvl := replaced.Access.LevelFor(access.Who{SignedIn: true, Account: "E1001"}); lvl != access.None {
		t.Errorf("E1001 still has %v after being removed", lvl)
	}

	// An empty list removes every grant.
	empty := access.List{}
	cleared, err := update(&empty)
	if err != nil || len(cleared.Access) != 0 {
		t.Fatalf("cleared = %+v, %v", cleared.Access, err)
	}

	// A list that is not well formed is refused and nothing changes, not even the other fields of the update.
	bad := access.List{{Kind: access.Guest, Level: access.Backup}}
	if _, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "renamed by a bad update", RootPath: src.RootPath, Enabled: true, Access: &bad}); err == nil {
		t.Fatal("guests with the backup level were stored")
	}
	if got, _ := cat.GetSource(ctx, src.ID); got.Name != "docs" || len(got.Access) != 0 {
		t.Errorf("a refused update left %q with %+v", got.Name, got.Access)
	}
	if _, err := cat.UpdateSource(ctx, 424242, catalog.SourceUpdate{Name: "ghost", RootPath: folder("ghost"), Access: &next}); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("update of an unknown source = %v, want ErrNotFound", err)
	}
}

func TestACreateThatFailsLeavesNoGrantsBehind(t *testing.T) {
	ctx := context.Background()
	cat, pool := newStore(t)
	mustCreate(t, cat, "taken", nil)
	if _, err := cat.CreateSource(ctx, catalog.NewSource{Name: "taken", RootPath: folder("other"), CreatedBy: "t", Access: access.Upgrade()}); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("a duplicate name = %v, want ErrConflict", err)
	}
	bad := access.List{{Kind: access.User, Level: access.Browse}}
	if _, err := cat.CreateSource(ctx, catalog.NewSource{Name: "bad grants", RootPath: folder("bad"), CreatedBy: "t", Access: bad}); err == nil {
		t.Fatal("a grant for a user without an account was stored")
	}
	var sources, rows int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM sources), (SELECT count(*) FROM source_access)`).Scan(&sources, &rows); err != nil {
		t.Fatal(err)
	}
	if sources != 1 || rows != 0 {
		t.Errorf("after the failed creates there are %d sources and %d grants, want 1 and 0", sources, rows)
	}
}

func TestEachUpdateReplacesTheGrantsCompletely(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	src := mustCreate(t, cat, "busy", nil)
	a, b := grants(t, "E1001", "", ""), grants(t, "", "E2002", "")
	for i := 0; i < 20; i++ {
		l := a
		if i%2 == 1 {
			l = b
		}
		if _, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: "busy", RootPath: src.RootPath, Enabled: true, Access: &l}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := cat.GetSource(ctx, src.ID)
	if err != nil || !got.Access.Equal(b) {
		t.Errorf("after twenty replacements the grants are %+v, %v; want exactly the last list %+v", got.Access, err, b)
	}
}

// The grants are handed to Authorize with the row of the source locked: another change of the same source has to wait
// until the first one is through, or the decision could be made on grants that the other change replaces meanwhile.
func TestAuthorizeRunsWhileTheRowOfTheSourceIsLocked(t *testing.T) {
	ctx := context.Background()
	cat, _ := newStore(t)
	src := mustCreate(t, cat, "locked", nil)
	update := func(name string, authorize func(access.List) error) error {
		_, err := cat.UpdateSource(ctx, src.ID, catalog.SourceUpdate{Name: name, RootPath: src.RootPath, KeepRoot: true, Enabled: true, Authorize: authorize})
		return err
	}

	inside, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	// A test that fails must not leave the first change waiting inside its transaction: the schema could not be dropped.
	t.Cleanup(letGo)
	first := make(chan error, 1)
	go func() {
		first <- update("first", func(access.List) error {
			close(inside)
			<-release
			return nil
		})
	}()
	select {
	case <-inside:
	case <-time.After(10 * time.Second):
		t.Fatal("Authorize was not called")
	}

	second := make(chan error, 1)
	go func() { second <- update("second", nil) }()
	select {
	case err := <-second:
		t.Fatalf("a second change went through while the first one was being authorized: %v", err)
	case <-time.After(400 * time.Millisecond):
	}

	letGo()
	for i, ch := range []chan error{first, second} {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("change %d: %v", i+1, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("change %d did not finish", i+1)
		}
	}
	if got, _ := cat.GetSource(ctx, src.ID); got.Name != "second" {
		t.Errorf("the name is %q: the changes were not applied one after the other", got.Name)
	}
}
