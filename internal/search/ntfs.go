package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"timevault/internal/ntfsindex"
)

// Index is what the NTFS provider needs of the NTFS index; *ntfsindex.Manager is one.
type Index interface {
	Search(path string, terms []string, offset, limit int) (ntfsindex.Page, error)
	Subfolders(path string, limit int) ([]ntfsindex.Folder, bool, error)
	Probe(path string, since time.Time) (ntfsindex.ProbeResult, error)
	Health() ntfsindex.Health
}

// NTFS answers from the NTFS index of the monitored folders: the files and folders that exist below a folder right now,
// their sizes, and whether anything below a folder changed.
type NTFS struct{ idx Index }

var (
	_ Provider     = (*NTFS)(nil)
	_ FolderLister = (*NTFS)(nil)
	_ ChangeProber = (*NTFS)(nil)
)

// NewNTFS returns the provider that answers from idx.
func NewNTFS(idx Index) *NTFS { return &NTFS{idx: idx} }

// Name implements Provider.
func (*NTFS) Name() string { return "ntfs" }

// indexError turns what the index says into what a Provider says.
func indexError(err error) error {
	switch {
	case errors.Is(err, ntfsindex.ErrNotIndexed):
		return fmt.Errorf("%w: %v", ErrNotIndexed, err)
	case errors.Is(err, ntfsindex.ErrNotReady):
		return unavailable("the index is not ready")
	}
	return unavailable(err.Error())
}

func canceled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("search: %w", err)
	}
	return nil
}

// Search implements Provider. The terms are matched as plain text against the names, the search stays below q.Root, and
// every result is checked against q.Root again.
func (n *NTFS) Search(ctx context.Context, q Query) (Page, error) {
	req, err := newRequest(q)
	if err != nil {
		return Page{}, err
	}
	if err := canceled(ctx); err != nil {
		return Page{}, err
	}
	res, err := n.idx.Search(req.root.scope(), req.terms, req.offset, req.limit)
	if err != nil {
		return Page{}, indexError(err)
	}
	page := Page{Items: make([]Result, 0, len(res.Items)), Total: res.Total, Offset: req.offset, NextOffset: req.next(len(res.Items), res.Total)}
	for _, e := range res.Items {
		if !req.root.contains(e.Path) {
			page.Dropped++
			continue
		}
		page.Items = append(page.Items, Result{Name: e.Name, Path: e.Path, IsDir: e.IsDir, Size: e.Size, ModTime: ntfsindex.Time(e.MTime)})
	}
	return page, nil
}

// Subfolders implements FolderLister: the folders directly inside dir, with the size of everything below each.
func (n *NTFS) Subfolders(ctx context.Context, dir string, limit int) ([]FolderInfo, bool, error) {
	root, err := parseRoot(dir)
	if err != nil {
		return nil, false, err
	}
	if err := canceled(ctx); err != nil {
		return nil, false, err
	}
	folders, more, err := n.idx.Subfolders(root.scope(), limit)
	if err != nil {
		return nil, false, indexError(err)
	}
	out := make([]FolderInfo, len(folders))
	for i, f := range folders {
		out[i] = FolderInfo{Name: f.Name, Size: f.Size, ModTime: ntfsindex.Time(f.MTime)}
	}
	return out, more, nil
}

// Probe implements ChangeProber. The index reads the change journal before it answers, so a change that happened a moment
// ago counts; a time from before the index began to watch the folder counts as a change, because nothing is known of it.
func (n *NTFS) Probe(ctx context.Context, root string, since time.Time) (ProbeResult, error) {
	r, err := parseRoot(root)
	if err != nil {
		return ProbeResult{}, err
	}
	if err := canceled(ctx); err != nil {
		return ProbeResult{}, err
	}
	res, err := n.idx.Probe(r.scope(), since)
	if err != nil {
		return ProbeResult{}, indexError(err)
	}
	return ProbeResult{Items: res.Items, Changed: res.Changed}, nil
}

// Health implements Provider. A volume that is being built, or that cannot be indexed, makes it degraded.
func (n *NTFS) Health(context.Context) Health {
	h := n.idx.Health()
	out := Health{State: StateOK, Detail: h.Detail, Key: h.Key, Args: h.Args, CheckedAt: time.Now()}
	if h.State == ntfsindex.StateDegraded {
		out.State = StateDegraded
	}
	return out
}
