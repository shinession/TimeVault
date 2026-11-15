package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"timevault/internal/catalog"
	"timevault/internal/pathutil"
)

// SourceRequest is what an administrator submits to register or change a source.
type SourceRequest struct {
	Name     string
	RootPath string
	Excludes []string
	// ScheduleMinutes is the interval between automatic backups; nil means manual only.
	ScheduleMinutes *int
}

// Bounds of SourceRequest.ScheduleMinutes; the sources table has the same constraint.
const (
	MinScheduleMinutes = 1
	MaxScheduleMinutes = 43200
)

// ErrInvalidSource wraps every validation failure of a SourceRequest.
var ErrInvalidSource = errors.New("invalid source")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSource, fmt.Sprintf(format, args...))
}

// PrepareSource validates a request and returns the record to store. The
// folder must be a local directory that does not overlap the repository, the
// restore root or another source, so a backup can never read its own output or
// record the same files twice.
func (e *Engine) PrepareSource(ctx context.Context, req SourceRequest, actor string) (catalog.NewSource, error) {
	return e.prepare(ctx, req, actor, nil)
}

// PrepareSourceUpdate validates the new settings of an existing source. A folder
// that is the current one, or left empty, is kept without looking at the disk, so
// a source whose drive is offline can still be renamed or rescheduled. A different
// folder gets the checks of PrepareSource, except that the source being edited
// does not count as an overlap.
func (e *Engine) PrepareSourceUpdate(ctx context.Context, cur catalog.Source, req SourceRequest) (catalog.NewSource, error) {
	return e.prepare(ctx, req, cur.CreatedBy, &cur)
}

func (e *Engine) prepare(ctx context.Context, req SourceRequest, actor string, cur *catalog.Source) (catalog.NewSource, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 100 {
		return catalog.NewSource{}, invalid("the name must be 1 to 100 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return catalog.NewSource{}, invalid("the name contains a control character")
		}
	}
	root, err := e.checkRoot(ctx, req.RootPath, cur)
	if err != nil {
		return catalog.NewSource{}, err
	}
	excludes, err := ValidateExcludes(req.Excludes)
	if err != nil {
		return catalog.NewSource{}, invalid("%v", err)
	}
	if m := req.ScheduleMinutes; m != nil && (*m < MinScheduleMinutes || *m > MaxScheduleMinutes) {
		return catalog.NewSource{}, invalid("the schedule must be between %d and %d minutes", MinScheduleMinutes, MaxScheduleMinutes)
	}
	return catalog.NewSource{Name: name, RootPath: root, Excludes: excludes, CreatedBy: actor, ScheduleMinutes: req.ScheduleMinutes}, nil
}

// checkRoot validates the folder of a source and returns it cleaned. cur is the
// source being edited, nil when a new one is registered.
func (e *Engine) checkRoot(ctx context.Context, requested string, cur *catalog.Source) (string, error) {
	root := strings.TrimSpace(requested)
	if cur != nil && (root == "" || catalog.RootKey(root) == catalog.RootKey(cur.RootPath)) {
		return cur.RootPath, nil
	}
	if root == "" || !filepath.IsAbs(root) {
		return "", invalid("the folder must be an absolute path such as D:\\Data")
	}
	if strings.HasPrefix(root, `\\`) && !strings.HasPrefix(root, `\\?\`) {
		return "", invalid("network shares are not supported yet; use a local NTFS folder")
	}
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", invalid("the folder does not exist")
	case err != nil:
		return "", invalid("the folder cannot be read: %v", err)
	case !st.IsDir():
		return "", invalid("the path is not a folder")
	}
	real, err := pathutil.Real(root)
	if err != nil {
		return "", invalid("the folder cannot be resolved: %v", err)
	}
	for _, p := range e.protected {
		if p == "" {
			continue
		}
		if pathutil.Overlaps(pathutil.RealOrClean(p), real) {
			return "", invalid("the folder overlaps the repository or restore folder %s", p)
		}
	}
	existing, err := e.cat.ListSources(ctx)
	if err != nil {
		return "", err
	}
	for _, other := range existing {
		if cur != nil && other.ID == cur.ID {
			continue
		}
		if pathutil.Overlaps(pathutil.RealOrClean(other.RootPath), real) {
			return "", invalid("the folder overlaps the source %q", other.Name)
		}
	}
	return root, nil
}
