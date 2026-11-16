package search

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// maxSubfolders bounds how many folders of one folder Subfolders collects.
	maxSubfolders = 20_000
	// subfolderPage is how many folders one request asks for.
	subfolderPage = 2_500
	// probeTimeLayout is how Everything reads a date without a zone: the local time of the machine it runs on.
	probeTimeLayout = "2006-01-02T15:04:05"
)

var (
	_ FolderLister = (*Everything)(nil)
	_ ChangeProber = (*Everything)(nil)
)

// Subfolders implements FolderLister: one request per page of the folders directly inside dir, with the
// size and modification time Everything holds for each. Sizes are only there when Everything indexes folder
// sizes; otherwise every Size is -1.
func (e *Everything) Subfolders(ctx context.Context, dir string, limit int) ([]FolderInfo, bool, error) {
	root, err := parseRoot(dir)
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 || limit > maxSubfolders {
		limit = maxSubfolders
	}
	search := `infolder:"` + root.scope() + `" folder:`
	folders := make([]FolderInfo, 0, min(limit, subfolderPage))
	for offset := 0; ; {
		body, err := e.get(ctx, url.Values{
			"search":               {search},
			"json":                 {"1"},
			"size_column":          {"1"},
			"date_modified_column": {"1"},
			"offset":               {strconv.Itoa(offset)},
			"count":                {strconv.Itoa(min(subfolderPage, limit-len(folders)))},
			"sort":                 {"name"},
			"ascending":            {"1"},
		})
		if err != nil {
			return nil, false, err
		}
		total, items, err := decodeResponse(body)
		if err != nil {
			return nil, false, err
		}
		for _, item := range items {
			// Without a path column the name is the bare name; anything else is not a folder directly inside dir.
			if !isFolder(item.Type) || item.Name == "" || strings.ContainsAny(item.Name, `\/`) {
				continue
			}
			folders = append(folders, FolderInfo{Name: item.Name, Size: parseSize(item.Size), ModTime: parseTime(item.DateModified)})
		}
		offset += len(items)
		switch {
		case len(items) == 0 || offset >= total:
			return folders, false, nil
		case len(folders) >= limit:
			return folders, true, nil
		}
	}
}

// Probe implements ChangeProber with two count-only requests: how many entries Everything holds below the
// root, and whether any of them was created, modified, renamed, moved or had its attributes changed since the
// given time. Everything's "recently changed" date is what sees renames, moves and attribute changes, which the
// created and modified dates do not show; a deletion leaves nothing to date, so it only shows in Items. A zero
// since asks for the count alone.
func (e *Everything) Probe(ctx context.Context, root string, since time.Time) (ProbeResult, error) {
	r, err := parseRoot(root)
	if err != nil {
		return ProbeResult{}, err
	}
	scope := r.scope()
	selector := `<path:"` + scope + `" | wfn:"` + strings.TrimSuffix(scope, `\`) + `">`
	items, err := e.count(ctx, selector)
	if err != nil {
		return ProbeResult{}, err
	}
	if items == 0 {
		return ProbeResult{}, ErrNotIndexed
	}
	if since.IsZero() {
		return ProbeResult{Items: int64(items)}, nil
	}
	at := since.Local().Format(probeTimeLayout)
	changed, err := e.count(ctx, selector+` <rc:>=`+at+` | dm:>=`+at+` | dc:>=`+at+`>`)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Items: int64(items), Changed: changed > 0}, nil
}

// count returns how many entries match a search, without fetching any.
func (e *Everything) count(ctx context.Context, search string) (int, error) {
	body, err := e.get(ctx, url.Values{"search": {search}, "json": {"1"}, "count": {"0"}})
	if err != nil {
		return 0, err
	}
	total, _, err := decodeResponse(body)
	return total, err
}
