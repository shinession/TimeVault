package pathutil

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxUNCLength is the longest network path ParseUNC accepts, in characters. The
// display_path column of the sources table has the same limit.
const MaxUNCLength = 520

const (
	maxHostLength  = 253
	maxShareLength = 80
)

// ErrInvalidUNC is wrapped by every error of ParseUNC.
var ErrInvalidUNC = errors.New("invalid network path")

// UNC is a network path \\host\share\folder\... split into its parts.
type UNC struct {
	Host  string
	Share string
	// Rest holds the folders below the share, outermost first.
	Rest []string
}

func isSeparator(c byte) bool { return c == '\\' || c == '/' }

func uncError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidUNC, fmt.Sprintf(format, args...))
}

// ValidateHost reports whether name is a plain host name as people type it in
// \\host\share paths: 1 to 253 ASCII letters, digits, '-', '_' and '.', without a
// leading, trailing or doubled dot. The error never contains name.
func ValidateHost(name string) error {
	if name == "" {
		return errors.New("the host name is missing")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return errors.New("the host name may only contain letters, digits, '-', '_' and '.'")
		}
	}
	switch {
	case name[0] == '.' || name[len(name)-1] == '.':
		return errors.New("the host name must not start or end with a dot")
	case strings.Contains(name, ".."):
		return errors.New("the host name must not contain two dots in a row")
	case len(name) > maxHostLength:
		return fmt.Errorf("the host name is longer than %d characters", maxHostLength)
	}
	return nil
}

// ParseUNC parses a network path such as \\server\share\folder. Either slash may
// separate the parts; spaces around the text and one trailing separator are
// dropped. The host must pass ValidateHost, the share must be a valid Windows
// name that does not end with $ (hidden and administrative shares are not for
// users) and every folder below it must pass ValidateName. The result holds
// the text as typed, only the separators are normalised.
//
// Errors wrap ErrInvalidUNC, say what is wrong and never contain the text, so
// they are safe to log.
func ParseUNC(raw string) (UNC, error) {
	s := strings.TrimSpace(raw)
	switch {
	case len(s) < 2 || !isSeparator(s[0]) || !isSeparator(s[1]):
		return UNC{}, uncError(`it must start with \\ followed by the host name`)
	case len(s) > 4*MaxUNCLength:
		return UNC{}, uncError("it is longer than %d characters", MaxUNCLength)
	case !utf8.ValidString(s):
		return UNC{}, uncError("it is not valid UTF-8")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return UNC{}, uncError("it contains a control character")
		}
	}
	body := s[2:]
	if n := len(body); n > 0 && isSeparator(body[n-1]) {
		body = body[:n-1]
	}
	if utf8.RuneCountInString(body)+2 > MaxUNCLength {
		return UNC{}, uncError("it is longer than %d characters", MaxUNCLength)
	}

	parts := strings.Split(strings.ReplaceAll(body, "/", `\`), `\`)
	if err := ValidateHost(parts[0]); err != nil {
		return UNC{}, fmt.Errorf("%w: %w", ErrInvalidUNC, err)
	}
	if len(parts) < 2 || parts[1] == "" {
		return UNC{}, uncError("the share name is missing")
	}
	share := parts[1]
	switch {
	case utf8.RuneCountInString(share) > maxShareLength:
		return UNC{}, uncError("the share name is longer than %d characters", maxShareLength)
	case strings.HasSuffix(share, "$"):
		return UNC{}, uncError("the share name must not end with $: hidden and administrative shares are not allowed")
	case ValidateName(share) != nil:
		return UNC{}, uncError("the share name is not a valid Windows name")
	}
	u := UNC{Host: parts[0], Share: share}
	for i, folder := range parts[2:] {
		switch {
		case folder == "":
			return UNC{}, uncError("the folder name at position %d is empty (two separators in a row)", i+1)
		case folder == "." || folder == "..":
			return UNC{}, uncError("the folder name at position %d is . or .., which is not allowed", i+1)
		case ValidateName(folder) != nil:
			return UNC{}, uncError("the folder name at position %d is not a valid Windows name (a forbidden character, a reserved name, a trailing dot or space, or more than %d characters)", i+1, MaxNameUnits)
		}
	}
	if len(parts) > 2 {
		u.Rest = parts[2:]
	}
	return u, nil
}

// String is the path with backslashes and no trailing separator: \\host\share\folder.
func (u UNC) String() string {
	var b strings.Builder
	b.WriteString(`\\`)
	b.WriteString(u.Host)
	b.WriteByte('\\')
	b.WriteString(u.Share)
	for _, folder := range u.Rest {
		b.WriteByte('\\')
		b.WriteString(folder)
	}
	return b.String()
}

// Join appends a relative path, which may use either slash, to the path. Separators
// at either end of rel are ignored, so an empty rel gives String.
func (u UNC) Join(rel string) string {
	rel = strings.Trim(strings.ReplaceAll(rel, "/", `\`), `\`)
	if rel == "" {
		return u.String()
	}
	return u.String() + `\` + rel
}
