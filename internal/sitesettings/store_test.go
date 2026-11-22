package sitesettings_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"timevault/internal/db/dbtest"
	"timevault/internal/sitesettings"
)

func TestStoreLoadSaveAndReset(t *testing.T) {
	ctx := context.Background()
	store := sitesettings.New(dbtest.New(t))

	got, err := store.Load(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("a new database stores nothing: %v, %v", got, err)
	}
	if err := store.Save(ctx, map[string]string{"job_workers": "4", "log_level": "debug"}, nil, "admin"); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.Load(ctx); !reflect.DeepEqual(got, map[string]string{"job_workers": "4", "log_level": "debug"}) {
		t.Fatalf("Load = %v", got)
	}

	// A save replaces a value, adds another and resets a third, together.
	if err := store.Save(ctx, map[string]string{"job_workers": " 6 ", "cookie_secure": "true"}, []string{"log_level"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.Load(ctx); !reflect.DeepEqual(got, map[string]string{"job_workers": "6", "cookie_secure": "true"}) {
		t.Fatalf("Load after the second save = %v", got)
	}
	rows, err := store.Rows(ctx)
	if err != nil || len(rows) != 2 || rows[0].Key != "cookie_secure" || rows[1].UpdatedBy != "admin" || rows[1].UpdatedAt.IsZero() {
		t.Fatalf("Rows = %+v, %v", rows, err)
	}
	// Resetting what is not stored is not an error.
	if err := store.Save(ctx, nil, []string{"web_log_host"}, "cli"); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRefusesWhatIsNotASiteSettingAndSavesNothing(t *testing.T) {
	ctx := context.Background()
	store := sitesettings.New(dbtest.New(t))
	for name, c := range map[string]struct {
		set   map[string]string
		reset []string
	}{
		"unknown key":   {map[string]string{"job_workers": "3", "colour": "blue"}, nil},
		"unknown reset": {map[string]string{"job_workers": "3"}, []string{"colour"}},
		"empty value":   {map[string]string{"job_workers": "3", "log_level": "  "}, nil},
	} {
		err := store.Save(ctx, c.set, c.reset, "admin")
		if !errors.Is(err, sitesettings.ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
		if got, _ := store.Load(ctx); len(got) != 0 {
			t.Errorf("%s: a refused save stored %v", name, got)
		}
	}
}

func TestStoreKeepsALongNameOfTheAccountWithinTheColumn(t *testing.T) {
	ctx := context.Background()
	store := sitesettings.New(dbtest.New(t))
	if err := store.Save(ctx, map[string]string{"job_workers": "3"}, nil, strings.Repeat("a", 500)); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Rows(ctx)
	if err != nil || len(rows) != 1 || len(rows[0].UpdatedBy) != 128 {
		t.Fatalf("Rows = %+v, %v", rows, err)
	}
}
