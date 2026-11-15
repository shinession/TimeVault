// Command timevault-server runs the TimeVault web service and its background workers.
//
//	timevault-server [serve]      run the service (default)
//	timevault-server migrate      apply database migrations and exit
//	timevault-server hash-password  create a value for TIMEVAULT_ADMIN_PASSWORD_HASH
//	timevault-server version
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/config"
	"timevault/internal/db"
	"timevault/internal/jobs"
	"timevault/internal/repository"
	"timevault/internal/restore"
	"timevault/internal/search"
	"timevault/internal/server"
	"timevault/internal/service"
	"timevault/migrations"
	"timevault/web"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.1.0-mvp"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = runServe()
	case "migrate":
		err = runMigrate()
	case "hash-password":
		err = runHashPassword()
	case "version", "-v", "--version":
		fmt.Println("timevault-server", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const usage = `usage: timevault-server [command]

commands:
  serve           run the service (default)
  migrate         apply database migrations and exit
  hash-password   create a value for TIMEVAULT_ADMIN_PASSWORD_HASH
  version         print the version

All settings are read from TIMEVAULT_* environment variables; see docs/runbook.md.
`

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogJSON {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func runMigrate() error {
	cfg, err := config.Load(config.OSLookup)
	if err != nil {
		return err
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}
	log := newLogger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Connect(ctx, cfg.DatabaseURL, db.Options{MaxConns: 2})
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		log.Info("database schema is up to date")
	}
	for _, name := range applied {
		log.Info("applied migration", "name", name)
	}
	return nil
}

func runHashPassword() error {
	password, err := readNewPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	fmt.Println(hash)
	return nil
}

func runServe() error {
	cfg, err := config.Load(config.OSLookup)
	if err != nil {
		return err
	}
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	log := newLogger(cfg)
	log.Info("starting TimeVault", append([]any{"version", version}, cfg.LogAttrs()...)...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL, db.Options{MaxConns: poolSize(cfg.JobWorkers)})
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		applied, err := db.Migrate(ctx, pool, migrations.FS)
		if err != nil {
			return err
		}
		for _, name := range applied {
			log.Info("applied migration", "name", name)
		}
	} else if err := db.CheckSchema(ctx, pool, migrations.FS); err != nil {
		return err
	}
	// Recovery below marks running jobs as failed; that is only right when no other
	// service is working on this database.
	instance, err := db.AcquireInstanceLock(ctx, pool)
	if err != nil {
		return err
	}
	defer instance.Release()

	repo, err := repository.Open(cfg.RepositoryPath)
	if err != nil {
		return err
	}
	log.Info("repository ready", "path", repo.Root(), "id", repo.ID())

	var provider search.Provider = search.NewDisabled()
	if cfg.EverythingURL != "" {
		provider, err = search.NewEverything(search.EverythingOptions{
			BaseURL: cfg.EverythingURL, Username: cfg.EverythingUsername, Password: cfg.EverythingPassword, Timeout: cfg.EverythingTimeout,
		})
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
	}

	sessionKey := cfg.SessionKey
	if len(sessionKey) == 0 {
		log.Warn("TIMEVAULT_SESSION_KEY is not set: sessions end when the service restarts")
	}
	am, err := auth.NewManager(auth.Options{
		AdminUser: cfg.AdminUser, AdminPasswordHash: cfg.AdminPasswordHash, Disabled: cfg.AuthDisabled,
		SessionKey: sessionKey, TTL: cfg.SessionTTL, CookieSecure: cfg.CookieSecure,
	})
	if err != nil {
		return err
	}
	if cfg.AuthDisabled {
		log.Warn("sign-in is DISABLED; the service only accepts connections from this computer")
	}

	cat := catalog.NewStore(pool)
	restoreRoot := cfg.RestoreRoot
	engine := backup.New(backup.Config{
		Catalog: cat, Repository: repo, Logger: log, HashWorkers: cfg.HashWorkers, Protected: []string{repo.Root(), restoreRoot},
	})
	restoreMgr, err := restore.New(restore.Config{
		Catalog: cat, Repository: repo, Pool: pool, Root: restoreRoot, TokenKey: tokenKey(sessionKey), Logger: log,
	})
	if err != nil {
		return err
	}
	if !restoreMgr.Enabled() {
		log.Warn("TIMEVAULT_RESTORE_ROOT is not set: restores are disabled")
	}

	runner := jobs.NewRunner(pool, log, cfg.JobWorkers)
	if n, err := runner.RecoverInterrupted(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Warn("marked interrupted jobs as failed", "count", n)
	}
	if n, err := cat.RecoverStalePoints(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Warn("aborted unfinished recovery points and removed their partial history", "count", n)
	}
	svc := service.New(service.Config{
		Pool: pool, Catalog: cat, Repo: repo, Engine: engine, Restore: restoreMgr, Jobs: runner,
		Audit: audit.New(pool, log), Search: provider, Logger: log,
	})
	runner.Start(ctx)

	handler, err := server.New(svc, am, web.FS, server.Options{Version: version, Location: time.Local, CookieSecure: cfg.CookieSecure}, log)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: cfg.ListenAddr, Handler: handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		stop()
		runner.Shutdown(context.Background())
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "error", err)
	}
	runner.Shutdown(shutdownCtx)
	log.Info("stopped")
	return nil
}

// poolSize leaves room for the busiest case: every job worker running a backup,
// which holds one connection for its source lock and needs another for its queries,
// plus the connection the service lock keeps and the web requests.
func poolSize(jobWorkers int) int32 {
	return int32(max(16, 2*jobWorkers+8))
}

// tokenKey derives the restore-confirmation key from the session key, or
// generates one when sessions are not persistent.
func tokenKey(sessionKey []byte) []byte {
	if len(sessionKey) == 0 {
		k := make([]byte, 32)
		_, _ = rand.Read(k)
		return k
	}
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte("timevault restore confirmation"))
	return mac.Sum(nil)
}

func readNewPassword() (string, error) {
	first, err := readSecret("New password: ")
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(first)) < 12 {
		return "", errors.New("the password must have at least 12 characters")
	}
	if isTerminal() {
		second, err := readSecret("Repeat password: ")
		if err != nil {
			return "", err
		}
		if first != second {
			return "", errors.New("the passwords do not match")
		}
	}
	return first, nil
}
