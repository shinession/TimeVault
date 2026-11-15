package backup

import (
	"fmt"
	"path"
	"strings"
)

// DefaultExcludes are proposed for new sources: Windows housekeeping folders and
// Office lock files that are never worth restoring.
var DefaultExcludes = []string{"$RECYCLE.BIN", "System Volume Information", "Thumbs.db", "~$*"}

const (
	maxExcludes       = 100
	maxExcludeLength  = 200
	pathPatternMarker = "/"
)

type pattern struct {
	glob      string
	matchPath bool
}

// excluder matches case-insensitive globs against a file name, or against the
// source-relative path when the pattern contains a slash.
type excluder struct {
	patterns []pattern
}

// ValidateExcludes normalizes and checks a list of exclude patterns.
func ValidateExcludes(in []string) ([]string, error) {
	var out []string
	for _, raw := range in {
		p := strings.TrimSpace(strings.ReplaceAll(raw, `\`, "/"))
		if p == "" {
			continue
		}
		if len(p) > maxExcludeLength {
			return nil, fmt.Errorf("exclude pattern is longer than %d characters", maxExcludeLength)
		}
		if _, err := path.Match(strings.ToLower(p), ""); err != nil {
			return nil, fmt.Errorf("exclude pattern %q is invalid: %w", raw, err)
		}
		out = append(out, p)
	}
	if len(out) > maxExcludes {
		return nil, fmt.Errorf("at most %d exclude patterns are allowed", maxExcludes)
	}
	return out, nil
}

func newExcluder(patterns []string) *excluder {
	x := &excluder{}
	for _, raw := range patterns {
		p := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(raw, `\`, "/")))
		if p == "" {
			continue
		}
		x.patterns = append(x.patterns, pattern{glob: strings.Trim(p, "/"), matchPath: strings.Contains(strings.Trim(p, "/"), pathPatternMarker)})
	}
	return x
}

func (x *excluder) match(rel, name string) bool {
	if x == nil {
		return false
	}
	lowerName := strings.ToLower(name)
	lowerPath := lowerName
	if rel != "" {
		lowerPath = strings.ToLower(rel) + "/" + lowerName
	}
	for _, p := range x.patterns {
		candidate := lowerName
		if p.matchPath {
			candidate = lowerPath
		}
		if ok, _ := path.Match(p.glob, candidate); ok {
			return true
		}
	}
	return false
}
