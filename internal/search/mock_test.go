package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mockEntries() []Result {
	file := func(path, name string) Result { return Result{Name: name, Path: path + `\` + name, Size: 10} }
	dir := func(path, name string) Result {
		return Result{Name: name, Path: path + `\` + name, IsDir: true, Size: -1}
	}
	return []Result{
		file(`D:\data\src`, "Report 2024.docx"),
		file(`D:\data\src\sub`, "report 2023.docx"),
		dir(`D:\data\src`, "reports"),
		file(`D:\data\src2`, "report 2024.docx"),
		file(`D:\data\other`, "report.docx"),
		file(`d:\DATA\SRC`, "notes.txt"),
		file(`D:\data\src`, "报告 2024.docx"),
		dir(`D:\data`, "src"),
	}
}

func mockNames(page Page) []string {
	var names []string
	for _, it := range page.Items {
		names = append(names, it.Path)
	}
	return names
}

func TestMockConfinesToRootAndMatchesTerms(t *testing.T) {
	m := NewMock(mockEntries())
	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{"root boundary", Query{Text: "report", Root: `D:\data\src`}, []string{`D:\data\src\reports`, `D:\data\src\sub\report 2023.docx`, `D:\data\src\Report 2024.docx`}},
		{"sibling with the same prefix", Query{Text: "report", Root: `D:\data\src2`}, []string{`D:\data\src2\report 2024.docx`}},
		{"case-insensitive root", Query{Text: "notes", Root: `D:/Data/Src/`}, []string{`d:\DATA\SRC\notes.txt`}},
		{"all terms must match", Query{Text: "report 2024", Root: `D:\data\src`}, []string{`D:\data\src\Report 2024.docx`}},
		{"phrase", Query{Text: `"report 2024"`, Root: `D:\data\src`}, []string{`D:\data\src\Report 2024.docx`}},
		{"phrase is not split into terms", Query{Text: `"2024 report"`, Root: `D:\data\src`}, nil},
		{"case-insensitive term", Query{Text: "REPORT 2023", Root: `D:\data\src`}, []string{`D:\data\src\sub\report 2023.docx`}},
		{"unicode term", Query{Text: "报告", Root: `D:\data\src`}, []string{`D:\data\src\报告 2024.docx`}},
		{"wildcards separate terms", Query{Text: "rep*2024", Root: `D:\data\src`}, []string{`D:\data\src\Report 2024.docx`}},
		{"the root itself is not a result", Query{Text: "src", Root: `D:\data\src`}, nil},
		{"a folder inside the root is a result", Query{Text: "src", Root: `D:\data`}, []string{`D:\data\src`}},
		{"no match", Query{Text: "zzz", Root: `D:\data`}, nil},
		{"operators stay literal", Query{Text: "report|notes", Root: `D:\data`}, nil},
		{"other drive", Query{Text: "report", Root: `E:\data`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := m.Search(context.Background(), tt.q)
			if err != nil {
				t.Fatalf("Search error = %v", err)
			}
			if got := mockNames(page); fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("results = %q, want %q", got, tt.want)
			}
			if page.Total != len(tt.want) || page.Dropped != 0 || page.NextOffset != -1 {
				t.Errorf("page = total %d dropped %d next %d, want %d 0 -1", page.Total, page.Dropped, page.NextOffset, len(tt.want))
			}
		})
	}
}

func TestMockPagingMatchesTheRealProvider(t *testing.T) {
	var entries []Result
	for i := 1; i <= 130; i++ {
		name := fmt.Sprintf("bulk-%03d.dat", i)
		entries = append(entries, Result{Name: name, Path: `D:\r\bulk\` + name, Size: int64(i)})
	}
	entries = append(entries, Result{Name: "bulk-001.dat", Path: `D:\r2\bulk-001.dat`})
	m := NewMock(entries)

	var seen []string
	offset := 0
	for _, wantNext := range []int{50, 100, -1} {
		page, err := m.Search(context.Background(), Query{Text: "bulk-", Root: `D:\r`, Offset: offset, Limit: 50})
		if err != nil {
			t.Fatalf("Search error = %v", err)
		}
		if page.Total != 130 || page.Offset != offset || page.NextOffset != wantNext {
			t.Errorf("page at %d = total %d offset %d next %d, want 130 %d %d", offset, page.Total, page.Offset, page.NextOffset, offset, wantNext)
		}
		seen = append(seen, mockNames(page)...)
		offset = page.NextOffset
	}
	if len(seen) != 130 || seen[0] != `D:\r\bulk\bulk-001.dat` || seen[129] != `D:\r\bulk\bulk-130.dat` {
		t.Errorf("paged %d results from %q to %q", len(seen), seen[0], seen[len(seen)-1])
	}

	past, err := m.Search(context.Background(), Query{Text: "bulk-", Root: `D:\r`, Offset: 130, Limit: 50})
	if err != nil || past.Items == nil || len(past.Items) != 0 || past.Total != 130 || past.NextOffset != -1 {
		t.Errorf("page past the end = %+v, %v", past, err)
	}
	def, err := m.Search(context.Background(), Query{Text: "bulk-", Root: `D:\r`})
	if err != nil || len(def.Items) != DefaultLimit || def.NextOffset != DefaultLimit {
		t.Errorf("default page = %d items next %d, %v", len(def.Items), def.NextOffset, err)
	}
}

func TestMockOrdersFoldersFirstThenByName(t *testing.T) {
	m := NewMock([]Result{
		{Name: "b-file.txt", Path: `D:\r\b-file.txt`},
		{Name: "Z-dir", Path: `D:\r\Z-dir`, IsDir: true},
		{Name: "A-file.txt", Path: `D:\r\A-file.txt`},
		{Name: "a-dir", Path: `D:\r\a-dir`, IsDir: true},
	})
	page, err := m.Search(context.Background(), Query{Text: "-", Root: `D:\r`})
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if got, want := fmt.Sprint(mockNames(page)), `[D:\r\a-dir D:\r\Z-dir D:\r\A-file.txt D:\r\b-file.txt]`; got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}

func TestMockValidatesLikeTheRealProvider(t *testing.T) {
	m := NewMock(mockEntries())
	for name, q := range map[string]Query{
		"empty text":     {Text: "", Root: `D:\data`},
		"wildcard only":  {Text: "*", Root: `D:\data`},
		"control char":   {Text: "a\tb", Root: `D:\data`},
		"relative root":  {Text: "a", Root: `data`},
		"empty root":     {Text: "a"},
		"negative off":   {Text: "a", Root: `D:\data`, Offset: -1},
		"negative limit": {Text: "a", Root: `D:\data`, Limit: -1},
		"limit too big":  {Text: "a", Root: `D:\data`, Limit: MaxLimit + 1},
	} {
		if _, err := m.Search(context.Background(), q); !errors.Is(err, ErrBadQuery) {
			t.Errorf("%s: error = %v, want ErrBadQuery", name, err)
		}
	}
}

func TestMockOutage(t *testing.T) {
	m := NewMock(mockEntries())
	q := Query{Text: "report", Root: `D:\data`}
	if h := m.Health(context.Background()); h.State != StateOK || h.CheckedAt.IsZero() {
		t.Errorf("Health = %+v, want ok", h)
	}
	m.SetUnavailable(true)
	if _, err := m.Search(context.Background(), q); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Search during outage error = %v, want ErrUnavailable", err)
	}
	if h := m.Health(context.Background()); h.State != StateDegraded || h.Detail == "" {
		t.Errorf("Health during outage = %+v, want degraded", h)
	}
	if _, err := m.Search(context.Background(), Query{}); !errors.Is(err, ErrBadQuery) {
		t.Errorf("a bad query during an outage error = %v, want ErrBadQuery", err)
	}
	m.SetUnavailable(false)
	if page, err := m.Search(context.Background(), q); err != nil || page.Total == 0 {
		t.Errorf("Search after recovery = %+v, %v", page, err)
	}
	if m.Name() != "mock" {
		t.Errorf("Name() = %q", m.Name())
	}
}

func TestMockIsIsolatedFromItsInputAndSafeForConcurrentUse(t *testing.T) {
	entries := mockEntries()
	m := NewMock(entries)
	entries[0].Name = "changed"
	entries[0].Path = `D:\elsewhere\changed`

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.SetUnavailable(i%2 == 0)
			_, _ = m.Search(context.Background(), Query{Text: "report", Root: `D:\data`})
			m.Health(context.Background())
		}(i)
	}
	wg.Wait()
	m.SetUnavailable(false)

	page, err := m.Search(context.Background(), Query{Text: "report 2024", Root: `D:\data\src`})
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != "Report 2024.docx" {
		t.Errorf("results after mutating the input = %+v, %v", page.Items, err)
	}
	page.Items[0].Name = "mutated"
	if again, _ := m.Search(context.Background(), Query{Text: "report 2024", Root: `D:\data\src`}); again.Items[0].Name != "Report 2024.docx" {
		t.Error("mutating a returned page changed the mock")
	}
}

func TestMockHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewMock(mockEntries()).Search(ctx, Query{Text: "report", Root: `D:\data`}); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestDisabled(t *testing.T) {
	var p Provider = NewDisabled()
	if _, err := p.Search(context.Background(), Query{Text: "a", Root: `D:\r`}); !errors.Is(err, ErrDisabled) {
		t.Errorf("Search error = %v, want ErrDisabled", err)
	}
	if h := p.Health(context.Background()); h.State != StateDisabled || h.CheckedAt.IsZero() {
		t.Errorf("Health = %+v, want disabled", h)
	}
	if p.Name() != "disabled" {
		t.Errorf("Name() = %q", p.Name())
	}
}
