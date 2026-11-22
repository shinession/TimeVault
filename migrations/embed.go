// Package migrations embeds the SQL schema migrations.
package migrations

import (
	"embed"
	"io/fs"
)

// FS holds the numbered *.sql files applied by internal/db.Migrate to the schema of a server.
//
//go:embed *.sql
var FS embed.FS

//go:embed cluster/*.sql
var clusterFiles embed.FS

// ClusterFS holds the numbered *.sql files of the register of the servers that share a database. They are applied by
// internal/db.Migrate in the schema timevault_cluster, apart from the migrations of the servers.
var ClusterFS = func() fs.FS {
	sub, err := fs.Sub(clusterFiles, "cluster")
	if err != nil {
		panic(err)
	}
	return sub
}()
