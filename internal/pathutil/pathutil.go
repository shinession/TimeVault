// Package pathutil holds the path rules shared by the backup, catalog and
// restore code: Windows-compatible name validation, source-relative path
// normalization and case-insensitive containment checks.
package pathutil

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"timevault/internal/platform"
)

// MaxNameUnits is the longest file name component NTFS accepts, in UTF-16 units.
const MaxNameUnits = 255

// MaxRelDepth bounds the number of components in a user supplied relative path.
const MaxRelDepth = 256

// ErrUnsafePath is wrapped by every validation error so callers can test with errors.Is.
var ErrUnsafePath = errors.New("unsafe path")

var reservedDeviceNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {}, "CONIN$": {}, "CONOUT$": {},
	"COM0": {}, "COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {}, "COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT0": {}, "LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {}, "LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
	// Windows also reserves the superscript forms.
	"COM\u00b9": {}, "COM\u00b2": {}, "COM\u00b3": {}, "LPT\u00b9": {}, "LPT\u00b2": {}, "LPT\u00b3": {},
}

// NameKey is the case-insensitive sort and lookup key of a file name.
func NameKey(name string) string {
	return strings.ToLower(name)
}

// ValidateName reports whether name is safe to use as one path component on
// every platform TimeVault reads from or restores to.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: empty name", ErrUnsafePath)
	case name == "." || name == "..":
		return fmt.Errorf("%w: %q is not a file name", ErrUnsafePath, name)
	case !utf8.ValidString(name):
		return fmt.Errorf("%w: name is not valid UTF-8", ErrUnsafePath)
	}
	units := 0
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: control character in name", ErrUnsafePath)
		}
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return fmt.Errorf("%w: character %q is not allowed in a name", ErrUnsafePath, r)
		}
		units += utf16.RuneLen(r)
	}
	if units > MaxNameUnits {
		return fmt.Errorf("%w: name is longer than %d characters", ErrUnsafePath, MaxNameUnits)
	}
	if last := name[len(name)-1]; last == '.' || last == ' ' {
		return fmt.Errorf("%w: name ends with a dot or space", ErrUnsafePath)
	}
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if _, reserved := reservedDeviceNames[strings.ToUpper(strings.TrimRight(base, " "))]; reserved {
		return fmt.Errorf("%w: %q is a reserved device name", ErrUnsafePath, name)
	}
	return nil
}

// CleanRel normalizes a source-relative path supplied by a client. The result
// uses forward slashes, has no leading or trailing slash and is "" for the
// source root. Any ".." component, drive letter or invalid name is rejected.
func CleanRel(p string) (string, error) {
	parts, err := SplitRel(p)
	if err != nil {
		return "", err
	}
	return strings.Join(parts, "/"), nil
}

// SplitRel is CleanRel returning the individual components.
func SplitRel(p string) ([]string, error) {
	if strings.ContainsRune(p, 0) {
		return nil, fmt.Errorf("%w: NUL in path", ErrUnsafePath)
	}
	p = strings.ReplaceAll(p, `\`, "/")
	var parts []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		if err := ValidateName(c); err != nil {
			return nil, err
		}
		parts = append(parts, c)
	}
	if len(parts) > MaxRelDepth {
		return nil, fmt.Errorf("%w: path is deeper than %d levels", ErrUnsafePath, MaxRelDepth)
	}
	return parts, nil
}

// JoinRel appends name to a slash separated relative path.
func JoinRel(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// ParentRel returns the parent of a slash separated relative path.
func ParentRel(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// BaseRel returns the last component of a slash separated relative path.
func BaseRel(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

func foldPath(p string) string {
	p = filepath.Clean(p)
	p = strings.TrimPrefix(p, `\\?\`)
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// Within reports whether p equals root or lies inside it. Both must be
// absolute; the comparison is case-insensitive on Windows and respects
// component boundaries (root "D:\a" does not contain "D:\ab").
func Within(root, p string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(p) {
		return false
	}
	r, c := foldPath(root), foldPath(p)
	if r == c {
		return true
	}
	if !strings.HasSuffix(r, string(filepath.Separator)) {
		r += string(filepath.Separator)
	}
	return strings.HasPrefix(c, r)
}

// Overlaps reports whether a and b are the same directory or one contains the other.
func Overlaps(a, b string) bool {
	return Within(a, b) || Within(b, a)
}

// Real returns the absolute path of p with symbolic links and junctions
// resolved, for containment checks that must not be fooled by redirections.
// A path that does not exist yet is resolved through its nearest existing
// ancestor, so the result is where the path would really be created.
func Real(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	var missing []string
	for cur := abs; ; {
		resolved, err := platform.ResolvePath(cur)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if !errors.Is(err, fs.ErrNotExist) || parent == cur {
			return "", err
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

// RealOrClean is Real that falls back to the cleaned absolute path when the
// location cannot be resolved, for comparisons that must still run.
func RealOrClean(p string) string {
	if r, err := Real(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
