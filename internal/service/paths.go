package service

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/pathutil"
)

// withDisplayPath applies the policy for the display path of a source to a request, before the engine looks at
// anything else. The display path is the network path users know the folder by, \\server\share\folder.
//
// With a server host configured, the monitored folders are on this server only, so a display path is required and
// its host must be the configured one, compared without regard to case: the short name of a server with a
// domain name is another name. The stored path then spells the host exactly like the configuration. The edit form
// always sends the current value, so the same rules hold for updates. Without a server host the display path is
// optional and only checked for syntax.
func (s *Service) withDisplayPath(req backup.SourceRequest) (backup.SourceRequest, error) {
	if strings.TrimSpace(req.DisplayPath) == "" {
		req.DisplayPath = ""
		if s.serverHost == "" {
			return req, nil
		}
		return req, &backup.SourceError{
			Key: "errors.display_required",
			Msg: fmt.Sprintf(`the display path is required: the network path users know the folder by, such as \\%s\share\folder`, s.serverHost),
		}
	}
	u, err := backup.ParseDisplayPath(req.DisplayPath)
	if err != nil {
		return req, err
	}
	if s.serverHost != "" {
		if !strings.EqualFold(u.Host, s.serverHost) {
			return req, &backup.SourceError{
				Key:  "errors.display_host",
				Args: []any{u.Host, s.serverHost},
				Msg:  fmt.Sprintf(`the display path must start with \\%s\ because the folders are on this server, not \\%s\`, s.serverHost, u.Host),
			}
		}
		u.Host = s.serverHost
	}
	req.DisplayPath = u.String()
	return req, nil
}

// PathMap hides the physical folders of the server from the people who use TimeVault. They reach a backed up
// folder over the network, for example as \\server\TimeVault\monitor for D:\TimeVault\monitor, and only that
// display path is for them; the physical path is for administrators.
//
// A PathMap is a snapshot of the sources and of the repository and restore folders: build one per request with
// Service.PathMap. It never changes and is safe for concurrent use.
type PathMap struct {
	sources map[int64]mappedSource
	restore *pathutil.UNC // the display root of the restore folder, nil when there is none
	roots   []maskRoot    // the folders to hide, the longest first
}

type mappedSource struct {
	name    string
	display *pathutil.UNC // nil when the source has no display path
}

// NewPathMap builds the mapper from the sources, the repository and restore folders (the physical paths) and the
// display root of the restore folder (empty when there is none). Empty folders are ignored.
func NewPathMap(sources []catalog.Source, repositoryRoot, restoreRoot, restoreDisplayRoot string) *PathMap {
	m := &PathMap{sources: make(map[int64]mappedSource, len(sources))}
	if u, err := pathutil.ParseUNC(restoreDisplayRoot); err == nil {
		m.restore = &u
	}
	for _, src := range sources {
		ms := mappedSource{name: src.Name}
		label := "[" + src.Name + "]"
		if u, err := pathutil.ParseUNC(src.DisplayPath); err == nil {
			ms.display = &u
			label = u.String()
		}
		m.sources[src.ID] = ms
		m.addRoot(src.RootPath, label, fmt.Sprintf("[monitored folder %d]", src.ID))
	}
	m.addRoot(repositoryRoot, "[backup folder]", "[backup folder]")
	restoreLabel := "[restore folder]"
	if m.restore != nil {
		restoreLabel = m.restore.String()
	}
	m.addRoot(restoreRoot, restoreLabel, "[restore folder]")

	slices.SortStableFunc(m.roots, func(a, b maskRoot) int { return len(b.runes) - len(a.runes) })
	// A label that would still contain a folder to hide, such as a source named after its folder, must not
	// be put into the text.
	for i := range m.roots {
		if m.contains(m.roots[i].label) {
			m.roots[i].label = m.roots[i].fallback
		}
	}
	return m
}

// SourceDisplay is the display path of a file or folder of a source: the display path of the source followed by
// rel, which may use either slash and is relative to the source. It is "" when the source is unknown or has no
// display path.
func (m *PathMap) SourceDisplay(sourceID int64, rel string) string {
	if ms, ok := m.sources[sourceID]; ok && ms.display != nil {
		return ms.display.Join(rel)
	}
	return ""
}

// SourceLabel names a source for people who must not see its folder: its display path, or else its name. It is
// "" when the source is unknown.
func (m *PathMap) SourceLabel(sourceID int64) string {
	ms, ok := m.sources[sourceID]
	switch {
	case !ok:
		return ""
	case ms.display != nil:
		return ms.display.String()
	}
	return ms.name
}

// RestoreDisplay is the display path of a restore destination below the restore folder, such as the
// destination folder name of a restore. It is "" when the restore folder has no display root.
func (m *PathMap) RestoreDisplay(dest string) string {
	if m.restore == nil {
		return ""
	}
	return m.restore.Join(dest)
}

// maskRoot is one physical folder that must not appear in text.
type maskRoot struct {
	runes    []rune // the folder, with every run of separators collapsed into one sepRune
	drive    bool   // a bare drive such as D:, which only counts when a separator follows
	label    string // what replaces the folder in text
	fallback string // what replaces it when label would still contain a folder to hide
}

// sepRune stands for a run of path separators in a maskRoot.
const sepRune = '\\'

// maxMaskPasses bounds the passes of Mask; text of any real length settles after two.
const maxMaskPasses = 8

// maskFailsafe replaces text that could not be made safe, which only a contrived configuration can cause.
const maskFailsafe = "[path hidden]"

func isSeparator(r rune) bool { return r == '\\' || r == '/' }

func (m *PathMap) addRoot(path, label, fallback string) {
	rs := []rune(strings.TrimSpace(path))
	rs = rs[extendedPrefixLen(rs, 0):]
	runes := make([]rune, 0, len(rs))
	for _, r := range rs {
		if isSeparator(r) {
			if n := len(runes); n > 0 && runes[n-1] == sepRune {
				continue
			}
			r = sepRune
		}
		runes = append(runes, r)
	}
	for len(runes) > 0 && runes[len(runes)-1] == sepRune {
		runes = runes[:len(runes)-1]
	}
	root := maskRoot{runes: runes, label: label, fallback: fallback}
	switch {
	case len(runes) == 2 && runes[1] == ':' && (runes[0] >= 'a' && runes[0] <= 'z' || runes[0] >= 'A' && runes[0] <= 'Z'):
		root.drive = true
	case !slices.Contains(runes, sepRune):
		return // not a folder path
	}
	m.roots = append(m.roots, root)
}

// extendedPrefixLen is the length of the \\?\ prefix of Windows extended paths at rs[i], or 0.
func extendedPrefixLen(rs []rune, i int) int {
	if i+4 <= len(rs) && isSeparator(rs[i]) && isSeparator(rs[i+1]) && rs[i+2] == '?' && isSeparator(rs[i+3]) {
		return 4
	}
	return 0
}

// foldEqual compares two characters without regard to case, the way Windows compares file names.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for r := unicode.SimpleFold(a); r != a; r = unicode.SimpleFold(r) {
		if r == b {
			return true
		}
	}
	return false
}

// isTerminator reports whether r ends a path in running text: white space, quotes, closing brackets and the
// punctuation that cannot be part of a Windows file name or usually follows a path in a sentence.
func isTerminator(r rune) bool {
	switch r {
	case '"', '\'', '`', ',', ';', ':', ')', ']', '}', '<', '>', '|', '?', '*', '!':
		return true
	}
	return unicode.IsSpace(r) || unicode.IsControl(r) || (r > unicode.MaxASCII && unicode.IsPunct(r))
}

// atBoundary reports whether a path that is a folder to hide may end at rs[i]: at the end of the text, before a
// separator or a terminator, or before dots that are followed by one of those (a sentence that ends in a dot).
// A name character, a dot included, means another name: D:\a\b2 is not D:\a\b.
func atBoundary(rs []rune, i int) bool {
	for i < len(rs) && rs[i] == '.' {
		i++
	}
	return i >= len(rs) || isSeparator(rs[i]) || isTerminator(rs[i])
}

// match reports whether the folder starts at rs[i] and, if so, where its text ends. Separators match any run of
// separators and letters match without regard to case.
func (r *maskRoot) match(rs []rune, i int) (end int, ok bool) {
	for _, want := range r.runes {
		if want == sepRune {
			if i >= len(rs) || !isSeparator(rs[i]) {
				return 0, false
			}
			for i < len(rs) && isSeparator(rs[i]) {
				i++
			}
			continue
		}
		if i >= len(rs) || !foldEqual(rs[i], want) {
			return 0, false
		}
		i++
	}
	if r.drive {
		return i, i < len(rs) && isSeparator(rs[i])
	}
	return i, atBoundary(rs, i)
}

// matchAt finds the folder to hide that starts at rs[i], the longest first. A \\?\ in front of it belongs to it.
func (m *PathMap) matchAt(rs []rune, i int) (end int, root *maskRoot) {
	if n := extendedPrefixLen(rs, i); n > 0 {
		if end, root := m.matchFolders(rs, i+n); root != nil {
			return end, root
		}
	}
	return m.matchFolders(rs, i)
}

func (m *PathMap) matchFolders(rs []rune, i int) (end int, root *maskRoot) {
	for k := range m.roots {
		if end, ok := m.roots[k].match(rs, i); ok {
			return end, &m.roots[k]
		}
	}
	return 0, nil
}

// contains reports whether text holds a folder to hide.
func (m *PathMap) contains(text string) bool {
	rs := []rune(text)
	for i := range rs {
		if _, root := m.matchAt(rs, i); root != nil {
			return true
		}
	}
	return false
}

// Mask replaces every physical folder in free text, an error message or a log line for example, with what users
// may see: the display path of a source, [<source name>] for a source without one, [backup folder] for the
// repository and the display root of the restore folder, or [restore folder] without one. What follows the
// folder in the path is kept, with its separators turned into backslashes.
//
// A folder is found without regard to case, with either slash and with a \\?\ in front, and only where it ends
// at the end of the text, before a separator, white space, a quote, a closing bracket or other punctuation
// (also a full stop at the end of a sentence): D:\a\b2 is not D:\a\b. The longest folder wins. Text is scanned
// as decoded, not as it looks in JSON or in an escaped Go string; the result never contains a folder to hide.
func (m *PathMap) Mask(text string) string {
	if len(m.roots) == 0 || !strings.ContainsAny(text, `\/`) {
		return text
	}
	// Replacing one folder can put another one together, for example D:\aE:\b; run until nothing is left to hide.
	for range maxMaskPasses {
		masked, changed := m.maskOnce(text)
		if !changed {
			return text
		}
		text = masked
	}
	if _, changed := m.maskOnce(text); changed {
		return maskFailsafe
	}
	return text
}

func (m *PathMap) maskOnce(text string) (string, bool) {
	rs := []rune(text)
	var b strings.Builder
	b.Grow(len(text) + 16)
	changed, inPath := false, false
	for i := 0; i < len(rs); {
		if end, root := m.matchAt(rs, i); root != nil {
			b.WriteString(root.label)
			i, changed, inPath = end, true, true
			continue
		}
		r := rs[i]
		switch {
		case isTerminator(r):
			inPath = false
		case inPath && r == '/':
			r = '\\'
		}
		b.WriteRune(r)
		i++
	}
	if !changed {
		return text, false
	}
	return b.String(), true
}

// MaskValue returns a copy of v with Mask applied to every string in it: a string, a []string, a []any and a
// map[string]any, the last two recursively. The keys of a map are kept. Values of any other type are returned
// unchanged.
func (m *PathMap) MaskValue(v any) any {
	switch x := v.(type) {
	case string:
		return m.Mask(x)
	case []string:
		if x == nil {
			return x
		}
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = m.Mask(s)
		}
		return out
	case []any:
		if x == nil {
			return x
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = m.MaskValue(e)
		}
		return out
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = m.MaskValue(e)
		}
		return out
	}
	return v
}
