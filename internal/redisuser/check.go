package redisuser

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// maxProblems is how many Problem entries a Report lists.
const maxProblems = 20

// FieldStat says how one field of the hash fared over all hashes of the directory.
type FieldStat struct {
	Present int // hashes that have the field
	OK      int // decoded, and of the type the field is expected to have
	Failed  int // present, but not decodable or of an unexpected type
}

// Problem points at a field that could not be read. It names neither the key
// nor any data: N is the 1-based position of the key when all keys of the
// directory are sorted bytewise, which is how to find it again in redis-cli.
type Problem struct {
	N      int
	Field  string // empty when the whole key is affected
	Reason string
}

// Report is the result of Check. It holds counts and ordinals only, never
// names, e-mail addresses, passwords or keys.
type Report struct {
	Keys       int // keys under the prefix
	Records    int // readable records
	Unreadable int // hashes with a required field that is missing or unreadable
	NotHash    int // keys that hold something other than a hash
	Vanished   int // keys that disappeared while the check ran

	Fields map[string]FieldStat // by field name

	Active, Inactive       int // readable records by Activated
	Admin, User, OtherType int // readable records by Type
	NoPassword             int // readable records without a password
	DuplicateECodes        int // ee_codes (case-insensitive) that appear on more than one key

	Elapsed      time.Duration
	Problems     []Problem // the first ones, at most maxProblems
	ProblemCount int       // all of them
}

// Check reads every hash under the prefix and reports how readable the
// directory is. Use it to find records the decoder cannot handle. It has no
// time limit of its own and is not capped by MaxScan; give ctx a deadline.
func (s *Store) Check(ctx context.Context) (Report, error) {
	start := time.Now()
	keys, _, err := s.scan(ctx, s.glob+"*", 0)
	if err != nil {
		return Report{}, err
	}
	sort.Strings(keys)
	rep := Report{Keys: len(keys), Fields: make(map[string]FieldStat, len(fieldSpecs))}
	ecodes := map[string]int{}
	for base := 0; base < len(keys); base += pipelineChunk {
		got, err := s.hgetall(ctx, keys[base:min(base+pipelineChunk, len(keys))], base)
		if err != nil {
			return Report{}, err
		}
		for _, f := range got {
			rep.add(f, ecodes)
		}
	}
	for _, n := range ecodes {
		if n > 1 {
			rep.DuplicateECodes++
		}
	}
	rep.Elapsed = time.Since(start)
	return rep, nil
}

func (r *Report) add(f fetched, ecodes map[string]int) {
	switch {
	case f.notHash:
		r.NotHash++
		r.problem(f.n, "", "not a hash")
		return
	case len(f.hash) == 0:
		r.Vanished++
		return
	}
	h := decodeHash(f.key, f.hash, true)
	for i, spec := range fieldSpecs {
		st := r.Fields[spec.name]
		switch h.state[i] {
		case fieldOK:
			st.Present++
			st.OK++
		case fieldBad:
			st.Present++
			st.Failed++
			r.problem(f.n, spec.name, h.reason[i])
		case fieldAbsent:
			if h.reason[i] != "" { // a required field that is missing
				r.problem(f.n, spec.name, h.reason[i])
			}
		}
		r.Fields[spec.name] = st
	}
	if ecode := h.rec.ECode; ecode != "" {
		ecodes[ecode]++
	}
	if !h.usable {
		r.Unreadable++
		return
	}
	r.Records++
	if h.rec.Activated {
		r.Active++
	} else {
		r.Inactive++
	}
	switch {
	case h.rec.IsAdmin():
		r.Admin++
	case strings.EqualFold(h.rec.Type, "user"):
		r.User++
	default:
		r.OtherType++
	}
	if !h.rec.HasPassword() {
		r.NoPassword++
	}
}

func (r *Report) problem(n int, field, reason string) {
	r.ProblemCount++
	if len(r.Problems) < maxProblems {
		r.Problems = append(r.Problems, Problem{N: n, Field: field, Reason: reason})
	}
}

// Format renders the report as plain text for an administrator.
func (r Report) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "user directory check: %d keys in %s\n", r.Keys, r.Elapsed.Round(time.Millisecond))
	fmt.Fprintf(&b, "  readable records: %d\n  unreadable keys:  %d\n  not a hash:       %d\n  vanished:         %d\n",
		r.Records, r.Unreadable, r.NotHash, r.Vanished)
	fmt.Fprintf(&b, "  activated: %d, not activated: %d\n", r.Active, r.Inactive)
	fmt.Fprintf(&b, "  type admin: %d, user: %d, other: %d\n", r.Admin, r.User, r.OtherType)
	fmt.Fprintf(&b, "  without password: %d\n", r.NoPassword)
	fmt.Fprintf(&b, "  ee_codes on more than one key: %d\n", r.DuplicateECodes)
	b.WriteString("fields (present / decoded / failed):\n")
	for _, spec := range fieldSpecs {
		st := r.Fields[spec.name]
		fmt.Fprintf(&b, "  %-17s %d / %d / %d\n", spec.name, st.Present, st.OK, st.Failed)
	}
	if r.ProblemCount > 0 {
		fmt.Fprintf(&b, "problems (%d of %d; #N is the position of the key among all keys sorted by name):\n", len(r.Problems), r.ProblemCount)
		for _, p := range r.Problems {
			field := p.Field
			if field == "" {
				field = "key"
			}
			fmt.Fprintf(&b, "  #%d %s: %s\n", p.N, field, p.Reason)
		}
	}
	return b.String()
}
