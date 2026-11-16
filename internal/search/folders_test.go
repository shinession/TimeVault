package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder collects strings written by the fake engine's handler goroutine.
type recorder struct {
	mu    sync.Mutex
	items []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.items = append(r.items, s)
	r.mu.Unlock()
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.items...)
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.items = nil
	r.mu.Unlock()
}

func TestEverythingSubfoldersAsksForFoldersInsideTheFolder(t *testing.T) {
	eng := newEngine(t, respondJSON(`{
	"totalResults":3,
	"results":[
		{"type":"folder","name":"annual","size":"1048576","date_modified":"`+fileTime20240305+`"},
		{"type":"folder","name":"empty","size":"0","date_modified":""},
		{"type":"folder","name":"no size indexed","size":"","date_modified":"`+fileTime20240305+`"}
	]
}`))
	e := newTestEverything(t, eng.URL, nil)

	got, more, err := e.Subfolders(context.Background(), `D:/data/src/`, 0)
	if err != nil || more {
		t.Fatalf("Subfolders = %v, more=%v, %v", got, more, err)
	}
	want := []FolderInfo{
		{Name: "annual", Size: 1 << 20, ModTime: time20240305},
		{Name: "empty", Size: 0},
		{Name: "no size indexed", Size: -1, ModTime: time20240305},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d folders, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Size != want[i].Size || !got[i].ModTime.Equal(want[i].ModTime) {
			t.Errorf("folder %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	query, _, _ := eng.last()
	for key, want := range map[string]string{
		"search": `infolder:"D:\data\src\" folder:`, "json": "1", "size_column": "1", "date_modified_column": "1",
		"offset": "0", "count": strconv.Itoa(subfolderPage), "sort": "name", "ascending": "1",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("request %s = %q, want %q", key, got, want)
		}
	}
	if query.Has("path_column") {
		t.Error("the path of every folder is known, so it must not be requested")
	}
}

func TestEverythingSubfoldersPagesAndStopsAtTheLimit(t *testing.T) {
	var offsets recorder
	eng := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		offsets.add(r.URL.Query().Get("offset") + "/" + r.URL.Query().Get("count"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		const total = 7
		var items []string
		for i := offset; i < min(offset+count, total); i++ {
			items = append(items, fmt.Sprintf(`{"type":"folder","name":"f%d","size":"%d","date_modified":""}`, i, i))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"totalResults":%d,"results":[%s]}`, total, strings.Join(items, ","))
	})
	e := newTestEverything(t, eng.URL, nil)

	got, more, err := e.Subfolders(context.Background(), `D:\data`, 5)
	if err != nil || !more || len(got) != 5 || got[4].Name != "f4" {
		t.Fatalf("limited Subfolders = %d folders, more=%v, %v", len(got), more, err)
	}
	if seen := offsets.all(); len(seen) != 1 || seen[0] != "0/5" {
		t.Fatalf("requests = %v, want one request for exactly the limit", seen)
	}

	offsets.reset()
	got, more, err = e.Subfolders(context.Background(), `D:\data`, 0)
	if err != nil || more || len(got) != 7 {
		t.Fatalf("unlimited Subfolders = %d folders, more=%v, %v", len(got), more, err)
	}
}

func TestEverythingSubfoldersIgnoresWhatIsNotAFolderInsideTheFolder(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":4,"results":[
		{"type":"file","name":"readme.txt","size":"3","date_modified":""},
		{"type":"folder","name":"D:\\elsewhere","size":"3","date_modified":""},
		{"type":"folder","name":"","size":"3","date_modified":""},
		{"type":"folder","name":"real","size":"9","date_modified":""}
	]}`))
	e := newTestEverything(t, eng.URL, nil)

	got, _, err := e.Subfolders(context.Background(), `D:\data`, 0)
	if err != nil || len(got) != 1 || got[0].Name != "real" {
		t.Fatalf("Subfolders = %+v, %v; want only the real folder", got, err)
	}
}

func TestEverythingSubfoldersErrors(t *testing.T) {
	down := newEngine(t, respondStatus(http.StatusInternalServerError))
	e := newTestEverything(t, down.URL, nil)
	if _, _, err := e.Subfolders(context.Background(), `D:\data`, 0); !errors.Is(err, ErrUnavailable) {
		t.Errorf("engine failure = %v, want ErrUnavailable", err)
	}
	for _, dir := range []string{"", "relative\\path", `D:\data\..\other`, `D:\a"b`, `D:\a*b`} {
		if _, _, err := e.Subfolders(context.Background(), dir, 0); !errors.Is(err, ErrBadQuery) {
			t.Errorf("Subfolders(%q) = %v, want ErrBadQuery", dir, err)
		}
	}
	if down.hits.Load() != 1 {
		t.Errorf("the engine was asked %d times, want 1: a bad folder must not reach it", down.hits.Load())
	}
}

func TestEverythingProbeReadsTheTreeAndTheChangesInLocalTime(t *testing.T) {
	var searches recorder
	eng := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		search := r.URL.Query().Get("search")
		searches.add(search)
		total := 12698
		if strings.Contains(search, "rc:>=") {
			total = 2
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"totalResults":%d,"results":[]}`, total)
	})
	e := newTestEverything(t, eng.URL, nil)

	since := time.Date(2026, 11, 16, 9, 24, 37, 999, time.Local)
	got, err := e.Probe(context.Background(), `D:/TimeVault/monitor`, since)
	if err != nil || got.Items != 12698 || !got.Changed {
		t.Fatalf("Probe = %+v, %v; want 12698 items and a change", got, err)
	}
	selector := `<path:"D:\TimeVault\monitor\" | wfn:"D:\TimeVault\monitor">`
	want := []string{
		selector,
		selector + ` <rc:>=2026-11-16T09:24:37 | dm:>=2026-11-16T09:24:37 | dc:>=2026-11-16T09:24:37>`,
	}
	if seen := searches.all(); len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("searches = %q, want %q", seen, want)
	}
	if q, _, _ := eng.last(); q.Get("count") != "0" {
		t.Errorf("count = %q, a probe only needs the totals", q.Get("count"))
	}
}

func TestEverythingProbeReportsNoChange(t *testing.T) {
	eng := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		total := 40
		if strings.Contains(r.URL.Query().Get("search"), "rc:>=") {
			total = 0
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"totalResults":%d,"results":[]}`, total)
	})
	e := newTestEverything(t, eng.URL, nil)
	got, err := e.Probe(context.Background(), `D:\TimeVault\monitor`, time.Now())
	if err != nil || got.Items != 40 || got.Changed {
		t.Fatalf("Probe = %+v, %v; want 40 items and no change", got, err)
	}
}

func TestEverythingProbeOfAFolderItDoesNotKnow(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
	e := newTestEverything(t, eng.URL, nil)
	if _, err := e.Probe(context.Background(), `\\server\share\unknown`, time.Now()); !errors.Is(err, ErrNotIndexed) {
		t.Fatalf("Probe of an unindexed folder = %v, want ErrNotIndexed", err)
	}
	if eng.hits.Load() != 1 {
		t.Errorf("the engine was asked %d times; an unindexed folder needs no change query", eng.hits.Load())
	}
}

func TestEverythingProbeErrors(t *testing.T) {
	down := newEngine(t, respondStatus(http.StatusBadGateway))
	e := newTestEverything(t, down.URL, nil)
	if _, err := e.Probe(context.Background(), `D:\data`, time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("engine failure = %v, want ErrUnavailable", err)
	}
	if _, err := e.Probe(context.Background(), `relative`, time.Now()); !errors.Is(err, ErrBadQuery) {
		t.Errorf("relative root = %v, want ErrBadQuery", err)
	}
}

func TestEverythingProbeWithoutATimeOnlyCounts(t *testing.T) {
	var searches recorder
	eng := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		searches.add(r.URL.Query().Get("search"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"totalResults":77,"results":[]}`)
	})
	e := newTestEverything(t, eng.URL, nil)
	got, err := e.Probe(context.Background(), `D:\data`, time.Time{})
	if err != nil || got.Items != 77 || got.Changed {
		t.Fatalf("Probe = %+v, %v; want the count alone", got, err)
	}
	if n := len(searches.all()); n != 1 {
		t.Fatalf("the engine was asked %d times, want 1", n)
	}
}

func TestMockFolderListerAndProber(t *testing.T) {
	m := NewMock(nil)
	m.SetSubfolders(`D:\data\`, []FolderInfo{{Name: "a", Size: 1}, {Name: "b", Size: 2}})
	got, more, err := m.Subfolders(context.Background(), `d:\DATA`, 0)
	if err != nil || more || len(got) != 2 {
		t.Fatalf("Subfolders = %+v, %v, %v", got, more, err)
	}
	if got, more, _ = m.Subfolders(context.Background(), `D:\data`, 1); !more || len(got) != 1 {
		t.Fatalf("limited Subfolders = %+v, more=%v", got, more)
	}
	m.SetSubfolders(`D:\data`, nil)
	if got, _, _ = m.Subfolders(context.Background(), `D:\data`, 0); len(got) != 0 {
		t.Fatalf("a removed folder still lists %+v", got)
	}

	if _, err := m.Probe(context.Background(), `D:\data`, time.Now()); !errors.Is(err, ErrNotIndexed) {
		t.Fatalf("Probe of a folder that was not set = %v", err)
	}
	m.SetProbe(`D:\data`, ProbeResult{Items: 3, Changed: true})
	if res, err := m.Probe(context.Background(), `D:\DATA\`, time.Now()); err != nil || res.Items != 3 || !res.Changed {
		t.Fatalf("Probe = %+v, %v", res, err)
	}
	m.SetUnavailable(true)
	if _, err := m.Probe(context.Background(), `D:\data`, time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Probe while down = %v", err)
	}
	if _, _, err := m.Subfolders(context.Background(), `D:\data`, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Subfolders while down = %v", err)
	}
	if m.ProbeCalls() != 3 {
		t.Errorf("ProbeCalls = %d, want 3", m.ProbeCalls())
	}
}

// The engine must never see anything of the folder but the quoted, validated scope.
func TestEverythingFolderQueriesCannotBeInjected(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
	e := newTestEverything(t, eng.URL, nil)
	for _, dir := range []string{`D:\a" | "b`, `D:\a|b`, "D:\\a\nb", `D:\a<b>`} {
		_, _, err1 := e.Subfolders(context.Background(), dir, 0)
		_, err2 := e.Probe(context.Background(), dir, time.Now())
		if !errors.Is(err1, ErrBadQuery) || !errors.Is(err2, ErrBadQuery) {
			t.Errorf("%q: Subfolders = %v, Probe = %v; want ErrBadQuery", dir, err1, err2)
		}
	}
	if eng.hits.Load() != 0 {
		t.Fatalf("an invalid folder reached the engine %d times", eng.hits.Load())
	}
}
