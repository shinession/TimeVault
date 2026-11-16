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
	// DisplayPath is the network path users know the folder by, such as \\server\share\folder. Empty means
	// none, and on an update it removes the current one.
	DisplayPath string
	// MaxVersions is the most versions of one file to keep; nil or 0 means unlimited.
	MaxVersions *int
	// ConfirmPrune says that deleting the versions a lower limit makes unnecessary is meant. Without it such an
	// update is refused (see service.PruneNotConfirmedError).
	ConfirmPrune bool
}

// Bounds of SourceRequest.ScheduleMinutes; the sources table has the same constraint.
const (
	MinScheduleMinutes = 1
	MaxScheduleMinutes = 43200
)

// MaxVersionsLimit is the largest version limit a source can have; the sources table has the same constraint.
const MaxVersionsLimit = 10000

// ErrInvalidSource wraps every validation failure of a SourceRequest.
var ErrInvalidSource = errors.New("invalid source")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSource, fmt.Sprintf(format, args...))
}

// SourceError is a validation failure that the web UI shows in the language of the visitor: Key names the
// message and Args fill it in, Msg is the same sentence in English for the API and the log. It wraps
// ErrInvalidSource.
type SourceError struct {
	Key  string
	Args []any
	Msg  string
}

func (e *SourceError) Error() string { return "invalid source: " + e.Msg }

func (e *SourceError) Unwrap() error { return ErrInvalidSource }

// ParseDisplayPath validates the display path of a source, the network path users know the folder by, such as
// \\server\share\folder. A text that is not one gives a *SourceError with the key errors.display_invalid and
// the text as its argument.
func ParseDisplayPath(text string) (pathutil.UNC, error) {
	u, err := pathutil.ParseUNC(text)
	if err != nil {
		reason := strings.TrimPrefix(err.Error(), pathutil.ErrInvalidUNC.Error()+": ")
		return pathutil.UNC{}, &SourceError{
			Key:  "errors.display_invalid",
			Args: []any{text},
			Msg:  `the display path must look like \\server\share\folder: ` + reason,
		}
	}
	return u, nil
}

// PrepareSource validates a request and returns the record to store. The
// folder must be a local directory that does not overlap the repository, the
// restore root or another source, so a backup can never read its own output or
// record the same files twice. The display path is optional here and only
// checked for syntax; whether it is required, and on which server, is a policy
// of the service.
func (e *Engine) PrepareSource(ctx context.Context, req SourceRequest, actor string) (catalog.NewSource, error) {
	return e.prepare(ctx, req, actor, nil)
}

// PrepareSourceUpdate validates the new settings of an existing source. A folder
// that is the current one, or left empty, is kept without looking at the disk, so
// a source whose drive is offline can still be renamed or rescheduled. A different
// folder gets the checks of PrepareSource, except that the source being edited
// does not count as an overlap. The display path is always replaced, so an empty
// one removes the current display path.
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
	display := ""
	if strings.TrimSpace(req.DisplayPath) != "" {
		u, err := ParseDisplayPath(req.DisplayPath)
		if err != nil {
			return catalog.NewSource{}, err
		}
		display = u.String()
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
	var maxVersions *int
	if m := req.MaxVersions; m != nil {
		if *m < 0 || *m > MaxVersionsLimit {
			return catalog.NewSource{}, invalid("the version limit must be between 1 and %d, or 0 for no limit", MaxVersionsLimit)
		}
		if *m > 0 {
			maxVersions = m
		}
	}
	return catalog.NewSource{Name: name, RootPath: root, Excludes: excludes, CreatedBy: actor, ScheduleMinutes: req.ScheduleMinutes, DisplayPath: display, MaxVersions: maxVersions}, nil
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
