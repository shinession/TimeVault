// Command timevault-server runs the TimeVault web service and its background workers.
//
//	timevault-server [serve]        run the service (default)
//	timevault-server migrate        apply database migrations and exit
//	timevault-server hash-password  create a value for TIMEVAULT_ADMIN_PASSWORD_HASH
//	timevault-server check-redis-users  check that the user accounts in Redis can be read
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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/backup"
	"timevault/internal/catalog"
	"timevault/internal/config"
	"timevault/internal/db"
	"timevault/internal/jobs"
	"timevault/internal/platform"
	"timevault/internal/redisuser"
	"timevault/internal/repository"
	"timevault/internal/restore"
	"timevault/internal/search"
	"timevault/internal/server"
	"timevault/internal/service"
	"timevault/migrations"
	"timevault/web"
)

// version is the one place the program version is written. The build command in docs/runbook.md does not
// set it: -ldflags "-X main.version=..." would replace it, and an old copy of that command stamps an old number.
var version = "0.1.5"

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
	case "check-redis-users":
		err = runCheckRedisUsers()
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
  serve               run the service (default)
  migrate             apply database migrations and exit
  hash-password       create a value for TIMEVAULT_ADMIN_PASSWORD_HASH
  check-redis-users   check that the user accounts in Redis can be read (prints counts, never names or passwords)
  version             print the version

Settings are read from TIMEVAULT_* environment variables and from a .env file: the one named by
TIMEVAULT_ENV_FILE, else .env in the working directory, else .env next to the executable.
A variable that is set in the environment wins over the file; the startup log names the ones
that do. See docs/runbook.md.
`

// loadConfig reads the settings from the environment and the .env file.
func loadConfig() (config.Config, error) {
	lookup, envPath, err := config.LoadEnv(config.OSLookup)
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Load(lookup)
	if err != nil {
		return config.Config{}, err
	}
	cfg.EnvFile = envPath
	cfg.EnvOverrides = config.EnvOverrides(config.OSLookup, envPath)
	return cfg, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogJSON {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func runMigrate() error {
	cfg, err := loadConfig()
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

// runCheckRedisUsers reads the whole user directory once and reports how much of
// it can be used. The report holds counts only: no name, e-mail address or password.
func runCheckRedisUsers() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.RedisURL == "" {
		return errors.New("TIMEVAULT_REDIS_URL is not set, neither in the environment nor in the .env file")
	}
	store, err := redisuser.Open(redisuser.Options{URL: cfg.RedisURL, KeyPrefix: cfg.RedisKeyPrefix, Timeout: cfg.RedisTimeout})
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	settings := cfg.EnvFile
	if settings == "" {
		settings = "(none: the environment only)"
	}
	fmt.Printf("settings file: %s\nuser directory: %s, key prefix %q\n", settings, store.Addr(), cfg.RedisKeyPrefix)
	if len(cfg.EnvOverrides) > 0 {
		fmt.Printf("overridden by the process environment: %s\n", strings.Join(cfg.EnvOverrides, ", "))
	}
	if err := store.Ping(ctx); err != nil {
		return fmt.Errorf("the user directory does not answer: %w", err)
	}
	report, err := store.Check(ctx)
	if err != nil {
		return err
	}
	fmt.Print(report.Format())
	if report.Records == 0 {
		return errors.New("no readable user record was found under the key prefix, so nobody could sign in")
	}
	return nil
}

func runServe() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	log := newLogger(cfg)
	log.Info("starting TimeVault", append([]any{"version", version}, cfg.LogAttrs()...)...)
	if prio, err := platform.ParsePriority(cfg.ProcessPriority); err == nil && prio != platform.PriorityNormal {
		if err := platform.SetPriority(prio); err != nil {
			log.Warn("could not lower the priority of the service; it keeps running at normal priority", "priority", string(prio), "error", err)
		} else {
			log.Info("the service runs at a lower priority, so it yields to the other services of this computer", "priority", string(prio))
		}
	}
	if len(cfg.EnvOverrides) > 0 {
		log.Warn("these settings come from the process environment and override the .env file, so editing the file does not change them: remove the environment variables, or set them to the value you want",
			"settings", strings.Join(cfg.EnvOverrides, ","), "env_file", cfg.EnvFile)
	}
	if host, _, err := net.SplitHostPort(cfg.ListenAddr); err == nil && !cfg.IsLoopbackListen(host) {
		log.Warn("the service is reachable from other computers: anyone who can reach it can browse the names, sizes and history of the monitored folders without signing in, and account passwords are typed into this site, so serve it over HTTPS through a TLS reverse proxy",
			"cookie_secure", cfg.CookieSecure)
	}

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
	authOptions := auth.Options{
		AdminUser: cfg.AdminUser, AdminPasswordHash: cfg.AdminPasswordHash, Disabled: cfg.AuthDisabled,
		SessionKey: sessionKey, TTL: cfg.SessionTTL, CookieSecure: cfg.CookieSecure, CookiePath: cfg.BasePath,
	}
	// directory stays nil, and so do the interface values it feeds, when no user directory is configured.
	var directory *redisuser.Directory
	if cfg.RedisURL != "" {
		store, err := redisuser.Open(redisuser.Options{URL: cfg.RedisURL, KeyPrefix: cfg.RedisKeyPrefix, Timeout: cfg.RedisTimeout})
		if err != nil {
			return fmt.Errorf("user directory: %w", err)
		}
		defer store.Close()
		directory = redisuser.NewDirectory(store, log)
		authOptions.Directory = directory
		// Redis may start after the service, so an answer is not required here.
		pingCtx, cancelPing := context.WithTimeout(ctx, 2*cfg.RedisTimeout)
		if err := store.Ping(pingCtx); err != nil {
			log.Warn("the user directory does not answer; company accounts cannot sign in until it does", "addr", store.Addr(), "error", err)
		} else {
			log.Info("user directory reachable", "addr", store.Addr())
		}
		cancelPing()
	}
	am, err := auth.NewManager(authOptions)
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
	serviceConfig := service.Config{
		Pool: pool, Catalog: cat, Repo: repo, Engine: engine, Restore: restoreMgr, Jobs: runner,
		Audit: audit.New(pool, log), Search: provider, Logger: log,
		ServerHost: cfg.ServerHost, RestoreDisplayRoot: cfg.RestoreDisplayRoot,
		Scheduler: service.SchedulerOptions{
			Parallel: cfg.SchedulerParallel, GapAuto: cfg.SchedulerGapAuto, Gap: cfg.SchedulerGap,
			RestAuto: cfg.SchedulerRestAuto, Rest: cfg.SchedulerRest,
			Probe: cfg.SchedulerEverythingProbe, FullScanEvery: cfg.SchedulerFullScanEvery,
		},
	}
	if cfg.SchedulerEverythingProbe && cfg.EverythingURL == "" {
		log.Warn("TIMEVAULT_SCHEDULER_EVERYTHING_PROBE is on but TIMEVAULT_EVERYTHING_URL is not set: every scheduled run scans its folder")
	}
	if directory != nil {
		serviceConfig.Directory = directory
	}
	svc := service.New(serviceConfig)

	// Everything that can fail to build comes before the workers start, so that a failure leaves nothing running.
	handler, err := server.New(svc, am, web.FS, server.Options{
		Version: version, Location: time.Local, CookieSecure: cfg.CookieSecure, Deployment: cfg.Describe(),
		SSOHost: cfg.SSOHost, SSORequireSameIP: cfg.SSORequireSameIP, TrustedProxies: cfg.TrustedProxies, BasePath: cfg.BasePath,
	}, log)
	if err != nil {
		return err
	}
	if cfg.BasePath != "" {
		log.Info("serving under a path prefix", "base_path", cfg.BasePath,
			"note", "pages, redirects and cookies use the prefix; requests with or without it are accepted")
	}
	if cfg.SSOHost != "" && directory == nil {
		log.Warn("TIMEVAULT_SSO_HOST is set but TIMEVAULT_REDIS_URL is not: single sign-on needs the user directory, so the login page offers none")
	}
	if cfg.SSOHost != "" && !cfg.SSORequireSameIP {
		log.Warn("single sign-on does not check that the browser has the address the sign-on service saw (TIMEVAULT_SSO_REQUIRE_SAME_IP=false): someone who can make a colleague open a sign-on link could sign in as that colleague; configure TIMEVAULT_TRUSTED_PROXIES and turn the check on")
	}
	srv := &http.Server{
		Addr: cfg.ListenAddr, Handler: handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
	}

	runner.Start(ctx)
	var scheduler sync.WaitGroup
	scheduler.Add(1)
	go func() {
		defer scheduler.Done()
		svc.RunScheduler(ctx)
	}()
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
		scheduler.Wait()
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
	// No backup may be queued once the runner starts to shut down.
	scheduler.Wait()
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
