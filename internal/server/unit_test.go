package server

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"timevault/internal/audit"
	"timevault/internal/restore"
	"timevault/internal/service"
	"timevault/web"
)

var templateKey = regexp.MustCompile(`\.T "([a-zA-Z0-9_.]+)"`)

func TestTemplatesParse(t *testing.T) {
	pages, err := parseTemplates(web.FS, (&Server{}).funcs())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"login", "explorer", "history", "versions", "backups", "job", "restore_preview", "audit", "status", "settings", "error"} {
		if pages[name] == nil {
			t.Errorf("page template %q is missing", name)
		}
	}
}

func TestEveryTemplateKeyIsTranslated(t *testing.T) {
	files, err := fs.Glob(web.FS, "templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	for _, f := range files {
		body, err := fs.ReadFile(web.FS, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range templateKey.FindAllStringSubmatch(string(body), -1) {
			for _, lang := range []string{"en", "zh"} {
				if _, ok := messages[lang][m[1]]; !ok {
					t.Errorf("%s uses message %q which is missing in %q", f, m[1], lang)
				}
			}
		}
	}
}

var verbs = regexp.MustCompile(`%[sd]`)

func TestLanguageTablesAreInSync(t *testing.T) {
	en, zh := messages["en"], messages["zh"]
	var keys []string
	for k := range en {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		z, ok := zh[k]
		if !ok {
			t.Errorf("message %q has no Chinese translation", k)
			continue
		}
		if strings.TrimSpace(en[k]) == "" || strings.TrimSpace(z) == "" {
			t.Errorf("message %q is empty", k)
		}
		if a, b := len(verbs.FindAllString(en[k], -1)), len(verbs.FindAllString(z, -1)); a != b {
			t.Errorf("message %q has %d format verbs in English but %d in Chinese", k, a, b)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("message %q exists only in Chinese", k)
		}
	}
}

// The labels shown for stored values are looked up by prefix; every value the
// back end can produce needs a message, in both languages.
func TestVocabulariesAreTranslated(t *testing.T) {
	vocab := map[string][]string{
		"verify.":      {"pending", "validated", "verified", "failed"},
		"state.":       {"queued", "running", "succeeded", "failed", "canceled", "building", "committed", "aborted"},
		"phase.":       {"", "snapshot_preparing", "scanning", "manifest_validating", "committing", "committed", "verifying", "planning", "restoring", "done"},
		"jobkind.":     {"backup", "verify", "restore"},
		"kind.":        {"full", "incremental"},
		"policy.":      {"skip", "rename", "overwrite"},
		"item.":        {"ok", "skipped", "failed"},
		"auditresult.": {audit.ResultOK, audit.ResultDenied, audit.ResultFailed},
		"component.":   {"database", "repository", "search", "directory", "restore", "jobs", "vss", "usn"},
		"cstate.":      {"ok", "degraded", "disabled", "failed"},
		"live.":        {"protected", "changed", "new", "not_captured", "deleted"},
		"now.":         {"same", "modified", "deleted"},
		"consistency.": {"live_scan", "vss"},
		"counter.":     {"files", "dirs", "added", "modified", "deleted", "skipped", "new_objects", "bytes_hashed", "items", "created", "overwritten", "renamed", "failed", "bytes_written"},
		"auditaction.": auditActions,
	}
	for _, a := range []restore.Action{restore.ActionMkdir, restore.ActionDirExists, restore.ActionCreate, restore.ActionOverwrite,
		restore.ActionRename, restore.ActionSkipSame, restore.ActionSkipExists, restore.ActionBlocked} {
		vocab["action."] = append(vocab["action."], string(a))
	}
	for prefix, values := range vocab {
		for _, v := range values {
			for _, lang := range []string{"en", "zh"} {
				if _, ok := messages[lang][prefix+v]; !ok {
					t.Errorf("no %s message for %q", lang, prefix+v)
				}
			}
		}
	}
}

func TestErrorKeysExist(t *testing.T) {
	for _, key := range []string{"errors.generic", "errors.internal", "errors.not_found", "errors.bad_path", "errors.csrf", "errors.forbidden", "errors.restore_disabled",
		"errors.not_restorable", "errors.stale_preview", "errors.not_confirmed", "errors.blocked", "errors.source_unavailable", "errors.source_disabled", "errors.source_busy"} {
		if !messageExists(key) {
			t.Errorf("missing %s", key)
		}
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                       "/",
		"/explorer?source=1":     "/explorer?source=1",
		"/history?path=a/b":      "/history?path=a/b",
		"//evil.example":         "/",
		`/\evil.example`:         "/",
		`/./\evil.example`:       "/", // http.Redirect would clean this into /\evil.example
		`/a/..\evil.example`:     "/",
		"/\t/evil.example":       "/", // browsers drop the tab and read //evil.example
		"/\x00/evil.example":     "/",
		"https://evil.example":   "/",
		"javascript:alert(1)":    "/",
		"/ok\r\nSet-Cookie: x=1": "/",
		"relative":               "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// The service reports the English text for the API and a key for the web UI.
func TestStatusDetailsAreTranslatedWhenTheServiceGivesAKey(t *testing.T) {
	zh := &Page{Lang: "zh", loc: &Locale{lang: "zh"}}
	en := &Page{Lang: "en", loc: &Locale{lang: "en"}}
	hasChinese := func(s string) bool {
		for _, r := range s {
			if r >= 0x4e00 && r <= 0x9fff {
				return true
			}
		}
		return false
	}
	keyed := []service.Component{
		{Name: "database", State: "failed", Detail: "the database does not answer", Key: "db_down"},
		{Name: "repository", State: "ok", Detail: `D:\repo — 40% free`, Key: "repo_free", Args: []any{`D:\repo`, 40}},
		{Name: "search", State: "disabled", Detail: "no search engine configured", Key: "search_disabled"},
		{Name: "restore", State: "disabled", Detail: "no restore folder configured", Key: "restore_disabled"},
		{Name: "vss", State: "disabled", Detail: "not implemented in the MVP: x", Key: "vss_disabled"},
		{Name: "usn", State: "disabled", Detail: "not implemented in the MVP: y", Key: "usn_disabled"},
		{Name: "jobs", State: "ok", Detail: "2 running, 1 queued", Key: "jobs_detail", Args: []any{2, 1}},
	}
	for _, c := range keyed {
		if got := zh.ComponentDetail(c); !hasChinese(got) || strings.Contains(got, "%!") {
			t.Errorf("%s in Chinese = %q", c.Name, got)
		}
		if got := en.ComponentDetail(c); hasChinese(got) || got == "" || strings.Contains(got, "%!") {
			t.Errorf("%s in English = %q", c.Name, got)
		}
	}
	if got := zh.ComponentDetail(service.Component{Detail: `D:\restore`}); got != `D:\restore` {
		t.Errorf("a detail without a key must be shown as reported, got %q", got)
	}
	if got := zh.ComponentDetail(service.Component{Detail: "raw", Key: "no_such_key"}); got != "raw" {
		t.Errorf("an unknown key must fall back to the reported text, got %q", got)
	}
}

func TestAssetVersionFollowsTheContent(t *testing.T) {
	a := fstest.MapFS{"css/app.css": {Data: []byte("a{}")}, "js/app.js": {Data: []byte("1")}}
	same := fstest.MapFS{"js/app.js": {Data: []byte("1")}, "css/app.css": {Data: []byte("a{}")}}
	changed := fstest.MapFS{"css/app.css": {Data: []byte("a{color:red}")}, "js/app.js": {Data: []byte("1")}}
	renamed := fstest.MapFS{"css/site.css": {Data: []byte("a{}")}, "js/app.js": {Data: []byte("1")}}
	va, err := assetVersion(a)
	if err != nil || len(va) != 12 {
		t.Fatalf("assetVersion = %q, %v", va, err)
	}
	if v, _ := assetVersion(same); v != va {
		t.Errorf("the same content must give the same version: %q vs %q", v, va)
	}
	if v, _ := assetVersion(changed); v == va {
		t.Error("changed content must change the version")
	}
	if v, _ := assetVersion(renamed); v == va {
		t.Error("a renamed file must change the version")
	}
}

func TestHelpers(t *testing.T) {
	if got := humanBytes(0); got != "0 B" {
		t.Errorf("humanBytes(0) = %q", got)
	}
	if got := humanBytes(1536); got != "1.5 KB" {
		t.Errorf("humanBytes(1536) = %q", got)
	}
	if got := humanBytes(5 << 30); got != "5.0 GB" {
		t.Errorf("humanBytes(5GiB) = %q", got)
	}
	if got := percent(50, 200); got != 25 {
		t.Errorf("percent = %d", got)
	}
	if got := percent(5, 0); got != 0 {
		t.Errorf("percent with no total = %d", got)
	}
	if got := buildURL("/x", "a", "", "b", 0, "c", "v w", "d", int64(7), "e", true, "f", false); got != "/x?c=v+w&d=7&e=1" {
		t.Errorf("buildURL = %q", got)
	}
	if got := buildURL("/x"); got != "/x" {
		t.Errorf("buildURL without params = %q", got)
	}
	if got := fmt.Sprint(crumbsFor("a/b/c")); got != "[{a a false} {b a/b false} {c a/b/c true}]" {
		t.Errorf("crumbsFor = %s", got)
	}
}

func TestClassifyMapsErrorsToStatuses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{restore.ErrDisabled, 409}, {restore.ErrStalePreview, 409}, {restore.ErrNotConfirmed, 422}, {restore.ErrBlocked, 422},
		{restore.ErrNotRestorable, 409}, {fmt.Errorf("wrap: %w", restore.ErrInvalidRequest), 400}, {fmt.Errorf("boom"), 500},
	}
	for _, c := range cases {
		if got, _ := classify(c.err); got != c.want {
			t.Errorf("classify(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}
