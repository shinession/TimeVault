package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mock is an in-memory Provider for tests of other packages. It applies the
// same query validation, root confinement and paging rules as Everything:
// entries must be strictly inside Query.Root and every sanitized term must be
// contained, case-insensitively, in Result.Name. Matches are ordered like the
// real engine's name sort: folders first, then by name.
type Mock struct {
	mu          sync.RWMutex
	entries     []Result
	unavailable bool
}

var _ Provider = (*Mock)(nil)

// NewMock returns a provider that searches a copy of entries. Result.Path must
// be the absolute path including Result.Name.
func NewMock(entries []Result) *Mock {
	return &Mock{entries: append([]Result(nil), entries...)}
}

// SetUnavailable makes Search fail with ErrUnavailable and Health report
// degraded until it is called again with false.
func (m *Mock) SetUnavailable(unavailable bool) {
	m.mu.Lock()
	m.unavailable = unavailable
	m.mu.Unlock()
}

// Name implements Provider.
func (m *Mock) Name() string { return "mock" }

// Search implements Provider.
func (m *Mock) Search(ctx context.Context, q Query) (Page, error) {
	req, err := newRequest(q)
	if err != nil {
		return Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return Page{}, fmt.Errorf("search: %w", err)
	}
	m.mu.RLock()
	down := m.unavailable
	matches := make([]Result, 0, len(m.entries))
	for _, e := range m.entries {
		if req.root.contains(e.Path) && containsAll(e.Name, req.terms) {
			matches = append(matches, e)
		}
	}
	m.mu.RUnlock()
	if down {
		return Page{}, unavailable("mock engine is down")
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		if an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name); an != bn {
			return an < bn
		}
		return strings.ToLower(a.Path) < strings.ToLower(b.Path)
	})
	total := len(matches)
	start := min(req.offset, total)
	end := min(start+req.limit, total)
	return Page{
		Items:      matches[start:end:end],
		Total:      total,
		Offset:     req.offset,
		NextOffset: req.next(end-start, total),
	}, nil
}

// Health implements Provider.
func (m *Mock) Health(context.Context) Health {
	m.mu.RLock()
	down := m.unavailable
	m.mu.RUnlock()
	h := Health{State: StateOK, CheckedAt: time.Now()}
	if down {
		h.State, h.Detail = StateDegraded, "mock engine is down"
	}
	return h
}

func containsAll(name string, terms []string) bool {
	name = strings.ToLower(name)
	for _, term := range terms {
		if !strings.Contains(name, strings.ToLower(term)) {
			return false
		}
	}
	return true
}
