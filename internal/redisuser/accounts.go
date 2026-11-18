package redisuser

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"timevault/internal/auth"
)

// Active lists the accounts that can sign in: the newest record of every employee ID, when that record is
// activated. It reads every hash under the prefix, so it is not for every request; Directory.Accounts keeps the
// answer for a while. It never reads more than maxScanLimit keys.
func (s *Store) Active(ctx context.Context) ([]Record, error) {
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()
	keys, _, err := s.scan(ctx, s.glob+"*", maxScanLimit)
	if err != nil {
		return nil, err
	}
	slices.Sort(keys)
	newest := map[string]Record{}
	for base := 0; base < len(keys); base += pipelineChunk {
		got, err := s.hgetall(ctx, keys[base:min(base+pipelineChunk, len(keys))], base)
		if err != nil {
			return nil, err
		}
		for _, f := range got {
			if !f.exists() {
				continue
			}
			h := decodeHash(f.key, f.hash, false)
			if !h.usable || h.rec.ECode == "" {
				continue
			}
			if cur, ok := newest[h.rec.ECode]; !ok || newerRecord(h.rec, cur) {
				newest[h.rec.ECode] = h.rec
			}
		}
	}
	out := make([]Record, 0, len(newest))
	for _, r := range newest {
		if r.Activated {
			out = append(out, r)
		}
	}
	return out, nil
}

// newerRecord is the order sortNewest uses: the later UpdateDate wins, and the key decides between equal dates, so
// the outcome never depends on the order SCAN returned the keys in.
func newerRecord(a, b Record) bool {
	if a.Updated.Equal(b.Updated) {
		return a.Ref < b.Ref
	}
	return a.Updated.After(b.Updated)
}

// accountsTTL is how long a list of accounts is kept before the directory is read again.
const accountsTTL = time.Minute

// accountList is the last list Directory.Accounts built.
type accountList struct {
	mu   sync.Mutex
	at   time.Time
	list []auth.Account
}

var _ auth.AccountLister = (*Directory)(nil)

// Accounts implements auth.AccountLister. The list holds the employee ID and the name of every account that can sign
// in, ordered by name, and is kept for a minute: reading the directory takes one round trip for every 50 accounts.
func (d *Directory) Accounts(ctx context.Context) ([]auth.Account, error) {
	d.accounts.mu.Lock()
	defer d.accounts.mu.Unlock()
	if d.accounts.list != nil && d.clock().Sub(d.accounts.at) < accountsTTL {
		return slices.Clone(d.accounts.list), nil
	}
	recs, err := d.store.Active(ctx)
	if err != nil {
		return nil, d.answer(err, "account list")
	}
	list := make([]auth.Account, 0, len(recs))
	for _, r := range recs {
		list = append(list, auth.Account{ID: r.ECode, Name: identityOf(r).Name})
	}
	slices.SortFunc(list, func(a, b auth.Account) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	d.accounts.list, d.accounts.at = list, d.clock()
	return slices.Clone(list), nil
}
