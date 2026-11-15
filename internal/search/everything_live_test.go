package search

// Live integration test of the Everything adapter against a real Everything
// 1.4 instance. It creates no files and is skipped unless
// TIMEVAULT_TEST_EVERYTHING_URL is set.
//
// Start the isolated test instance and run the test (PowerShell):
//
//	$s = 'powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-everything.ps1'
//	Invoke-Expression "$s -Action fixture -IndexRoot data\everything-test"
//	Invoke-Expression "$s -Action start -IndexRoot data\everything-test"
//	. .\scripts\dev-env.ps1        # loads data\everything\everything.env, written by start
//	go test ./internal/search/... -run Live -count=1 -v
//	Invoke-Expression "$s -Action stop"
//
// "-Action status -ShowEnv" emits the same variables as PowerShell assignments.
//
// Environment (the test is skipped when URL or ROOT is missing):
//
//	TIMEVAULT_TEST_EVERYTHING_URL       base URL of the instance, e.g. http://127.0.0.1:38480
//	TIMEVAULT_TEST_EVERYTHING_USERNAME  optional HTTP Basic user name
//	TIMEVAULT_TEST_EVERYTHING_PASSWORD  optional HTTP Basic password
//	TIMEVAULT_TEST_EVERYTHING_ROOT      absolute <IndexRoot>\source folder of the fixture
//
// The fixture is created by "dev-everything.ps1 -Action fixture" and must be
// indexed by the instance. <root> is TIMEVAULT_TEST_EVERYTHING_ROOT and <root>2
// is its sibling, which shares the path prefix:
//
//	<root>\tv-fixture-marker.txt
//	<root>\readme.txt                 11 bytes "hello world", modified 2024-03-05T06:07:08Z
//	<root>\needle top.txt
//	<root>\docs\report 2024 v1.docx
//	<root>\docs\报告 2024 v2.docx
//	<root>\docs\café menü.txt
//	<root>\docs\a_b.txt, a_c.txt      look-alikes of "a|b" (| cannot occur in a name)
//	<root>\docs\has!x.txt             the only name containing "!x"
//	<root>\docs\x plain.txt
//	<root>\docs\size gt1.txt
//	<root>\docs\emoji 😀 needle.txt
//	<root>\docs\deep\er\est\buried needle.txt
//	<root>\文档 café\inner needle.txt
//	<root>\sub dir with spaces\spaced needle.txt
//	<root>\folder needle\inside.txt
//	<root>\odd !;,'^&()[]{}$#@+=~ dir\odd needle.txt
//	<root>\pct %TEMP% dir\pct needle.txt
//	<root>\bulk\bulk-001.dat ... bulk-130.dat
//	<root>2\tv-fixture-marker.txt, outside needle.txt, 报告 2024 v2.docx, has!x.txt
//	<root>2\bulk\bulk-001.dat ... bulk-005.dat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type liveEnv struct {
	url, user, pass string
	root, sibling   string
	engine          *Everything
}

func newLiveEnv(t *testing.T) liveEnv {
	t.Helper()
	env := liveEnv{
		url:  strings.TrimRight(os.Getenv("TIMEVAULT_TEST_EVERYTHING_URL"), "/"),
		user: os.Getenv("TIMEVAULT_TEST_EVERYTHING_USERNAME"),
		pass: os.Getenv("TIMEVAULT_TEST_EVERYTHING_PASSWORD"),
		root: strings.TrimRight(os.Getenv("TIMEVAULT_TEST_EVERYTHING_ROOT"), `\/`),
	}
	if env.url == "" {
		t.Skip("TIMEVAULT_TEST_EVERYTHING_URL is not set; skipping the live Everything test")
	}
	if env.root == "" {
		t.Skip("TIMEVAULT_TEST_EVERYTHING_ROOT is not set: run scripts\\dev-everything.ps1 -Action fixture and restart the instance")
	}
	env.sibling = env.root + "2"
	engine, err := NewEverything(EverythingOptions{BaseURL: env.url, Username: env.user, Password: env.pass, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("NewEverything error = %v", err)
	}
	env.engine = engine
	return env
}

// search runs one query and checks what must hold for every query: no error,
// and no result the engine returned outside the root (Dropped == 0 proves the
// engine-side confinement worked, not only the re-check).
func (e liveEnv) search(t *testing.T, root, text string, offset, limit int) Page {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	page, err := e.engine.Search(ctx, Query{Text: text, Root: root, Offset: offset, Limit: limit})
	if err != nil {
		t.Fatalf("Search(%q in %q) error = %v", text, root, err)
	}
	if page.Dropped != 0 {
		t.Errorf("Search(%q in %q) dropped %d results the engine returned outside the root", text, root, page.Dropped)
	}
	parsed, err := parseRoot(root)
	if err != nil {
		t.Fatalf("parseRoot(%q) error = %v", root, err)
	}
	for _, it := range page.Items {
		if !parsed.contains(it.Path) {
			t.Errorf("Search(%q in %q) returned %q outside the root", text, root, it.Path)
		}
	}
	return page
}

func liveNames(page Page) []string {
	out := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, it.Name)
	}
	sort.Strings(out)
	return out
}

func assertLiveNames(t *testing.T, label string, page Page, want ...string) {
	t.Helper()
	want = append([]string(nil), want...)
	sort.Strings(want)
	if got := liveNames(page); strings.Join(got, "|") != strings.Join(want, "|") || page.Total != len(want) {
		t.Errorf("%s: names %q total %d, want %q total %d", label, got, page.Total, want, len(want))
	}
}

func liveContains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func TestEverythingLiveHealth(t *testing.T) {
	env := newLiveEnv(t)
	h := env.engine.Health(context.Background())
	if h.State != StateOK || h.Latency <= 0 || h.CheckedAt.IsZero() {
		t.Errorf("Health = %+v, want ok with a latency", h)
	}
}

func TestEverythingLiveSearchResults(t *testing.T) {
	env := newLiveEnv(t)

	needles := []string{
		"buried needle.txt", "emoji 😀 needle.txt", "folder needle", "inner needle.txt",
		"needle top.txt", "odd needle.txt", "pct needle.txt", "spaced needle.txt",
	}
	page := env.search(t, env.root, "needle", 0, 0)
	assertLiveNames(t, "needle", page, needles...)
	if page.NextOffset != -1 || page.Offset != 0 {
		t.Errorf("needle page offsets = %d/%d, want 0/-1", page.Offset, page.NextOffset)
	}
	for _, it := range page.Items {
		if it.IsDir != (it.Name == "folder needle") {
			t.Errorf("%q IsDir = %v", it.Name, it.IsDir)
		}
		if it.IsDir && it.Size != -1 && it.Size != 6 {
			t.Errorf("folder size = %d, want -1 (not indexed) or 6", it.Size)
		}
	}

	page = env.search(t, env.root, "README", 0, 0)
	if len(page.Items) != 1 {
		t.Fatalf("README = %+v", page.Items)
	}
	readme := page.Items[0]
	wantTime := time.Date(2024, 3, 5, 6, 7, 8, 0, time.UTC)
	if readme.Name != "readme.txt" || readme.Path != env.root+`\readme.txt` || readme.IsDir || readme.Size != 11 || !readme.ModTime.Equal(wantTime) {
		t.Errorf("readme = %+v, want readme.txt of 11 bytes modified %v", readme, wantTime)
	}

	assertLiveNames(t, "unicode term", env.search(t, env.root, "报告", 0, 0), "报告 2024 v2.docx")
	assertLiveNames(t, "unicode term in the sibling", env.search(t, env.sibling, "报告", 0, 0), "报告 2024 v2.docx")
	assertLiveNames(t, "accented term", env.search(t, env.root, "café", 0, 0), "café menü.txt", "文档 café")
	assertLiveNames(t, "emoji term", env.search(t, env.root, "😀", 0, 0), "emoji 😀 needle.txt")
	assertLiveNames(t, "phrase with a space", env.search(t, env.root, `"x p"`, 0, 0), "x plain.txt")
	assertLiveNames(t, "two terms", env.search(t, env.root, "needle spaced", 0, 0), "spaced needle.txt")
	assertLiveNames(t, "wildcards split terms", env.search(t, env.root, "nee*le", 0, 0), needles...)
	assertLiveNames(t, "no match", env.search(t, env.root, "no-such-file-anywhere", 0, 0))
}

func TestEverythingLivePaging(t *testing.T) {
	env := newLiveEnv(t)
	bulk := func(from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, fmt.Sprintf("bulk-%03d.dat", i))
		}
		return out
	}
	ordered := func(page Page) []string {
		var out []string
		for _, it := range page.Items {
			out = append(out, it.Name)
		}
		return out
	}

	summary := func(list []string) string {
		if len(list) == 0 {
			return "no names"
		}
		return fmt.Sprintf("%d names from %q to %q", len(list), list[0], list[len(list)-1])
	}

	for _, tt := range []struct {
		offset, limit, wantNext int
		wantFrom, wantTo        int
	}{
		{0, 50, 50, 1, 50},
		{50, 50, 100, 51, 100},
		{100, 50, -1, 101, 130},
		{0, 200, -1, 1, 130},
		{129, 1, -1, 130, 130},
		{0, 0, DefaultLimit, 1, DefaultLimit},
		{130, 50, -1, 1, 0},
		{5000, 50, -1, 1, 0},
	} {
		page := env.search(t, env.root, "bulk-", tt.offset, tt.limit)
		if page.Total != 130 || page.Offset != tt.offset || page.NextOffset != tt.wantNext {
			t.Errorf("offset %d limit %d: total %d offset %d next %d, want 130 %d %d", tt.offset, tt.limit, page.Total, page.Offset, page.NextOffset, tt.offset, tt.wantNext)
		}
		if got, want := ordered(page), bulk(tt.wantFrom, tt.wantTo); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("offset %d limit %d: got %s, want %s", tt.offset, tt.limit, summary(got), summary(want))
		}
	}

	var all []string
	for offset, pages := 0, 0; offset != -1; pages++ {
		if pages > 10 {
			t.Fatal("NextOffset never reached -1")
		}
		page := env.search(t, env.root, "bulk-", offset, 37)
		all = append(all, ordered(page)...)
		offset = page.NextOffset
	}
	if strings.Join(all, "|") != strings.Join(bulk(1, 130), "|") {
		t.Errorf("following NextOffset gave %s, want %s", summary(all), summary(bulk(1, 130)))
	}
}

func TestEverythingLiveFolderScoping(t *testing.T) {
	env := newLiveEnv(t)

	assertLiveNames(t, "sibling files stay out", env.search(t, env.root, "outside", 0, 0))
	assertLiveNames(t, "sibling root", env.search(t, env.sibling, "outside", 0, 0), "outside needle.txt")
	if page := env.search(t, env.sibling, "bulk-", 0, 0); page.Total != 5 {
		t.Errorf("sibling bulk total = %d, want 5", page.Total)
	}
	if page := env.search(t, env.root, "bulk-", 0, 0); page.Total != 130 {
		t.Errorf("bulk total = %d, want 130 (the sibling's copies must not count)", page.Total)
	}

	assertLiveNames(t, "sub-folder includes its own sub-folders", env.search(t, env.root+`\docs`, "needle", 0, 0), "buried needle.txt", "emoji 😀 needle.txt")
	assertLiveNames(t, "deep sub-folder", env.search(t, env.root+`\docs\deep`, "needle", 0, 0), "buried needle.txt")
	assertLiveNames(t, "root with spaces", env.search(t, env.root+`\sub dir with spaces`, "needle", 0, 0), "spaced needle.txt")
	assertLiveNames(t, "root with non-ASCII characters", env.search(t, env.root+`\文档 café`, "needle", 0, 0), "inner needle.txt")
	assertLiveNames(t, "root with shell and Everything punctuation", env.search(t, env.root+`\odd !;,'^&()[]{}$#@+=~ dir`, "needle", 0, 0), "odd needle.txt")
	assertLiveNames(t, "root with a percent pair", env.search(t, env.root+`\pct %TEMP% dir`, "needle", 0, 0), "pct needle.txt")

	docs := env.root + `\docs`
	for _, variant := range []string{docs + `\`, strings.ReplaceAll(docs, `\`, "/"), strings.ReplaceAll(docs, `\`, "/") + "/", strings.ToUpper(docs), strings.ToLower(docs) + `\`} {
		assertLiveNames(t, "root written as "+variant, env.search(t, variant, "needle", 0, 0), "buried needle.txt", "emoji 😀 needle.txt")
	}
}

func TestEverythingLiveQuotedTermsStayLiteral(t *testing.T) {
	env := newLiveEnv(t)

	literal := []struct {
		name, text string
		want       []string
		orFewer    bool // the result depends on the instance's path-separator setting
	}{
		{name: "or operator", text: "a|b"},
		{name: "quoted or phrase", text: `"a|b c"`},
		{name: "not operator", text: "!x", want: []string{"has!x.txt"}},
		{name: "group operators", text: "<a b>"},
		{name: "quoted group", text: `"<a b>"`},
		{name: "lone operators", text: "| < >"},
		{name: "regex function", text: "regex:.*"},
		{name: "size function", text: "size:>1"},
		{name: "relative path", text: `..\..`},
		{name: "ext function", text: "ext:txt"},
		{name: "file function", text: "file:"},
		{name: "folder function", text: "folder:"},
		{name: "content function", text: "content:hello"},
		{name: "case modifier", text: "case:NEEDLE"},
		{name: "whole word modifier", text: "wholeword:needle"},
		{name: "path function", text: `path:C:\`},
		{name: "infolder function", text: `infolder:C:\`},
		{name: "parent function", text: `parent:C:\`},
		{name: "list separators", text: "a;b a,b"},
		{name: "or between known names", text: "needle|zzz"},
		{name: "quotes cannot be closed early", text: `needle" "|" "zzz`},
		{name: "trailing backslash term and a second term", text: `docs\ needle`, want: []string{"buried needle.txt", "emoji 😀 needle.txt"}, orFewer: true},
	}
	for _, tt := range literal {
		page := env.search(t, env.root, tt.text, 0, 0)
		label := fmt.Sprintf("%s %q", tt.name, tt.text)
		if !tt.orFewer {
			assertLiveNames(t, label, page, tt.want...)
			continue
		}
		for _, name := range liveNames(page) {
			if !liveContains(tt.want, name) {
				t.Errorf("%s: unexpected result %q", label, name)
			}
		}
	}

	// A drive prefix is a literal path fragment: it may match everything below
	// the root (or nothing if the instance ignores path separators in terms)
	// but never anything outside it.
	count := 0
	err := filepath.WalkDir(env.root, func(path string, _ fs.DirEntry, err error) error {
		if err == nil && path != env.root {
			count++
		}
		return err
	})
	if err != nil {
		t.Logf("fixture folder is not readable from the test (%v); skipping the count check", err)
		count = 1 << 30
	}
	volume := env.root[:strings.Index(env.root, `\`)+1]
	if page := env.search(t, env.root, volume, 0, MaxLimit); page.Total > count {
		t.Errorf("drive prefix %q matched %d items, more than the %d below the root", volume, page.Total, count)
	}
}

// The adapter's choice of path:"<root>\" rests on how Everything 1.4.1.1024
// scopes searches. This pins those facts so a different Everything build that
// behaves otherwise is noticed.
func TestEverythingLiveScopeSemantics(t *testing.T) {
	env := newLiveEnv(t)
	total := func(search string) int {
		t.Helper()
		query := url.Values{"search": {search}, "json": {"1"}, "count": {"0"}}
		req, err := http.NewRequest(http.MethodGet, env.url+"/?"+query.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if env.user != "" || env.pass != "" {
			req.SetBasicAuth(env.user, env.pass)
		}
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("probe failed: %v", errors.Unwrap(err))
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var doc struct {
			TotalResults int `json:"totalResults"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("probe answer is not JSON (HTTP %d)", resp.StatusCode)
		}
		return doc.TotalResults
	}

	if got := total(`infolder:"` + env.root + `" needle`); got != 2 {
		t.Errorf("infolder: matched %d needles, want 2 (direct children only)", got)
	}
	if got := total(`parent:"` + env.root + `" needle`); got != 2 {
		t.Errorf("parent: matched %d needles, want 2 (direct children only)", got)
	}
	if got := total(`path:"` + env.root + `" outside`); got != 1 {
		t.Errorf("path: without a trailing backslash matched %d, want 1 (it leaks into the sibling with the same prefix)", got)
	}
	if got := total(`path:"` + env.root + `\" outside`); got != 0 {
		t.Errorf("path: with a trailing backslash matched %d, want 0", got)
	}
	if got := total(`path:"` + env.root + `\" needle`); got != 8 {
		t.Errorf("path: with a trailing backslash matched %d needles, want 8 (all sub-folders)", got)
	}
}

func TestEverythingLiveFailures(t *testing.T) {
	env := newLiveEnv(t)
	secrets := []string{env.pass, env.user, "confidential-plan"}
	noSecrets := func(label string, err error, health Health) {
		t.Helper()
		for _, s := range secrets {
			if s == "" {
				continue
			}
			if strings.Contains(err.Error(), s) || strings.Contains(health.Detail, s) {
				t.Errorf("%s leaks a secret", label)
			}
		}
	}

	if env.pass != "" {
		wrong, err := NewEverything(EverythingOptions{BaseURL: env.url, Username: env.user, Password: env.pass + "-wrong", Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_, searchErr := wrong.Search(context.Background(), Query{Text: "confidential-plan", Root: env.root})
		health := wrong.Health(context.Background())
		if !errors.Is(searchErr, ErrUnavailable) || health.State != StateDegraded {
			t.Errorf("wrong password: error %v health %+v, want ErrUnavailable and degraded", searchErr, health)
		}
		noSecrets("wrong password", searchErr, health)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()
	down, err := NewEverything(EverythingOptions{BaseURL: closed, Username: env.user, Password: env.pass, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, searchErr := down.Search(context.Background(), Query{Text: "confidential-plan", Root: env.root})
	health := down.Health(context.Background())
	if !errors.Is(searchErr, ErrUnavailable) || health.State != StateDegraded {
		t.Errorf("closed port: error %v health %+v, want ErrUnavailable and degraded", searchErr, health)
	}
	noSecrets("closed port", searchErr, health)
}
