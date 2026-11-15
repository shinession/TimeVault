// Package migrations embeds the SQL schema migrations.
package migrations

import "embed"

// FS holds the numbered *.sql files applied by internal/db.Migrate.
//
//go:embed *.sql
var FS embed.FS
