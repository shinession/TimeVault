package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"timevault/internal/pathutil"
	"timevault/internal/search"
)

const (
	// folderInfoTTL is how long what the search engine said about the folders of one folder is reused, so
	// sorting and paging a listing asks the engine once.
	folderInfoTTL = 10 * time.Second
	// folderInfoTimeout bounds one question to the engine; a listing never waits longer for folder sizes.
	folderInfoTimeout = 2 * time.Second
	// folderInfoBackoff is how long the engine is left alone after it failed to answer.
	folderInfoBackoff = 15 * time.Second
	// folderInfoEntries is how many folders are remembered; the memory is dropped when it is exceeded.
	folderInfoEntries = 256
)

// folderInfos remembers the size and modification time the search engine holds for the sub-folders of a folder.
type folderInfos struct {
	mu        sync.Mutex
	byDir     map[string]cachedFolders
	downUntil atomic.Int64 // unix nanoseconds
}

type cachedFolders struct {
	at    time.Time
	byKey map[string]search.FolderInfo
}

// lookup returns the engine's folder information about the folders directly inside dir, keyed by
// pathutil.NameKey of the folder name. It returns nil when the engine cannot give any: it is not configured, it
// does not answer, or it was asked too recently and failed. A listing then shows no folder sizes.
func (f *folderInfos) lookup(ctx context.Context, provider search.Provider, dir string, now time.Time) map[string]search.FolderInfo {
	lister, ok := provider.(search.FolderLister)
	if !ok || now.UnixNano() < f.downUntil.Load() {
		return nil
	}
	key := strings.ToLower(dir)
	f.mu.Lock()
	if c, ok := f.byDir[key]; ok && now.Sub(c.at) < folderInfoTTL {
		f.mu.Unlock()
		return c.byKey
	}
	f.mu.Unlock()

	askCtx, cancel := context.WithTimeout(ctx, folderInfoTimeout)
	defer cancel()
	folders, _, err := lister.Subfolders(askCtx, dir, 0)
	if err != nil {
		// A caller that gave up, a folder that is no good, a folder that the index does not hold and an engine that is
		// switched off say nothing about an outage of the engine.
		if ctx.Err() == nil && !errors.Is(err, search.ErrBadQuery) && !errors.Is(err, search.ErrDisabled) && !errors.Is(err, search.ErrNotIndexed) {
			f.downUntil.Store(now.Add(folderInfoBackoff).UnixNano())
		}
		return nil
	}
	byKey := make(map[string]search.FolderInfo, len(folders))
	for _, info := range folders {
		byKey[pathutil.NameKey(info.Name)] = info
	}
	f.mu.Lock()
	if f.byDir == nil || len(f.byDir) >= folderInfoEntries {
		f.byDir = make(map[string]cachedFolders)
	}
	f.byDir[key] = cachedFolders{at: now, byKey: byKey}
	f.mu.Unlock()
	return byKey
}
