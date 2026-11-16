// Package listing is the engine behind the folder lists of the web pages. It
// gives every item a type, filters, sorts and pages a list in memory and counts
// the items of each type, without knowing where the items come from.
package listing

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"timevault/internal/pathutil"
)

// Sort is the column a list is ordered by.
type Sort string

// The columns a list can be sorted by.
const (
	SortName  Sort = "name"
	SortType  Sort = "type"
	SortSize  Sort = "size"
	SortMTime Sort = "mtime"
)

const (
	// DefaultLimit is the page size when Options.Limit is not positive.
	DefaultLimit = 100
	// MaxLimit is the largest page size; bigger limits are cut to it.
	MaxLimit = 1000
)

const (
	maxTypes  = 200 // facets reported
	maxExtLen = 16  // runes in an extension
	extPrefix = "ext:"
)

// Type keys. A file with an extension has the key "ext:" plus the extension.
const (
	keyDir   = "dir"
	keyLink  = "link"
	keyNoExt = "noext"
	// filterFile is a filter only: it matches every item that is not a folder.
	filterFile = "file"
)

// Options says which part of a list is wanted and in which order.
type Options struct {
	// Sort is the column to order by; "" or an unknown value means SortName.
	Sort Sort
	// Desc reverses the order, ties included.
	Desc bool
	// Type keeps only items of one type: "dir", "file", "link", "noext" or
	// "ext:<extension>", as NormalizeType returns it. "" and anything else keep everything.
	Type string
	// DirsFirst lists folders before everything else, whatever Desc says.
	DirsFirst bool
	// Offset is how many matching items to skip; a negative value means 0.
	Offset int
	// Limit is the page size; <= 0 means DefaultLimit and > MaxLimit means MaxLimit.
	Limit int
}

// View is what the engine needs to know about one item.
type View struct {
	Name  string
	IsDir bool
	// Special is a symlink, junction or other irregular item. It is never a
	// folder for listing purposes.
	Special bool
	// Size is the size in bytes. A folder counts with the size the caller knows (0 when it knows none); the
	// size of a special item never counts.
	Size int64
	// MTimeNS is the modification time in Unix nanoseconds; 0 means unknown.
	MTimeNS int64
}

// TypeCount is the number of items of one type.
type TypeCount struct {
	Key   string
	Count int
}

// Page is one window of a list.
type Page[T any] struct {
	// Items is the requested window after filtering and sorting; never nil.
	Items []T
	// Total is how many items match the filter.
	Total int
	// Unfiltered is how many items the list has before the filter.
	Unfiltered int
	// Types counts the items of the whole list, before the filter: folders,
	// links, files without extension, then extensions from the most to the
	// least common, at most 200 entries.
	Types []TypeCount
}

// ExtOf returns the lower-cased extension of a file name, or "" when it has
// none. The extension is the text after the last dot and counts only when it
// has 1 to 16 characters and no white space, so "Report. Final version" has none.
func ExtOf(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	ext := strings.ToLower(name[i+1:])
	if n := utf8.RuneCountInString(ext); n == 0 || n > maxExtLen || strings.IndexFunc(ext, unicode.IsSpace) >= 0 {
		return ""
	}
	return ext
}

// TypeKey is the type of an item: "link" for a special item, "dir" for a
// folder, "ext:<extension>" for a file with an extension and "noext" for any
// other file.
func TypeKey(name string, isDir, special bool) string {
	switch {
	case special:
		return keyLink
	case isDir:
		return keyDir
	}
	if ext := ExtOf(name); ext != "" {
		return extPrefix + ext
	}
	return keyNoExt
}

// ParseSort maps a request value to a Sort; anything unknown means SortName.
func ParseSort(s string) Sort {
	switch Sort(s) {
	case SortType, SortSize, SortMTime:
		return Sort(s)
	}
	return SortName
}

// NormalizeType returns the canonical form of a type filter and whether s is
// one: "", "dir", "file", "link", "noext" or "ext:<extension>" with the
// extension trimmed and lower-cased.
func NormalizeType(s string) (string, bool) {
	switch s {
	case "":
		return "", true
	case keyDir, filterFile, keyLink, keyNoExt:
		return s, true
	}
	ext, ok := strings.CutPrefix(s, extPrefix)
	if !ok {
		return "", false
	}
	ext = strings.ToLower(strings.TrimSpace(ext))
	if n := utf8.RuneCountInString(ext); n == 0 || n > maxExtLen || strings.IndexFunc(ext, isExtBreak) >= 0 {
		return "", false
	}
	return extPrefix + ext, true
}

func isExtBreak(r rune) bool {
	return unicode.IsSpace(r) || r == '.' || r == '/' || r == '\\'
}

// Matches reports whether an item whose TypeKey is key passes a type filter.
// The filter "file" passes everything but folders. "" and a filter that
// NormalizeType rejects pass everything.
func Matches(filter, key string) bool {
	canonical, ok := NormalizeType(filter)
	return !ok || matchesCanonical(canonical, key)
}

func matchesCanonical(filter, key string) bool {
	switch filter {
	case "":
		return true
	case filterFile:
		return key != keyDir
	}
	return key == filter
}

// typeRank orders the type groups: folders, links, files without extension, extensions.
func typeRank(key string) uint8 {
	switch key {
	case keyDir:
		return 0
	case keyLink:
		return 1
	case keyNoExt:
		return 2
	}
	return 3
}

// row holds what the comparisons need, computed once per item.
type row struct {
	index int // position in the caller's slice, the last tie-break
	typ   string
	key   string // pathutil.NameKey(name)
	name  string
	size  int64
	mtime int64
	rank  uint8
	dir   bool
}

func compareNames(a, b *row) int {
	if c := strings.Compare(a.key, b.key); c != 0 {
		return c
	}
	return strings.Compare(a.name, b.name)
}

func compareTypes(a, b *row) int {
	if c := cmp.Compare(a.rank, b.rank); c != 0 {
		return c
	}
	if c := strings.Compare(a.typ, b.typ); c != 0 {
		return c
	}
	return compareNames(a, b)
}

func compareSizes(a, b *row) int {
	if c := cmp.Compare(a.size, b.size); c != 0 {
		return c
	}
	return compareNames(a, b)
}

func compareMTimes(a, b *row) int {
	if c := cmp.Compare(a.mtime, b.mtime); c != 0 {
		return c
	}
	return compareNames(a, b)
}

// Apply filters, sorts and pages items. view is called once per item and the
// caller's slice is never modified. The order is total, so equal input always
// gives equal output: ties fall back to the case-insensitive name, then the
// exact name, then the position in items.
func Apply[T any](items []T, view func(T) View, o Options) Page[T] {
	filter, _ := NormalizeType(o.Type)

	rows := make([]row, len(items))
	counts := make(map[string]int)
	for i, it := range items {
		v := view(it)
		typ := TypeKey(v.Name, v.IsDir, v.Special)
		r := row{index: i, typ: typ, key: pathutil.NameKey(v.Name), name: v.Name, mtime: v.MTimeNS, rank: typeRank(typ), dir: v.IsDir && !v.Special}
		if !v.Special {
			r.size = v.Size
		}
		rows[i] = r
		counts[typ]++
	}

	matching := make([]*row, 0, len(rows))
	for i := range rows {
		if matchesCanonical(filter, rows[i].typ) {
			matching = append(matching, &rows[i])
		}
	}
	sortRows(matching, o)

	limit := o.Limit
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	start := min(max(o.Offset, 0), len(matching))
	end := min(start+limit, len(matching))
	window := make([]T, 0, end-start)
	for _, r := range matching[start:end] {
		window = append(window, items[r.index])
	}
	return Page[T]{Items: window, Total: len(matching), Unfiltered: len(items), Types: facets(counts)}
}

func sortRows(rows []*row, o Options) {
	compare := compareNames
	switch o.Sort {
	case SortType:
		compare = compareTypes
	case SortSize:
		compare = compareSizes
	case SortMTime:
		compare = compareMTimes
	}
	sign := 1
	if o.Desc {
		sign = -1
	}
	slices.SortFunc(rows, func(a, b *row) int {
		if o.DirsFirst && a.dir != b.dir {
			if a.dir {
				return -1
			}
			return 1
		}
		if c := compare(a, b); c != 0 {
			return sign * c
		}
		return cmp.Compare(a.index, b.index)
	})
}

func facets(counts map[string]int) []TypeCount {
	types := make([]TypeCount, 0, len(counts))
	for key, n := range counts {
		types = append(types, TypeCount{Key: key, Count: n})
	}
	slices.SortFunc(types, func(a, b TypeCount) int {
		if c := cmp.Compare(typeRank(a.Key), typeRank(b.Key)); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	if len(types) > maxTypes {
		types = types[:maxTypes]
	}
	return types
}
