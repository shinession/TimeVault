package search

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxRootLength bounds the length of the folder of a request.
const maxRootLength = 4096

func badQuery(msg string) error {
	return fmt.Errorf("%w: %s", ErrBadQuery, msg)
}

// Sanitize turns untrusted user text into literal search terms.
//
// Terms are separated by whitespace; a double-quoted phrase is one term. Double
// quotes are never part of a term. '*' and '?' cannot occur in a Windows file
// name, so they separate terms instead of being searched for. Everything else
// (| ! < > : regex:, size: and so on) is part of the term: names are matched as
// plain text, there are no operators.
func Sanitize(text string) ([]string, error) {
	if len(text) > MaxTextLength {
		return nil, badQuery("text is longer than " + strconv.Itoa(MaxTextLength) + " bytes")
	}
	if !utf8.ValidString(text) {
		return nil, badQuery("text is not valid UTF-8")
	}
	var (
		terms   []string
		current strings.Builder
		quoted  bool
	)
	flush := func() {
		if term := strings.TrimSpace(current.String()); term != "" {
			terms = append(terms, term)
		}
		current.Reset()
	}
	for _, r := range text {
		switch {
		case unicode.IsControl(r):
			return nil, badQuery("text contains a control character")
		case r == '"':
			flush()
			quoted = !quoted
		case r == '*' || r == '?':
			flush()
		case unicode.IsSpace(r) && !quoted:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	switch {
	case len(terms) == 0:
		return nil, badQuery("text has no search terms")
	case len(terms) > MaxTerms:
		return nil, badQuery("text has more than " + strconv.Itoa(MaxTerms) + " search terms")
	}
	return terms, nil
}

// rootPath is an absolute Windows directory split for case-insensitive,
// path-boundary-aware comparison.
type rootPath struct {
	volume   string // "D:" or `\\server\share`
	segments []string
}

func parseRoot(raw string) (rootPath, error) {
	if strings.TrimSpace(raw) == "" {
		return rootPath{}, badQuery("root is empty")
	}
	if len(raw) > maxRootLength {
		return rootPath{}, badQuery("root is too long")
	}
	volume, segments, ok := splitAbsolute(raw)
	if !ok {
		return rootPath{}, badQuery("root must be a canonical absolute Windows path")
	}
	names := segments
	if strings.HasPrefix(volume, `\\`) {
		names = append(strings.Split(volume[2:], `\`), segments...)
	}
	for _, name := range names {
		if !validName(name) {
			return rootPath{}, badQuery("root contains characters that cannot appear in a path")
		}
	}
	return rootPath{volume: volume, segments: segments}, nil
}

// validName rejects what Windows does not allow in a path element.
func validName(name string) bool {
	if name == "" || strings.ContainsAny(name, `<>:"|?*`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// splitAbsolute splits a drive-letter or UNC path. It accepts '/' as a
// separator and one trailing separator, and refuses anything that is not
// canonical: relative paths, \\?\ and \\.\ prefixes, empty, "." and ".." elements.
func splitAbsolute(p string) (volume string, segments []string, ok bool) {
	p = strings.ReplaceAll(p, "/", `\`)
	var rest string
	switch {
	case len(p) >= 3 && isDriveLetter(p[0]) && p[1] == ':' && p[2] == '\\':
		volume, rest = strings.ToUpper(p[:2]), p[3:]
	case strings.HasPrefix(p, `\\`) && !strings.HasPrefix(p, `\\?\`) && !strings.HasPrefix(p, `\\.\`):
		parts := strings.SplitN(p[2:], `\`, 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", nil, false
		}
		volume = `\\` + parts[0] + `\` + parts[1]
		if len(parts) == 3 {
			rest = parts[2]
		}
	default:
		return "", nil, false
	}
	if rest == "" {
		return volume, nil, true
	}
	segments = strings.Split(strings.TrimSuffix(rest, `\`), `\`)
	for _, s := range segments {
		if s == "" || s == "." || s == ".." {
			return "", nil, false
		}
	}
	return volume, segments, true
}

func isDriveLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// scope is the root with a trailing backslash.
func (r rootPath) scope() string {
	if len(r.segments) == 0 {
		return r.volume + `\`
	}
	return r.volume + `\` + strings.Join(r.segments, `\`) + `\`
}

// contains reports whether p is strictly inside the root. It is the
// provider-independent re-check of everything the engine returns.
func (r rootPath) contains(p string) bool {
	volume, segments, ok := splitAbsolute(p)
	if !ok || len(segments) <= len(r.segments) || !strings.EqualFold(volume, r.volume) {
		return false
	}
	for i, s := range r.segments {
		if !strings.EqualFold(segments[i], s) {
			return false
		}
	}
	return true
}

// request is a validated Query.
type request struct {
	root   rootPath
	terms  []string
	offset int
	limit  int
}

func newRequest(q Query) (request, error) {
	root, err := parseRoot(q.Root)
	if err != nil {
		return request{}, err
	}
	terms, err := Sanitize(q.Text)
	if err != nil {
		return request{}, err
	}
	if q.Offset < 0 || q.Offset > math.MaxInt32 {
		return request{}, badQuery("offset is out of range")
	}
	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0 || limit > MaxLimit:
		return request{}, badQuery("limit must be between 1 and " + strconv.Itoa(MaxLimit))
	}
	return request{root: root, terms: terms, offset: q.Offset, limit: limit}, nil
}

// next is the offset of the page after this one, or -1.
func (r request) next(returned, total int) int {
	if returned > 0 && r.offset+returned < total {
		return r.offset + returned
	}
	return -1
}
