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

// SourceRequest is what an administrator submits to register a source.
type SourceRequest struct {
	Name     string
	RootPath string
	Excludes []string
}

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
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 100 {
		return catalog.NewSource{}, invalid("the name must be 1 to 100 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return catalog.NewSource{}, invalid("the name contains a control character")
		}
	}
	root := strings.TrimSpace(req.RootPath)
	if root == "" || !filepath.IsAbs(root) {
		return catalog.NewSource{}, invalid("the folder must be an absolute path such as D:\\Data")
	}
	if strings.HasPrefix(root, `\\`) && !strings.HasPrefix(root, `\\?\`) {
		return catalog.NewSource{}, invalid("network shares are not supported yet; use a local NTFS folder")
	}
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return catalog.NewSource{}, invalid("the folder does not exist")
	case err != nil:
		return catalog.NewSource{}, invalid("the folder cannot be read: %v", err)
	case !st.IsDir():
		return catalog.NewSource{}, invalid("the path is not a folder")
	}
	real, err := pathutil.Real(root)
	if err != nil {
		return catalog.NewSource{}, invalid("the folder cannot be resolved: %v", err)
	}
	for _, p := range e.protected {
		if p == "" {
			continue
		}
		if pathutil.Overlaps(pathutil.RealOrClean(p), real) {
			return catalog.NewSource{}, invalid("the folder overlaps the repository or restore folder %s", p)
		}
	}
	existing, err := e.cat.ListSources(ctx)
	if err != nil {
		return catalog.NewSource{}, err
	}
	for _, other := range existing {
		if pathutil.Overlaps(pathutil.RealOrClean(other.RootPath), real) {
			return catalog.NewSource{}, invalid("the folder overlaps the source %q", other.Name)
		}
	}
	excludes, err := ValidateExcludes(req.Excludes)
	if err != nil {
		return catalog.NewSource{}, invalid("%v", err)
	}
	return catalog.NewSource{Name: name, RootPath: root, Excludes: excludes, CreatedBy: actor}, nil
}
