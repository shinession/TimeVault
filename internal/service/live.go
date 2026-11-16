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
	"timevault/internal/listing"
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
	Name  string
	IsDir bool
	// Size is the size in bytes. A folder has one only when the search engine knows it, see SizeKnown.
	Size int64
	// SizeKnown is false when there is no size to show: for a link, and for a folder the search engine holds
	// no size of (it is not configured, does not answer or does not index folder sizes).
	SizeKnown bool
	ModTime   time.Time
	Status    LiveStatus
	// Special marks reparse points and other items the backup does not follow.
	Special bool
}

// LiveListing is one page of a folder as it exists right now.
type LiveListing struct {
	Rel     string
	Entries []LiveEntry
	// Total is the number of entries that match the type filter and Unfiltered
	// the number the folder has.
	Total      int
	Unfiltered int
	// NextOffset is the offset of the next page, or -1 after the last one.
	NextOffset int
	// Types counts the entries of the whole folder by type, whatever the filter.
	Types []listing.TypeCount
	// Deleted are items present in the newest recovery point that no longer exist here.
	Deleted     []catalog.Entry
	DeletedMore bool
	Latest      *catalog.Point
	Hidden      int
}

// ErrSourceUnavailable means the source folder cannot be read right now.
var ErrSourceUnavailable = errors.New("the source folder is not accessible")

// BrowseLive lists a folder of the source as it exists on disk in name order
// and marks each item as protected, changed or new relative to the newest
// recovery point; see ListLive. A limit outside 1..1000 means 200.
func (s *Service) BrowseLive(ctx context.Context, src catalog.Source, rel string, offset, limit int) (LiveListing, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return s.ListLive(ctx, src, rel, listing.Options{Sort: listing.SortName, Offset: offset, Limit: limit})
}

// ListLive lists a folder of the source as it exists on disk, filtered, sorted
// and cut into a page as o says, and marks each item as protected, changed or
// new relative to the newest recovery point. Folders are read through os.Root,
// so a crafted path can never leave the source. Items deleted since the last
// backup are listed separately, because that is where a lost file is found
// again; they follow the type filter of o but not its order or window.
func (s *Service) ListLive(ctx context.Context, src catalog.Source, rel string, o listing.Options) (LiveListing, error) {
	rel, err := pathutil.CleanRel(rel)
	if err != nil {
		return LiveListing{}, err
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
			e.Size, e.SizeKnown = info.Size(), true
		}
		live = append(live, e)
	}
	s.fillFolderSizes(ctx, live, filepath.Join(src.RootPath, osRel))

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
		if _, ok := present[name]; ok {
			continue
		}
		if listing.Matches(o.Type, listing.TypeKey(name, prev.IsDir, false)) {
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

	page := listing.Apply(live, liveView, o)
	out.Total, out.Unfiltered, out.Types = page.Total, page.Unfiltered, page.Types
	if len(page.Items) > 0 {
		out.Entries = page.Items
	}
	if next := max(o.Offset, 0) + len(page.Items); next < page.Total {
		out.NextOffset = next
	}
	return out, nil
}

func liveView(e LiveEntry) listing.View {
	v := listing.View{Name: e.Name, IsDir: e.IsDir, Special: e.Special, Size: e.Size}
	if !e.ModTime.IsZero() {
		v.MTimeNS = e.ModTime.UnixNano()
	}
	return v
}

// fillFolderSizes gives the folders of a listing the size the search engine holds for them. The disk lists a
// folder without its size, and walking every folder to add it up would make a listing as slow as a backup scan.
// The modification time stays the one of the disk. A folder the engine does not know keeps no size.
func (s *Service) fillFolderSizes(ctx context.Context, entries []LiveEntry, dir string) {
	hasFolder := false
	for i := range entries {
		if entries[i].IsDir {
			hasFolder = true
			break
		}
	}
	if !hasFolder {
		return
	}
	infos := s.folders.lookup(ctx, s.search, dir, time.Now())
	if len(infos) == 0 {
		return
	}
	for i := range entries {
		if !entries[i].IsDir {
			continue
		}
		if info, ok := infos[pathutil.NameKey(entries[i].Name)]; ok && info.Size >= 0 {
			entries[i].Size, entries[i].SizeKnown = info.Size, true
		}
	}
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
