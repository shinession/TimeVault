package search

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fileTime20240305 is the FILETIME of 2024-03-05T06:07:08Z.
const fileTime20240305 = "133540924280000000"

var time20240305 = time.Date(2024, 3, 5, 6, 7, 8, 0, time.UTC)

// fakeEngine is a fake Everything HTTP server that records what it was asked.
type fakeEngine struct {
	*httptest.Server
	hits atomic.Int32

	mu     sync.Mutex
	query  url.Values
	header http.Header
	path   string
}

func newEngine(t *testing.T, respond http.HandlerFunc) *fakeEngine {
	t.Helper()
	e := &fakeEngine{}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		e.mu.Lock()
		e.query, e.header, e.path = r.URL.Query(), r.Header.Clone(), r.URL.Path
		e.mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(e.Close)
	return e
}

func (e *fakeEngine) last() (query url.Values, header http.Header, path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.query, e.header, e.path
}

func respondJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func respondStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte("<html>" + http.StatusText(code) + "</html>"))
	}
}

func newTestEverything(t *testing.T, baseURL string, mutate func(*EverythingOptions)) *Everything {
	t.Helper()
	opts := EverythingOptions{BaseURL: baseURL, Timeout: 2 * time.Second}
	if mutate != nil {
		mutate(&opts)
	}
	e, err := NewEverything(opts)
	if err != nil {
		t.Fatalf("NewEverything error = %v", err)
	}
	return e
}

func sameResult(a, b Result) bool {
	return a.Name == b.Name && a.Path == b.Path && a.IsDir == b.IsDir && a.Size == b.Size && a.ModTime.Equal(b.ModTime)
}

func TestEverythingSearchRequestAndResults(t *testing.T) {
	eng := newEngine(t, respondJSON(`{
	"totalResults":40,
	"results":[
		{
			"type":"folder",
			"name":"annual",
			"path":"D:\\data\\src",
			"size":"",
			"date_modified":"`+fileTime20240305+`"
		},
		{
			"type":"file",
			"name":"report 2024.docx",
			"path":"D:\\data\\src\\annual",
			"size":"1234",
			"date_modified":"`+fileTime20240305+`"
		},
		{
			"type":"file",
			"name":"报告 😀.txt",
			"path":"D:\\data\\src\\annual\\sub",
			"size":"0",
			"date_modified":""
		}
	]
}`))
	e := newTestEverything(t, eng.URL, nil)

	page, err := e.Search(context.Background(), Query{Text: `report "annual 2024"`, Root: `D:/data/src/`, Offset: 10, Limit: 25})
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}

	query, header, path := eng.last()
	wantQuery := map[string]string{
		"search":               `path:"D:\data\src\" "report" "annual 2024"`,
		"json":                 "1",
		"path_column":          "1",
		"size_column":          "1",
		"date_modified_column": "1",
		"offset":               "10",
		"count":                "25",
		"sort":                 "name",
		"ascending":            "1",
	}
	for key, want := range wantQuery {
		if got := query.Get(key); got != want {
			t.Errorf("request parameter %s = %q, want %q", key, got, want)
		}
	}
	if len(query) != len(wantQuery) {
		t.Errorf("request has parameters %v, want exactly %d", query, len(wantQuery))
	}
	if path != "/" {
		t.Errorf("request path = %q, want /", path)
	}
	if got := header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header = %q without credentials, want none", got)
	}

	want := []Result{
		{Name: "annual", Path: `D:\data\src\annual`, IsDir: true, Size: -1, ModTime: time20240305},
		{Name: "report 2024.docx", Path: `D:\data\src\annual\report 2024.docx`, Size: 1234, ModTime: time20240305},
		{Name: "报告 😀.txt", Path: `D:\data\src\annual\sub\报告 😀.txt`, Size: 0},
	}
	if len(page.Items) != len(want) {
		t.Fatalf("Items = %+v, want %d items", page.Items, len(want))
	}
	for i := range want {
		if !sameResult(page.Items[i], want[i]) {
			t.Errorf("Items[%d] = %+v, want %+v", i, page.Items[i], want[i])
		}
	}
	if page.Total != 40 || page.Offset != 10 || page.NextOffset != 13 || page.Dropped != 0 {
		t.Errorf("page = total %d offset %d next %d dropped %d, want 40 10 13 0", page.Total, page.Offset, page.NextOffset, page.Dropped)
	}
	if e.Name() != "everything" {
		t.Errorf("Name() = %q", e.Name())
	}
}

func TestEverythingSearchDefaultLimit(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
	e := newTestEverything(t, eng.URL, nil)
	page, err := e.Search(context.Background(), Query{Text: "a", Root: `D:\r`})
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if query, _, _ := eng.last(); query.Get("count") != strconv.Itoa(DefaultLimit) || query.Get("offset") != "0" {
		t.Errorf("count/offset = %q/%q, want %d/0", query.Get("count"), query.Get("offset"), DefaultLimit)
	}
	if page.Items == nil || len(page.Items) != 0 || page.Total != 0 || page.NextOffset != -1 {
		t.Errorf("empty page = %+v", page)
	}
}

func TestEverythingPaging(t *testing.T) {
	tests := []struct {
		name                    string
		total, offset, returned int
		wantNext                int
	}{
		{"first of three pages", 130, 0, 50, 50},
		{"middle page", 130, 50, 50, 100},
		{"last short page", 130, 100, 30, -1},
		{"exactly the last item", 130, 129, 1, -1},
		{"page ends on the total", 100, 50, 50, -1},
		{"offset past the end", 130, 130, 0, -1},
		{"far past the end", 130, 5000, 0, -1},
		{"no matches", 0, 0, 0, -1},
		{"everything fits", 5, 0, 5, -1},
		{"engine returns nothing although more remain", 10, 0, 0, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var items []string
			for i := 0; i < tt.returned; i++ {
				items = append(items, fmt.Sprintf(`{"type":"file","name":"f%03d.txt","path":"D:\\r","size":"1","date_modified":"%s"}`, tt.offset+i, fileTime20240305))
			}
			eng := newEngine(t, respondJSON(fmt.Sprintf(`{"totalResults":%d,"results":[%s]}`, tt.total, strings.Join(items, ","))))
			e := newTestEverything(t, eng.URL, nil)
			page, err := e.Search(context.Background(), Query{Text: "f", Root: `D:\r`, Offset: tt.offset, Limit: 50})
			if err != nil {
				t.Fatalf("Search error = %v", err)
			}
			if page.Total != tt.total || page.NextOffset != tt.wantNext || len(page.Items) != tt.returned || page.Offset != tt.offset {
				t.Errorf("page = total %d next %d items %d offset %d, want %d %d %d %d",
					page.Total, page.NextOffset, len(page.Items), page.Offset, tt.total, tt.wantNext, tt.returned, tt.offset)
			}
		})
	}
}

func TestEverythingRejectsBadQueriesWithoutCallingTheEngine(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
	e := newTestEverything(t, eng.URL, nil)
	valid := Query{Text: "a", Root: `D:\r`}
	tests := []struct {
		name   string
		mutate func(*Query)
	}{
		{"empty text", func(q *Query) { q.Text = "" }},
		{"blank text", func(q *Query) { q.Text = "  \t " }},
		{"only wildcards", func(q *Query) { q.Text = "*" }},
		{"long text", func(q *Query) { q.Text = strings.Repeat("a", MaxTextLength+1) }},
		{"control character", func(q *Query) { q.Text = "a\x00b" }},
		{"too many terms", func(q *Query) { q.Text = strings.Repeat("a ", MaxTerms+1) }},
		{"empty root", func(q *Query) { q.Root = "" }},
		{"relative root", func(q *Query) { q.Root = `data\src` }},
		{"traversal root", func(q *Query) { q.Root = `D:\a\..\b` }},
		{"negative offset", func(q *Query) { q.Offset = -1 }},
		{"huge offset", func(q *Query) { q.Offset = 1 << 40 }},
		{"negative limit", func(q *Query) { q.Limit = -1 }},
		{"limit above the maximum", func(q *Query) { q.Limit = MaxLimit + 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := valid
			tt.mutate(&q)
			if _, err := e.Search(context.Background(), q); !errors.Is(err, ErrBadQuery) {
				t.Errorf("Search error = %v, want ErrBadQuery", err)
			}
		})
	}
	if n := eng.hits.Load(); n != 0 {
		t.Errorf("engine was called %d times for invalid queries", n)
	}
	if _, err := e.Search(context.Background(), Query{Text: "a", Root: `D:\r`, Limit: MaxLimit}); err != nil {
		t.Errorf("Limit == MaxLimit error = %v", err)
	}
}

func TestEverythingRootPostFilter(t *testing.T) {
	body := `{"totalResults":9,"results":[
		{"type":"file","name":"a.txt","path":"D:\\data\\src","size":"1","date_modified":""},
		{"type":"file","name":"b.txt","path":"d:\\DATA\\Src\\sub","size":"1","date_modified":""},
		{"type":"file","name":"c.txt","path":"D:\\data\\src2","size":"1","date_modified":""},
		{"type":"folder","name":"src","path":"D:\\data","size":"","date_modified":""},
		{"type":"file","name":"d.txt","path":"D:\\data\\src\\..\\other","size":"1","date_modified":""},
		{"type":"file","name":"e.txt","path":"E:\\data\\src","size":"1","date_modified":""},
		{"type":"folder","name":"D:\\data\\src\\idx","path":"","size":"","date_modified":""},
		{"type":"file","name":"","path":"D:\\data\\src","size":"1","date_modified":""},
		{"type":"file","name":"g.txt","path":"\\\\srv\\share","size":"1","date_modified":""}
	]}`
	eng := newEngine(t, respondJSON(body))
	e := newTestEverything(t, eng.URL, nil)

	for _, root := range []string{`D:\data\src`, `D:\data\src\`, `D:/data/src`, `d:\DATA\SRC\`} {
		page, err := e.Search(context.Background(), Query{Text: "x", Root: root, Limit: 50})
		if err != nil {
			t.Fatalf("Search(root %q) error = %v", root, err)
		}
		var got []string
		for _, it := range page.Items {
			got = append(got, it.Path)
		}
		want := []string{`D:\data\src\a.txt`, `d:\DATA\Src\sub\b.txt`, `D:\data\src\idx`}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("root %q kept %q, want %q", root, got, want)
		}
		if page.Dropped != 6 || page.Total != 9 || page.NextOffset != -1 {
			t.Errorf("root %q: dropped %d total %d next %d, want 6 9 -1", root, page.Dropped, page.Total, page.NextOffset)
		}
		if idx := page.Items[2]; idx.Name != "idx" || !idx.IsDir {
			t.Errorf("index root entry = %+v, want folder named idx", idx)
		}
	}

	// A sibling that only shares the name prefix must never be accepted.
	page, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\data\src2`, Limit: 50})
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Path != `D:\data\src2\c.txt` || page.Dropped != 8 {
		t.Errorf("root src2: items %+v dropped %d", page.Items, page.Dropped)
	}
}

func TestEverythingTolerantJSON(t *testing.T) {
	tests := []struct {
		name     string
		item     string
		wantSize int64
		wantDir  bool
		wantTime time.Time
	}{
		{"observed shape", `{"type":"file","name":"x","path":"D:\\r","size":"11","date_modified":"` + fileTime20240305 + `"}`, 11, false, time20240305},
		{"numeric size and date", `{"type":"file","name":"x","path":"D:\\r","size":11,"date_modified":` + fileTime20240305 + `}`, 11, false, time20240305},
		{"zero size", `{"type":"file","name":"x","path":"D:\\r","size":"0"}`, 0, false, time.Time{}},
		{"empty size", `{"type":"folder","name":"x","path":"D:\\r","size":""}`, -1, true, time.Time{}},
		{"missing size and date", `{"type":"file","name":"x","path":"D:\\r"}`, -1, false, time.Time{}},
		{"null size and date", `{"type":"file","name":"x","path":"D:\\r","size":null,"date_modified":null}`, -1, false, time.Time{}},
		{"negative size", `{"type":"file","name":"x","path":"D:\\r","size":"-1"}`, -1, false, time.Time{}},
		{"unknown size marker", `{"type":"file","name":"x","path":"D:\\r","size":"18446744073709551615"}`, -1, false, time.Time{}},
		{"text size", `{"type":"file","name":"x","path":"D:\\r","size":"1 KB"}`, -1, false, time.Time{}},
		{"structured size", `{"type":"file","name":"x","path":"D:\\r","size":{"a":1},"date_modified":[1]}`, -1, false, time.Time{}},
		{"rfc3339 date", `{"type":"file","name":"x","path":"D:\\r","size":"1","date_modified":"2024-03-05T06:07:08Z"}`, 1, false, time20240305},
		{"rfc3339 date with offset", `{"type":"file","name":"x","path":"D:\\r","size":"1","date_modified":"2024-03-05T14:07:08+08:00"}`, 1, false, time20240305},
		{"rfc3339 date with fraction", `{"type":"file","name":"x","path":"D:\\r","size":"1","date_modified":"2024-03-05T06:07:08.5Z"}`, 1, false, time20240305.Add(500 * time.Millisecond)},
		{"zero filetime", `{"type":"file","name":"x","path":"D:\\r","size":"1","date_modified":"0"}`, 1, false, time.Time{}},
		{"unknown filetime marker", `{"type":"file","name":"x","path":"D:\\r","size":"1","date_modified":"18446744073709551615"}`, 1, false, time.Time{}},
		{"unix seconds are not guessed", `{"type":"file","name":"x","path":"D:\\r","size":"1","date_modified":"1709618828"}`, 1, false, time.Time{}},
		{"garbage date", `{"type":"file","name":"x","path":"D:\\r","size":"1","date_modified":"yesterday"}`, 1, false, time.Time{}},
		{"capitalised type", `{"type":"Folder","name":"x","path":"D:\\r","size":""}`, -1, true, time.Time{}},
		{"missing type", `{"name":"x","path":"D:\\r","size":"3"}`, 3, false, time.Time{}},
		{"unknown fields are ignored", `{"type":"file","name":"x","path":"D:\\r","size":"3","extra":{"a":[1,2]},"offset":0}`, 3, false, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := newEngine(t, respondJSON(`{"totalResults":1,"results":[`+tt.item+`]}`))
			e := newTestEverything(t, eng.URL, nil)
			page, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`})
			if err != nil {
				t.Fatalf("Search error = %v", err)
			}
			if len(page.Items) != 1 {
				t.Fatalf("Items = %+v, Dropped %d", page.Items, page.Dropped)
			}
			it := page.Items[0]
			if it.Size != tt.wantSize || it.IsDir != tt.wantDir || !it.ModTime.Equal(tt.wantTime) {
				t.Errorf("item = size %d dir %v time %v, want %d %v %v", it.Size, it.IsDir, it.ModTime, tt.wantSize, tt.wantDir, tt.wantTime)
			}
			if it.Path != `D:\r\x` || it.Name != "x" {
				t.Errorf("item name/path = %q/%q", it.Name, it.Path)
			}
		})
	}
}

func TestEverythingTolerantEnvelope(t *testing.T) {
	ok := map[string]int{
		`{"totalResults":5,"results":[]}`:            5,
		`{"totalResults":"5","results":[]}`:          5,
		`{"totalResults":0}`:                         0,
		`{"totalResults":3,"results":null}`:          3,
		`{"results":[],"totalResults":7,"x":true}`:   7,
		" \n{\"totalResults\":2,\"results\":[]}\n\n": 2,
	}
	for body, want := range ok {
		eng := newEngine(t, respondJSON(body))
		page, err := newTestEverything(t, eng.URL, nil).Search(context.Background(), Query{Text: "x", Root: `D:\r`})
		if err != nil || page.Total != want {
			t.Errorf("body %q: total %d error %v, want %d", body, page.Total, err, want)
		}
	}
	bad := []string{
		``, `   `, `not json`, `{`, `[]`, `null`, `"text"`, `<html></html>`,
		`{}`, `{"results":[]}`, `{"totalResults":"many","results":[]}`, `{"totalResults":-1,"results":[]}`,
		`{"totalResults":1.5,"results":[]}`, `{"totalResults":99999999999,"results":[]}`, `{"totalResults":true}`,
		`{"totalResults":1,"results":{}}`, `{"totalResults":1,"results":[{"name":5}]}`, `{"totalResults":1,"results":[1]}`,
	}
	for _, body := range bad {
		eng := newEngine(t, respondJSON(body))
		if _, err := newTestEverything(t, eng.URL, nil).Search(context.Background(), Query{Text: "x", Root: `D:\r`}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("body %q: error = %v, want ErrUnavailable", body, err)
		}
	}
}

func TestEverythingHTTPFailures(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusNoContent} {
		eng := newEngine(t, respondStatus(code))
		_, err := newTestEverything(t, eng.URL, nil).Search(context.Background(), Query{Text: "x", Root: `D:\r`})
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("HTTP %d: error = %v, want ErrUnavailable", code, err)
			continue
		}
		if !strings.Contains(err.Error(), strconv.Itoa(code)) {
			t.Errorf("HTTP %d: error %q does not name the status", code, err)
		}
	}
}

func TestEverythingConnectionRefused(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":0}`))
	e := newTestEverything(t, eng.URL, nil)
	eng.Close()
	if _, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("error = %v, want ErrUnavailable", err)
	}
}

func TestEverythingTimeout(t *testing.T) {
	eng := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	e := newTestEverything(t, eng.URL, func(o *EverythingOptions) { o.Timeout = 80 * time.Millisecond })
	start := time.Now()
	_, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want ErrUnavailable wrapping DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Search took %v with an 80ms timeout", elapsed)
	}
}

func TestEverythingTimeoutWhileReadingBody(t *testing.T) {
	eng := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write([]byte(`{"totalResults":`))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	e := newTestEverything(t, eng.URL, func(o *EverythingOptions) { o.Timeout = 80 * time.Millisecond })
	if _, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("error = %v, want ErrUnavailable", err)
	}
}

func TestEverythingCallerContext(t *testing.T) {
	block := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}
	eng := newEngine(t, block)
	e := newTestEverything(t, eng.URL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(60*time.Millisecond, cancel)
	_, err := e.Search(ctx, Query{Text: "x", Root: `D:\r`})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Errorf("canceled caller: error = %v, want context.Canceled and not ErrUnavailable", err)
	}

	deadline, stop := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer stop()
	_, err = e.Search(deadline, Query{Text: "x", Root: `D:\r`})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("caller deadline: error = %v, want ErrUnavailable wrapping DeadlineExceeded", err)
	}
}

func paddedJSON(size int) []byte {
	const doc = `{"totalResults":0,"results":[]}`
	return append([]byte(doc), bytes.Repeat([]byte(" "), size-len(doc))...)
}

func TestEverythingResponseSizeLimit(t *testing.T) {
	tests := []struct {
		name          string
		size          int
		contentLength bool
		wantErr       bool
	}{
		{"exactly the limit", maxBodyBytes, false, false},
		{"exactly the limit with content length", maxBodyBytes, true, false},
		{"one byte over", maxBodyBytes + 1, false, true},
		{"one byte over with content length", maxBodyBytes + 1, true, true},
		{"far over", 3 * maxBodyBytes, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := paddedJSON(tt.size)
			eng := newEngine(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentLength {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				}
				_, _ = w.Write(body)
			})
			e := newTestEverything(t, eng.URL, func(o *EverythingOptions) { o.Timeout = 10 * time.Second })
			_, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`})
			if tt.wantErr != errors.Is(err, ErrUnavailable) {
				t.Errorf("error = %v, want ErrUnavailable: %v", err, tt.wantErr)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("error = %v for a body at the limit", err)
			}
		})
	}
}

func TestEverythingDoesNotFollowRedirects(t *testing.T) {
	target := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
	redirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/?search=x", http.StatusFound)
	}
	for name, mutate := range map[string]func(*EverythingOptions){
		"default client": nil,
		"custom client":  func(o *EverythingOptions) { o.HTTPClient = &http.Client{} },
	} {
		t.Run(name, func(t *testing.T) {
			redirecting := newEngine(t, redirect)
			e := newTestEverything(t, redirecting.URL, mutate)
			_, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`})
			if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "302") {
				t.Errorf("error = %v, want ErrUnavailable mentioning 302", err)
			}
			if n := target.hits.Load(); n != 0 {
				t.Errorf("redirect target was contacted %d times", n)
			}
		})
	}
}

func TestEverythingBasicAuth(t *testing.T) {
	tests := []struct {
		name, user, pass, want string
	}{
		{"user and password", "svc", "pw", "Basic " + base64.StdEncoding.EncodeToString([]byte("svc:pw"))},
		{"password only", "", "pw", "Basic " + base64.StdEncoding.EncodeToString([]byte(":pw"))},
		{"user only", "svc", "", "Basic " + base64.StdEncoding.EncodeToString([]byte("svc:"))},
		{"none", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
			e := newTestEverything(t, eng.URL, func(o *EverythingOptions) { o.Username, o.Password = tt.user, tt.pass })
			if _, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`}); err != nil {
				t.Fatalf("Search error = %v", err)
			}
			if _, header, _ := eng.last(); header.Get("Authorization") != tt.want {
				t.Errorf("Authorization = %q, want %q", header.Get("Authorization"), tt.want)
			}
			e.Health(context.Background())
			if _, header, _ := eng.last(); header.Get("Authorization") != tt.want {
				t.Errorf("Health Authorization = %q, want %q", header.Get("Authorization"), tt.want)
			}
		})
	}
}

func TestEverythingNeverLeaksSecrets(t *testing.T) {
	const user, pass, text = "svc-user-7", "s3cr3t-Passw0rd", "confidential-plan"
	failures := map[string]http.HandlerFunc{
		"401":       respondStatus(http.StatusUnauthorized),
		"500":       respondStatus(http.StatusInternalServerError),
		"malformed": respondJSON(`{nope`),
		"no total":  respondJSON(`{"results":[]}`),
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/", http.StatusFound)
		},
		"oversize":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(paddedJSON(maxBodyBytes + 10)) },
		"early hang": func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
	}
	check := func(label, got string) {
		t.Helper()
		for _, secret := range []string{user, pass, text, base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))} {
			if strings.Contains(got, secret) {
				t.Errorf("%s leaks %q: %s", label, secret, got)
			}
		}
	}
	for name, handler := range failures {
		eng := newEngine(t, handler)
		e := newTestEverything(t, eng.URL, func(o *EverythingOptions) {
			o.Username, o.Password, o.Timeout = user, pass, 150*time.Millisecond
		})
		_, err := e.Search(context.Background(), Query{Text: text, Root: `D:\r`})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s: error = %v, want ErrUnavailable", name, err)
		}
		check(name+" error", err.Error())
		check(name+" health", e.Health(context.Background()).Detail)
	}

	dead := newEngine(t, respondStatus(200))
	e := newTestEverything(t, dead.URL, func(o *EverythingOptions) { o.Username, o.Password = user, pass })
	dead.Close()
	_, err := e.Search(context.Background(), Query{Text: text, Root: `D:\r`})
	check("refused error", err.Error())
	check("refused health", e.Health(context.Background()).Detail)

	opts := EverythingOptions{BaseURL: "http://127.0.0.1:1", Username: user, Password: pass}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		check("option "+verb, fmt.Sprintf(verb, opts))
		check("provider "+verb, fmt.Sprintf(verb, newTestEverything(t, dead.URL, func(o *EverythingOptions) { o.Username, o.Password = user, pass })))
	}
}

func TestNewEverythingBaseURL(t *testing.T) {
	allowed := []string{
		"http://127.0.0.1:38480", "http://127.0.0.1:38480/", "http://127.1.2.3:8080/", "https://127.0.0.1",
		"http://[::1]:8080", "http://localhost", "http://LOCALHOST:8080", "HTTP://127.0.0.1:80",
		"http://127.0.0.1:38480/prefix/", "  http://127.0.0.1:1  ", "http://localhost:65535",
	}
	for _, raw := range allowed {
		if _, err := NewEverything(EverythingOptions{BaseURL: raw}); err != nil {
			t.Errorf("NewEverything(%q) error = %v, want success", raw, err)
		}
	}
	rejected := []string{
		"", " ", "127.0.0.1:38480", "localhost:38480", "//127.0.0.1", "http:127.0.0.1", "http://", "http:///path",
		"ftp://127.0.0.1", "file:///c:/x", "ws://127.0.0.1",
		"http://example.com", "http://192.168.1.10:80", "http://10.0.0.5", "http://0.0.0.0:80", "http://[::]:80",
		"http://[fe80::1]", "http://[::2]", "http://8.8.8.8", "http://localhost.evil.com", "http://127.0.0.1.evil.com",
		"http://evil.com#@127.0.0.1", "http://127.0.0.1@evil.com", "http://evil.com@127.0.0.1",
		"http://user:secret@127.0.0.1:1", "http://:secret@127.0.0.1", "http://user@127.0.0.1",
		"http://127.0.0.1?x=1", "http://127.0.0.1/?", "http://127.0.0.1/#x", "http://127.0.0.1/#", "http://127.0.0.1/a?b=c",
		"http://2130706433", "http://0x7f.0.0.1", "http://127.1", "http://[::ffff:8.8.8.8]", "http://[::1%25eth0]",
		"http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:99999", "http://127.0.0.1:abc",
	}
	for _, raw := range rejected {
		if e, err := NewEverything(EverythingOptions{BaseURL: raw}); err == nil {
			t.Errorf("NewEverything(%q) = %v, want an error", raw, e)
		}
	}
}

func TestNewEverythingErrorsNeverEchoTheURL(t *testing.T) {
	for _, raw := range []string{
		"http://zq-user-91:zq-pass-91@127.0.0.1:1",
		"http://zq-user-91:zq-pass-91@evil.example/",
		"http://127.0.0.1:1/?token=zq-pass-91",
		"zq-user-91:zq-pass-91@127.0.0.1",
		"http://zq-user-91:zq-pass-91@[::1",
	} {
		_, err := NewEverything(EverythingOptions{BaseURL: raw})
		if err == nil {
			t.Errorf("NewEverything(%q) succeeded", raw)
			continue
		}
		if strings.Contains(err.Error(), "zq-") {
			t.Errorf("NewEverything(%q) error leaks the URL: %v", raw, err)
		}
	}
}

func TestNewEverythingTimeout(t *testing.T) {
	for _, tt := range []struct {
		timeout time.Duration
		want    time.Duration
		wantErr bool
	}{
		{0, 5 * time.Second, false},
		{time.Nanosecond, time.Nanosecond, false},
		{30 * time.Second, 30 * time.Second, false},
		{30*time.Second + time.Nanosecond, 0, true},
		{time.Hour, 0, true},
		{-time.Second, 0, true},
	} {
		e, err := NewEverything(EverythingOptions{BaseURL: "http://127.0.0.1:1", Timeout: tt.timeout})
		if tt.wantErr {
			if err == nil {
				t.Errorf("Timeout %v accepted", tt.timeout)
			}
			continue
		}
		if err != nil || e.timeout != tt.want {
			t.Errorf("Timeout %v: timeout %v error %v, want %v", tt.timeout, e.timeout, err, tt.want)
		}
	}
}

func TestEverythingBaseURLVariants(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
	for _, tt := range []struct{ suffix, wantPath string }{{"", "/"}, {"/", "/"}, {"/everything", "/everything/"}, {"/everything/", "/everything/"}} {
		e := newTestEverything(t, eng.URL+tt.suffix, nil)
		if _, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`}); err != nil {
			t.Fatalf("base %q: error = %v", tt.suffix, err)
		}
		if _, _, path := eng.last(); path != tt.wantPath {
			t.Errorf("base %q requested path %q, want %q", tt.suffix, path, tt.wantPath)
		}
	}

	local := strings.Replace(eng.URL, "127.0.0.1", "localhost", 1)
	if _, err := newTestEverything(t, local, nil).Search(context.Background(), Query{Text: "x", Root: `D:\r`}); err != nil {
		t.Errorf("localhost base URL: error = %v", err)
	}
}

func TestEverythingIPv6Loopback(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is not available: %v", err)
	}
	srv := httptest.NewUnstartedServer(respondJSON(`{"totalResults":0,"results":[]}`))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	if _, err := newTestEverything(t, srv.URL, nil).Search(context.Background(), Query{Text: "x", Root: `D:\r`}); err != nil {
		t.Errorf("IPv6 loopback: error = %v", err)
	}
}

func TestEverythingUsesTheSuppliedClient(t *testing.T) {
	eng := newEngine(t, respondJSON(`{"totalResults":0,"results":[]}`))
	var calls atomic.Int32
	custom := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	e := newTestEverything(t, eng.URL, func(o *EverythingOptions) { o.HTTPClient = custom })
	if _, err := e.Search(context.Background(), Query{Text: "x", Root: `D:\r`}); err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("supplied transport used %d times, want 1", calls.Load())
	}
	if custom.CheckRedirect != nil {
		t.Error("the supplied client was modified")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRequireLoopback(t *testing.T) {
	for address, wantErr := range map[string]bool{
		"127.0.0.1:80": false, "127.9.9.9:1": false, "[::1]:8080": false,
		"8.8.8.8:80": true, "192.168.0.1:80": true, "[2001:db8::1]:80": true, "0.0.0.0:80": true, "localhost:80": true, "bad": true,
	} {
		if err := requireLoopback("tcp", address, nil); (err != nil) != wantErr {
			t.Errorf("requireLoopback(%q) error = %v, want error: %v", address, err, wantErr)
		}
	}
}

func TestEverythingHealth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		eng := newEngine(t, respondJSON(`{"totalResults":171,"results":[]}`))
		h := newTestEverything(t, eng.URL, nil).Health(context.Background())
		if h.State != StateOK || h.Detail != "" || h.CheckedAt.IsZero() || h.Latency < 0 {
			t.Errorf("Health = %+v, want ok", h)
		}
		query, _, _ := eng.last()
		if query.Get("search") != "*" || query.Get("count") != "0" || query.Get("json") != "1" || len(query) != 3 {
			t.Errorf("health request = %v, want a count-only search", query)
		}
	})
	degraded := map[string]http.HandlerFunc{
		"empty index":  respondJSON(`{"totalResults":0,"results":[]}`),
		"unauthorized": respondStatus(http.StatusUnauthorized),
		"server error": respondStatus(http.StatusInternalServerError),
		"malformed":    respondJSON(`{`),
		"slow":         func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
	}
	for name, handler := range degraded {
		t.Run(name, func(t *testing.T) {
			eng := newEngine(t, handler)
			e := newTestEverything(t, eng.URL, func(o *EverythingOptions) { o.Timeout = 100 * time.Millisecond })
			h := e.Health(context.Background())
			if h.State != StateDegraded || h.Detail == "" || h.CheckedAt.IsZero() {
				t.Errorf("Health = %+v, want degraded with a detail", h)
			}
			if len(h.Detail) > 200 || strings.Contains(h.Detail, ErrUnavailable.Error()) {
				t.Errorf("Detail %q is not short and plain", h.Detail)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		eng := newEngine(t, respondStatus(200))
		e := newTestEverything(t, eng.URL, nil)
		eng.Close()
		if h := e.Health(context.Background()); h.State != StateDegraded || h.Detail == "" {
			t.Errorf("Health = %+v, want degraded", h)
		}
	})
}
