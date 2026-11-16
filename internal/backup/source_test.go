package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSourceErrorIsAnInvalidSource(t *testing.T) {
	var err error = &SourceError{Key: "errors.display_host", Args: []any{"typed", "expected"}, Msg: "the display path is on another server"}
	wrapped := fmt.Errorf("add source: %w", err)
	var se *SourceError
	if !errors.Is(wrapped, ErrInvalidSource) || !errors.As(wrapped, &se) || se.Key != "errors.display_host" || len(se.Args) != 2 {
		t.Fatalf("a SourceError must wrap ErrInvalidSource and survive wrapping: %v", wrapped)
	}
	if got, want := err.Error(), "invalid source: the display path is on another server"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if got, want := invalid("the name is bad").Error(), (&SourceError{Msg: "the name is bad"}).Error(); got != want {
		t.Fatalf("a SourceError reads like the other validation errors: %q vs %q", got, want)
	}
}

func TestParseDisplayPath(t *testing.T) {
	u, err := ParseDisplayPath(" //srv/TimeVault/monitor/ ")
	if err != nil || u.String() != `\\srv\TimeVault\monitor` {
		t.Fatalf("ParseDisplayPath = %v, %v", u, err)
	}
	for _, bad := range []string{``, `D:\TimeVault\monitor`, `\\srv`, `\\srv\C$\x`, `\\srv\share\..\x`, `\\srv\share\a*b`} {
		_, err := ParseDisplayPath(bad)
		var se *SourceError
		if !errors.As(err, &se) || !errors.Is(err, ErrInvalidSource) {
			t.Errorf("ParseDisplayPath(%q) = %v, want a *SourceError", bad, err)
			continue
		}
		if se.Key != "errors.display_invalid" || len(se.Args) != 1 || se.Args[0] != bad {
			t.Errorf("ParseDisplayPath(%q): key %q, args %v; the text as typed is the only argument", bad, se.Key, se.Args)
		}
		if !strings.Contains(se.Msg, `\\server\share\folder`) {
			t.Errorf("ParseDisplayPath(%q): the message %q should show what a display path looks like", bad, se.Msg)
		}
	}
}

func TestPrepareSourceDisplayPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fresh := t.TempDir()

	for in, want := range map[string]string{
		"":                                  "",
		"   ":                               "",
		`\\srv\TimeVault\monitor`:           `\\srv\TimeVault\monitor`,
		` //Srv.Example.com/TimeVault/Mon/`: `\\Srv.Example.com\TimeVault\Mon`,
	} {
		got, err := e.eng.PrepareSource(ctx, SourceRequest{Name: "n", RootPath: fresh, DisplayPath: in}, "tester")
		if err != nil || got.DisplayPath != want {
			t.Errorf("PrepareSource with display path %q = %q, %v; want %q", in, got.DisplayPath, err, want)
		}
		upd, err := e.eng.PrepareSourceUpdate(ctx, e.src, SourceRequest{Name: "n", DisplayPath: in})
		if err != nil || upd.DisplayPath != want {
			t.Errorf("PrepareSourceUpdate with display path %q = %q, %v; want %q", in, upd.DisplayPath, err, want)
		}
	}

	// An update replaces the display path, so leaving it empty removes the current one.
	cur := e.src
	cur.DisplayPath = `\\srv\TimeVault\monitor`
	got, err := e.eng.PrepareSourceUpdate(ctx, cur, SourceRequest{Name: "n"})
	if err != nil || got.DisplayPath != "" {
		t.Errorf("an empty display path on an update = %q, %v; want it removed", got.DisplayPath, err)
	}

	for _, bad := range []string{`D:\TimeVault\monitor`, `monitor`, `\\srv`, `\\srv\ADMIN$`, `\\srv\share\a|b`, `\\srv\share\x\\y`} {
		_, errNew := e.eng.PrepareSource(ctx, SourceRequest{Name: "n", RootPath: fresh, DisplayPath: bad}, "tester")
		_, errUpdate := e.eng.PrepareSourceUpdate(ctx, e.src, SourceRequest{Name: "n", DisplayPath: bad})
		for what, err := range map[string]error{"PrepareSource": errNew, "PrepareSourceUpdate": errUpdate} {
			var se *SourceError
			if !errors.As(err, &se) || se.Key != "errors.display_invalid" || len(se.Args) != 1 || se.Args[0] != bad || !errors.Is(err, ErrInvalidSource) {
				t.Errorf("%s with display path %q = %v, want a display_invalid *SourceError carrying the text", what, bad, err)
			}
		}
	}
}
