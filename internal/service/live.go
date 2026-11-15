package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"timevault/internal/catalog"
	"timevault/internal/pathutil"
)

// LiveStatus compares an item on disk with the newest recovery point.
type LiveStatus string

const (
	LiveProtected   LiveStatus = "protected"
	LiveChanged     LiveStatus = "changed"
	LiveNew         LiveStatus = "new"
	LiveNotCaptured LiveStatus = "not_captured"
)

const maxDeletedShown = 500

// LiveEntry is one item currently on disk.
type LiveEntry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
	Status  LiveStatus
	// Special marks reparse points and other items the backup does not follow.
	Special bool
}

// LiveListing is one page of a folder as it exists right now.
type LiveListing struct {
	Rel        string
	Entries    []LiveEntry
	Total      int
	NextOffset int
	// Deleted are items present in the newest recovery point that no longer exist here.
	Deleted     []catalog.Entry
	DeletedMore bool
	Latest      *catalog.Point
	Hidden      int
}

// ErrSourceUnavailable means the source folder cannot be read right now.
var ErrSourceUnavailable = errors.New("the source folder is not accessible")

// BrowseLive lists a folder of the source as it exists on disk and marks each
// item as protected, changed or new relative to the newest recovery point.
// Folders are read through os.Root, so a crafted path can never leave the
// source. Items deleted since the last backup are listed separately, because
// that is where a lost file is found again.
func (s *Service) BrowseLive(ctx context.Context, src catalog.Source, rel string, offset, limit int) (LiveListing, error) {
	rel, err := pathutil.CleanRel(rel)
	if err != nil {
		return LiveListing{}, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	root, err := os.OpenRoot(src.RootPath)
	if err != nil {
		return LiveListing{}, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	defer root.Close()

	osRel := "."
	if rel != "" {
		osRel = filepath.FromSlash(rel)
	}
	dir, err := root.Open(osRel)
	if errors.Is(err, fs.ErrNotExist) {
		return LiveListing{}, ErrNotFound
	}
	if err != nil {
		return LiveListing{}, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	defer dir.Close()
	if st, err := dir.Stat(); err != nil || !st.IsDir() {
		return LiveListing{}, ErrNotFound
	}
	dirEntries, err := dir.ReadDir(-1)
	if err != nil {
		return LiveListing{}, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}

	out := LiveListing{Rel: rel, NextOffset: -1}
	live := make([]LiveEntry, 0, len(dirEntries))
	present := make(map[string]struct{}, len(dirEntries))
	for _, de := range dirEntries {
		name := de.Name()
		if pathutil.ValidateName(name) != nil {
			out.Hidden++
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		present[name] = struct{}{}
		mode := info.Mode()
		e := LiveEntry{Name: name, IsDir: mode.IsDir(), ModTime: info.ModTime()}
		switch {
		case mode&(os.ModeSymlink|os.ModeIrregular) != 0:
			e.Special, e.IsDir = true, false
		case mode.IsRegular():
			e.Size = info.Size()
		}
		live = append(live, e)
	}
	sort.Slice(live, func(i, j int) bool {
		ki, kj := pathutil.NameKey(live[i].Name), pathutil.NameKey(live[j].Name)
		if ki != kj {
			return ki < kj
		}
		return live[i].Name < live[j].Name
	})

	var previous map[string]catalog.Entry
	if latest, err := s.cat.LatestCommitted(ctx, src.ID); err == nil {
		out.Latest = &latest
		if dirEntry, err := s.cat.ResolveAt(ctx, src.ID, latest.Seq, rel); err == nil && dirEntry.IsDir {
			if previous, err = s.cat.ChildrenAt(ctx, dirEntry.NodeID, latest.Seq); err != nil {
				return LiveListing{}, err
			}
		}
	}
	for i := range live {
		live[i].Status = liveStatus(live[i], previous)
	}
	for name, prev := range previous {
		if _, ok := present[name]; !ok {
			out.Deleted = append(out.Deleted, prev)
		}
	}
	sort.Slice(out.Deleted, func(i, j int) bool {
		ki, kj := pathutil.NameKey(out.Deleted[i].Name), pathutil.NameKey(out.Deleted[j].Name)
		if ki != kj {
			return ki < kj
		}
		return out.Deleted[i].Name < out.Deleted[j].Name
	})
	if len(out.Deleted) > maxDeletedShown {
		out.Deleted, out.DeletedMore = out.Deleted[:maxDeletedShown], true
	}

	out.Total = len(live)
	if offset >= len(live) {
		return out, nil
	}
	end := min(offset+limit, len(live))
	out.Entries = live[offset:end]
	if end < len(live) {
		out.NextOffset = end
	}
	return out, nil
}

func liveStatus(e LiveEntry, previous map[string]catalog.Entry) LiveStatus {
	prev, ok := previous[e.Name]
	switch {
	case !ok:
		return LiveNew
	case e.Special:
		if prev.State == catalog.VersionSkipped {
			return LiveNotCaptured
		}
		return LiveChanged
	case e.IsDir:
		if prev.IsDir {
			return LiveProtected
		}
		return LiveChanged
	case prev.IsDir || prev.State != catalog.VersionOK:
		if prev.State == catalog.VersionSkipped {
			return LiveNotCaptured
		}
		return LiveChanged
	case prev.Size == e.Size && prev.MTimeNS == e.ModTime.UnixNano():
		return LiveProtected
	default:
		return LiveChanged
	}
}
