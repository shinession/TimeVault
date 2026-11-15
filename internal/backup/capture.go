package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"timevault/internal/platform"
	"timevault/internal/repository"
)

const (
	maxCaptureAttempts = 3
	maxReasonLength    = 200
)

// captured is the outcome of copying one file into the repository.
type captured struct {
	hash    repository.Hash
	size    int64
	mtimeNS int64
	attrs   uint32
	created bool
	// skip is non-empty when the file could not be captured; it is the reason.
	skip string
	// err is a fatal error (cancellation, failing repository) that must stop the backup.
	err error
}

// capture streams one file into the repository. A file that changes while it
// is being read is read again, because a copy of a half-written file would be
// a silently corrupt backup; after a few attempts it is reported as skipped.
func (sc *scan) capture(ctx context.Context, rel string) captured {
	var lastChange string
	for attempt := 0; attempt < maxCaptureAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return captured{err: err}
		}
		if sc.e.hook != nil {
			sc.e.hook("opening", rel)
		}
		f, err := sc.root.Open(filepath.FromSlash(rel))
		if err != nil {
			return captured{skip: skipReason("cannot open", err)}
		}
		before, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return captured{skip: skipReason("cannot stat", err)}
		}
		if !before.Mode().IsRegular() {
			_ = f.Close()
			return captured{skip: "not a regular file"}
		}
		if sc.e.hook != nil {
			sc.e.hook("opened", rel)
		}
		info, created, err := sc.e.repo.Put(ctx, f)
		after, statErr := f.Stat()
		_ = f.Close()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return captured{err: ctxErr}
			}
			var srcErr *repository.SourceError
			if errors.As(err, &srcErr) {
				return captured{skip: skipReason("cannot read", srcErr.Err)}
			}
			return captured{err: err}
		}
		if statErr != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || info.Size != before.Size() {
			lastChange = "changed while it was being backed up"
			continue
		}
		// The name must still lead to a plain file. A symbolic link swapped in since the
		// folder was listed would otherwise record another file's content under this path.
		if lst, lerr := sc.root.Lstat(filepath.FromSlash(rel)); lerr != nil || !lst.Mode().IsRegular() {
			lastChange = "changed while it was being backed up"
			continue
		}
		return captured{
			hash:    info.Hash,
			size:    info.Size,
			mtimeNS: before.ModTime().UnixNano(),
			attrs:   platform.FileAttributes(before),
			created: created,
		}
	}
	return captured{skip: lastChange}
}

func skipReason(prefix string, err error) string {
	var reason string
	switch {
	case platform.IsLockViolation(err):
		reason = "locked by another process"
	case errors.Is(err, fs.ErrPermission):
		reason = "access denied"
	case errors.Is(err, fs.ErrNotExist):
		reason = "disappeared during the backup"
	default:
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		reason = fmt.Sprintf("%s: %v", prefix, err)
	}
	if len(reason) > maxReasonLength {
		reason = reason[:maxReasonLength]
	}
	return reason
}

// osRel converts a slash separated relative path for use with os.Root; "" is the root.
func osRel(rel string) string {
	if rel == "" {
		return "."
	}
	return filepath.FromSlash(rel)
}
