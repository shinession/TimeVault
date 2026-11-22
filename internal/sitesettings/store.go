// Package sitesettings keeps the Site settings (see internal/config/site.go) in the database.
//
// A row holds the value of a setting that was given one; a setting without a row has its default. The package only
// stores: whether the values suit each other is checked by config.SiteProblems, before they are saved.
package sitesettings

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/config"
)

// maxBy bounds the name of the account that is stored with a value.
const maxBy = 128

// Store reads and writes the table site_settings.
type Store struct{ pool *pgxpool.Pool }

// New returns a Store on pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Row is one stored setting.
type Row struct {
	Key       string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string
}

// Load returns the stored values by key. A key that this version does not know (a setting of a later version that
// was stored before a downgrade) is returned like the others; config.SiteLookup ignores it.
func (s *Store) Load(ctx context.Context) (map[string]string, error) {
	rows, err := s.Rows(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// Rows returns the stored settings with who saved them and when, ordered by key.
func (s *Store) Rows(ctx context.Context) ([]Row, error) {
	res, err := s.pool.Query(ctx, `SELECT key, value, updated_at, updated_by FROM site_settings ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("sitesettings: read: %w", err)
	}
	defer res.Close()
	var out []Row
	for res.Next() {
		var r Row
		if err := res.Scan(&r.Key, &r.Value, &r.UpdatedAt, &r.UpdatedBy); err != nil {
			return nil, fmt.Errorf("sitesettings: read: %w", err)
		}
		out = append(out, r)
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("sitesettings: read: %w", err)
	}
	return out, nil
}

// ErrInvalid means a key is not a Site setting, or a value to store is empty.
var ErrInvalid = errors.New("sitesettings: invalid setting")

// Save stores the values of set and removes the rows of reset, in one transaction: either everything is saved or
// nothing is. A removed row means the default applies again. by names the account that does it.
func (s *Store) Save(ctx context.Context, set map[string]string, reset []string, by string) error {
	keys := make([]string, 0, len(set))
	for k, v := range set {
		if _, ok := config.SiteSettingByKey(k); !ok {
			return fmt.Errorf("%w: %s is not a Site setting", ErrInvalid, k)
		}
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%w: %s has no value to store (reset it instead)", ErrInvalid, k)
		}
		keys = append(keys, k)
	}
	for _, k := range reset {
		if _, ok := config.SiteSettingByKey(k); !ok {
			return fmt.Errorf("%w: %s is not a Site setting", ErrInvalid, k)
		}
	}
	sort.Strings(keys)
	if len(by) > maxBy {
		by = by[:maxBy]
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sitesettings: save: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, k := range keys {
		if _, err := tx.Exec(ctx,
			`INSERT INTO site_settings (key, value, updated_by) VALUES ($1, $2, $3)
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
			k, strings.TrimSpace(set[k]), by); err != nil {
			return fmt.Errorf("sitesettings: save %s: %w", k, err)
		}
	}
	for _, k := range reset {
		if _, err := tx.Exec(ctx, `DELETE FROM site_settings WHERE key = $1`, k); err != nil {
			return fmt.Errorf("sitesettings: reset %s: %w", k, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sitesettings: save: %w", err)
	}
	return nil
}
