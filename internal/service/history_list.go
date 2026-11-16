package service

import (
	"context"
	"errors"

	"timevault/internal/catalog"
	"timevault/internal/listing"
	"timevault/internal/pathutil"
)

// HistoryList is one page of a folder as it existed at a recovery point, cut by
// the listing engine.
type HistoryList struct {
	Point  catalog.Point
	Latest catalog.Point
	Rel    string
	// Entries is the requested window; each carries its status relative to the newest point.
	Entries []catalog.Entry
	// Total is the number of entries that match the type filter and Unfiltered
	// the number the folder had.
	Total      int
	Unfiltered int
	// Types counts the entries of the whole folder by type, whatever the filter.
	Types []listing.TypeCount
}

// ListHistory lists a folder as it existed at a recovery point, filtered,
// sorted and cut into a page as o says. Unlike BrowseHistory it reads the whole
// folder, because a sort by size or type cannot be paged by name.
func (s *Service) ListHistory(ctx context.Context, src catalog.Source, point catalog.Point, rel string, o listing.Options) (HistoryList, error) {
	rel, err := pathutil.CleanRel(rel)
	if err != nil {
		return HistoryList{}, err
	}
	latest, err := s.cat.LatestCommitted(ctx, src.ID)
	if err != nil {
		return HistoryList{}, ErrNotFound
	}
	dir, err := s.cat.ResolveAt(ctx, src.ID, point.Seq, rel)
	if errors.Is(err, catalog.ErrNotFound) {
		return HistoryList{}, ErrNotFound
	}
	if err != nil {
		return HistoryList{}, err
	}
	if !dir.IsDir {
		return HistoryList{}, ErrNotFound
	}
	all, err := s.cat.ListAllChildren(ctx, catalog.ListParams{Parent: dir.NodeID, Seq: point.Seq, LatestSeq: latest.Seq})
	if err != nil {
		return HistoryList{}, err
	}
	page := listing.Apply(all, historyView, o)
	return HistoryList{
		Point: point, Latest: latest, Rel: rel, Entries: page.Items,
		Total: page.Total, Unfiltered: page.Unfiltered, Types: page.Types,
	}, nil
}

func historyView(e catalog.Entry) listing.View {
	return listing.View{Name: e.Name, IsDir: e.IsDir, Size: e.Size, MTimeNS: e.MTimeNS}
}
