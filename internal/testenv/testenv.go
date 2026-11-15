// Package testenv builds a complete, isolated TimeVault back end for tests: a
// migrated PostgreSQL schema, a repository, a registered source with real
// files on disk, a backup engine and a restore manager.
package testenv

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/db/dbtest"
	"timevault/internal/repository"
	"timevault/internal/restore"
)

// Clock is a settable clock shared with the restore manager.
type Clock struct{ T time.Time }

// Now implements the clock function expected by the managers.
func (c *Clock) Now() time.Time { return c.T }

// Env is the assembled back end.
type Env struct {
	T           *testing.T
	Pool        *pgxpool.Pool
	Cat         *catalog.Store
	Repo        *repository.Store
	Eng         *backup.Engine
	Restore     *restore.Manager
	SrcDir      string
	RestoreRoot string
	Src         catalog.Source
	Clock       *Clock

	files int
}

// New creates the environment. The test is skipped when no test database is configured.
func New(t *testing.T) *Env {
	t.Helper()
	pool := dbtest.New(t)
	base := t.TempDir()
	repo, err := repository.Open(filepath.Join(base, "repo"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{
		T: t, Pool: pool, Cat: catalog.NewStore(pool), Repo: repo,
		SrcDir:      filepath.Join(t.TempDir(), "source"),
		RestoreRoot: filepath.Join(t.TempDir(), "restores"),
		Clock:       &Clock{T: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)},
	}
	if err := os.MkdirAll(e.SrcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e.Restore, err = restore.New(restore.Config{
		Catalog: e.Cat, Repository: repo, Pool: pool, Root: e.RestoreRoot,
		TokenKey: []byte("0123456789abcdef0123456789abcdef"), Now: e.Clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Eng = backup.New(backup.Config{
		Catalog: e.Cat, Repository: repo, HashWorkers: 4,
		Protected: []string{repo.Root(), e.RestoreRoot},
	})
	req, err := e.Eng.PrepareSource(context.Background(), backup.SourceRequest{
		Name: "Team Files", RootPath: e.SrcDir, Excludes: backup.DefaultExcludes,
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if e.Src, err = e.Cat.CreateSource(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	return e
}

// Write creates or replaces a source file with a distinct modification time.
func (e *Env) Write(rel, content string) {
	e.T.Helper()
	p := filepath.Join(e.SrcDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.T.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.T.Fatal(err)
	}
	e.files++
	mt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(e.files) * time.Minute)
	if err := os.Chtimes(p, mt, mt); err != nil {
		e.T.Fatal(err)
	}
}

// Remove deletes a source file or folder.
func (e *Env) Remove(rel string) {
	e.T.Helper()
	if err := os.RemoveAll(filepath.Join(e.SrcDir, filepath.FromSlash(rel))); err != nil {
		e.T.Fatal(err)
	}
}

// Backup takes a recovery point and fails the test if that does not work.
func (e *Env) Backup() catalog.Point {
	e.T.Helper()
	p, err := e.Eng.Run(context.Background(), e.Src.ID, backup.RunOptions{}, nil)
	if err != nil {
		e.T.Fatalf("backup: %v", err)
	}
	return p
}

// ReadRestored returns the content of a file below the restore root.
func (e *Env) ReadRestored(rel string) string {
	e.T.Helper()
	b, err := os.ReadFile(filepath.Join(e.RestoreRoot, filepath.FromSlash(rel)))
	if err != nil {
		e.T.Fatalf("read restored %s: %v", rel, err)
	}
	return string(b)
}

// Restored reports whether a path below the restore root exists.
func (e *Env) Restored(rel string) bool {
	_, err := os.Lstat(filepath.Join(e.RestoreRoot, filepath.FromSlash(rel)))
	return err == nil
}
