package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// listFolder fills the folder "many" with 253 entries: 230 .txt files of 1 to 7 bytes, 20 .LOG files, one big
// file, one file without an extension and one sub folder.
func listFolder(h *harness) {
	for i := 0; i < 230; i++ {
		h.env.Write(fmt.Sprintf("many/doc%03d.txt", i), strings.Repeat("x", i%7+1))
	}
	for i := 0; i < 20; i++ {
		h.env.Write(fmt.Sprintf("many/log%02d.LOG", i), "log")
	}
	h.env.Write("many/big.bin", strings.Repeat("b", 5000))
	h.env.Write("many/README", "no extension")
	h.env.Write("many/sub/inner.txt", "inside")
}

func names(body string) []string {
	var out []string
	const open = `<span class="tv-name">`
	for {
		i := strings.Index(body, open)
		if i < 0 {
			return out
		}
		body = body[i+len(open):]
		out = append(out, body[:strings.Index(body, "</span>")])
	}
}

func entryNames(out map[string]any, key string) []string {
	var got []string
	for _, e := range out[key].([]any) {
		got = append(got, e.(map[string]any)["name"].(string))
	}
	return got
}

func TestFolderPagesSortFilterAndPage(t *testing.T) {
	h := newHarness(t)
	listFolder(h)
	h.env.Backup()
	src := itoa(h.env.Src.ID)
	alice := h.visitor()
	alice.loginAs(userID)
	_, out := alice.api("GET", "/api/v1/recovery-points?source_id="+src, nil)
	point := itoa(num(out["recovery_points"].([]any)[0].(map[string]any)["id"]))

	for _, base := range []string{"/explorer?source=" + src + "&path=many", "/history?source=" + src + "&point=" + point + "&path=many"} {
		t.Run(strings.SplitN(base, "?", 2)[0], func(t *testing.T) {
			get := func(query string) (*http.Response, string) {
				t.Helper()
				return alice.get(base + query)
			}

			// 100 a page by default, folders first, then by name; the sub folder leads.
			res, body := get("")
			got := names(body)
			if res.StatusCode != http.StatusOK || len(got) != 100 || got[0] != "sub" || got[1] != "big.bin" || got[2] != "doc000.txt" {
				t.Fatalf("first page = %d, %d names, starts with %v", res.StatusCode, len(got), got[:min(len(got), 3)])
			}
			if !strings.Contains(body, "Showing 1–100 of 253") || !strings.Contains(body, "page=2") || !strings.Contains(body, `rel="next"`) {
				t.Errorf("the first page does not say where it is:\n%s", body)
			}
			for _, label := range []string{">TXT<", ">Folder<"} {
				if !strings.Contains(body, label) {
					t.Errorf("the Type column lacks %s", label)
				}
			}

			if _, body := get("&page=3"); len(names(body)) != 53 || !strings.Contains(body, "Showing 201–253 of 253") || strings.Contains(body, `rel="next"`) {
				t.Errorf("last page: %d names\n%s", len(names(body)), body)
			}
			// A page past the end leads to the last one.
			if res, _ := get("&page=9"); res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "page=3") {
				t.Errorf("a page past the end = %d %q", res.StatusCode, res.Header.Get("Location"))
			}
			if _, body := get("&per_page=200&page=2"); len(names(body)) != 53 {
				t.Errorf("200 a page, second page: %d names", len(names(body)))
			}
			// A page size that is not offered means the default.
			if _, body := get("&per_page=7"); len(names(body)) != 100 {
				t.Errorf("an unknown page size: %d names", len(names(body)))
			}

			// Filter by type: the options say what the folder holds.
			_, body = get("&type=ext:log")
			if got := names(body); len(got) != 20 || !strings.Contains(body, "filtered from 253") || !strings.Contains(body, "LOG (20)") || !strings.Contains(body, "TXT (230)") {
				t.Errorf("filter by .log: %d names\n%s", len(got), body)
			}
			if _, body := get("&type=dir"); strings.Join(names(body), ",") != "sub" {
				t.Errorf("filter by folders: %v", names(body))
			}
			if _, body := get("&type=file"); len(names(body)) != 100 || strings.Contains(body, ">sub<") || !strings.Contains(body, "Showing 1–100 of 252") {
				t.Errorf("filter by files: %d names", len(names(body)))
			}
			if _, body := get("&type=noext"); strings.Join(names(body), ",") != "README" {
				t.Errorf("filter by no extension: %v", names(body))
			}
			if _, body := get("&type=ext:zip"); len(names(body)) != 0 || !strings.Contains(body, "No items of this type") {
				t.Errorf("a type nothing has: %v", names(body))
			}
			// A type that does not exist as a filter value is ignored.
			if _, body := get("&type=nonsense"); len(names(body)) != 100 {
				t.Errorf("a made-up type: %d names", len(names(body)))
			}

			// Sort: the page links keep the choice, and the direction can be reversed.
			_, body = get("&sort=size&order=desc")
			if got := names(body); got[0] != "sub" || got[1] != "big.bin" || got[2] != "README" || !strings.HasPrefix(got[3], "doc") || !strings.Contains(body, `aria-sort="descending"`) {
				t.Errorf("by size, biggest first: %v", got[:4])
			}
			if !strings.Contains(body, "sort=size") || !strings.Contains(body, "order=desc") {
				t.Errorf("the page links forget the sort:\n%s", body)
			}
			_, body = get("&sort=type")
			if got := names(body); got[0] != "sub" || got[1] != "README" || got[2] != "big.bin" || got[3] != "log00.LOG" || got[4] != "log01.LOG" {
				t.Errorf("by type: %v", got[:5])
			}
			_, body = get("&sort=type&order=desc&per_page=50&page=6")
			if got := names(body); strings.Join(got, ",") != "log00.LOG,big.bin,README" || !strings.Contains(body, "Showing 251–253 of 253") {
				t.Errorf("by type backwards, the last page: %v", got)
			}
			_, body = get("&sort=name&order=desc")
			if got := names(body); got[0] != "sub" || got[1] != "README" {
				t.Errorf("by name, backwards: %v", got[:3])
			}
		})
	}

	// Select all means the page: the form lists the items of one page only, 50 here.
	_, body := alice.get("/history?source=" + src + "&point=" + point + "&path=many&per_page=50&page=2")
	if got := strings.Count(body, `name="paths"`); got != 50 || !strings.Contains(body, "Select all selects the items on this page only.") {
		t.Errorf("selectable items on the second page of 50: %d", got)
	}
}

func TestFolderListsOfTheAPI(t *testing.T) {
	h := newHarness(t)
	listFolder(h)
	h.env.Backup()
	src := itoa(h.env.Src.ID)
	anon := h.visitor()
	_, out := anon.api("GET", "/api/v1/recovery-points?source_id="+src, nil)
	point := itoa(num(out["recovery_points"].([]any)[0].(map[string]any)["id"]))
	get := func(path string) map[string]any {
		t.Helper()
		status, out := anon.api("GET", path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d %v", path, status, out)
		}
		return out
	}
	files := "/api/v1/files?source_id=" + src + "&path=many"
	tree := "/api/v1/history/tree?source_id=" + src + "&point_id=" + point + "&path=many"

	for _, base := range []string{files, tree} {
		// Without parameters the order is by name, folders mixed in, as before.
		out := get(base + "&limit=3")
		if got := entryNames(out, "entries"); strings.Join(got, ",") != "big.bin,doc000.txt,doc001.txt" {
			t.Errorf("%s: default order = %v", base, got)
		}
		if first := out["entries"].([]any)[0].(map[string]any); first["type"] != "ext:bin" {
			t.Errorf("%s: type of an entry = %v", base, first)
		}

		out = get(base + "&sort=size&order=desc&limit=2")
		if got := entryNames(out, "entries"); got[0] != "big.bin" || num(out["total"]) != 253 || num(out["unfiltered"]) != 253 || num(out["next_offset"]) != 2 {
			t.Errorf("%s: by size = %v %v", base, got, out)
		}
		var facets = map[string]float64{}
		for _, f := range out["types"].([]any) {
			m := f.(map[string]any)
			facets[m["type"].(string)] = m["count"].(float64)
		}
		if facets["dir"] != 1 || facets["noext"] != 1 || facets["ext:txt"] != 230 || facets["ext:log"] != 20 || facets["ext:bin"] != 1 {
			t.Errorf("%s: type facets = %v", base, facets)
		}

		out = get(base + "&type=ext:txt&offset=225&limit=100")
		if got := entryNames(out, "entries"); len(got) != 5 || num(out["total"]) != 230 || num(out["unfiltered"]) != 253 || out["next_offset"] != nil {
			t.Errorf("%s: the end of a filtered list = %d entries %v", base, len(got), out)
		}
		out = get(base + "&dirs_first=true&limit=2")
		if got := entryNames(out, "entries"); got[0] != "sub" || got[1] != "big.bin" {
			t.Errorf("%s: folders first = %v", base, got)
		}
		out = get(base + "&sort=nonsense&type=nonsense&order=nonsense&limit=2")
		if got := entryNames(out, "entries"); got[0] != "big.bin" || num(out["total"]) != 253 {
			t.Errorf("%s: values that are not understood mean the defaults: %v", base, out)
		}
	}

	// The cursor of the history tree keeps working for callers that use it.
	out = get(tree + "&limit=100")
	cursor, _ := out["next_cursor"].(string)
	if len(out["entries"].([]any)) != 100 || cursor == "" {
		t.Fatalf("cursor paging = %d entries, cursor %q", len(out["entries"].([]any)), cursor)
	}
	out = get(tree + "&limit=100&cursor=" + url.QueryEscape(cursor))
	if got := entryNames(out, "entries"); len(got) != 100 || out["total"] != nil {
		t.Errorf("the second page by cursor = %d entries %v", len(got), out["total"])
	}

	// Search results and versions carry the type too.
	if out := get("/api/v1/history/versions?source_id=" + src + "&path=many/big.bin"); out["versions"].([]any)[0].(map[string]any)["type"] != "ext:bin" {
		t.Errorf("version type = %v", out["versions"])
	}
}

func TestAPageOfSelectedItemsCanBeRestored(t *testing.T) {
	h := newHarness(t)
	listFolder(h)
	h.env.Backup()
	src := h.env.Src.ID
	alice := h.visitor()
	alice.loginAs(userID)
	_, out := alice.api("GET", "/api/v1/recovery-points?source_id="+itoa(src), nil)
	point := num(out["recovery_points"].([]any)[0].(map[string]any)["id"])

	paths := func(n int) []string {
		var p []string
		for i := 0; i < n; i++ {
			p = append(p, fmt.Sprintf("many/doc%03d.txt", i%230))
		}
		return p
	}
	// The pages offer up to 500 items, so the preview takes 500; more than that is refused.
	req := func(n int) map[string]any {
		return map[string]any{"source_id": src, "point_id": point, "paths": paths(n), "dest_name": "page", "policy": "skip"}
	}
	if status, out := alice.api("POST", "/api/v1/restores/preview", req(120)); status != http.StatusOK || num(out["totals"].(map[string]any)["files"]) != 120 {
		t.Errorf("a preview of 120 items = %d %v", status, out)
	}
	if status, out := alice.api("POST", "/api/v1/restores/preview", req(501)); status != http.StatusBadRequest {
		t.Errorf("a preview of 501 items = %d %v, want 400", status, out)
	}
}
