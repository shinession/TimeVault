package catalog_test

import (
	"context"
	"errors"
	"testing"

	"timevault/internal/catalog"
	"timevault/internal/db/dbtest"
)

// Advisory locks are shared by the whole database. Catalogs that live in different
// schemas must not block each other, but one catalog must still serialize its writers.
func TestSourceLockIsScopedToTheSchema(t *testing.T) {
	ctx := context.Background()
	a := catalog.NewStore(dbtest.New(t))
	b := catalog.NewStore(dbtest.New(t))

	lockA, err := a.LockSource(ctx, 1)
	if err != nil {
		t.Fatalf("lock in the first schema: %v", err)
	}
	defer lockA.Unlock()

	lockB, err := b.LockSource(ctx, 1)
	if err != nil {
		t.Fatalf("the same source number in another schema must not be busy: %v", err)
	}
	defer lockB.Unlock()

	if _, err := a.LockSource(ctx, 1); !errors.Is(err, catalog.ErrBusy) {
		t.Fatalf("a second writer in the same schema got %v, want ErrBusy", err)
	}
	lockA.Unlock()
	again, err := a.LockSource(ctx, 1)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	again.Unlock()
}
