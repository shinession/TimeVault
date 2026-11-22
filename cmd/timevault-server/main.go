// Command timevault-server runs the TimeVault web service and its background workers.
//
//	timevault-server [serve]        run the service (default)
//	timevault-server migrate        apply database migrations and exit
//	timevault-server hash-password  create a value for TIMEVAULT_ADMIN_PASSWORD_HASH
//	timevault-server check-redis-users  check that the user accounts in Redis can be read
//	timevault-server check-config   check that the settings of the environment and .env are valid for the service and exit
//	timevault-server settings       list, get, set or reset the Site settings stored in the database
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
	"path/filepath"
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
	"timevault/internal/ntfsindex"
	"timevault/internal/platform"
	"timevault/internal/redisuser"
	"timevault/internal/repository"
	"timevault/internal/restore"
	"timevault/internal/search"
	"timevault/internal/server"
	"timevault/internal/service"
	"timevault/internal/sitesettings"
	"timevault/internal/update"
	"timevault/internal/weblog"
	"timevault/migrations"
	"timevault/web"
)

// version is the one place the program version is written. The build command in docs/runbook.md does not
// set it: -ldflags "-X main.version=..." would replace it, and an old copy of that command stamps an old number.
var version = "1.0.1"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serveAndRestart()
	case "migrate":
		err = runMigrate()
	case "hash-password":
		err = runHashPassword()
	case "check-redis-users":
		err = runCheckRedisUsers()
	case "check-config":
		err = runCheckConfig()
	case "settings":
		err = runSettings(os.Args[2:])
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
  check-config        check that the settings of the environment and the .env file are valid for the service,
                      without connecting to anything
  settings            list | get <key> | set <key> <value> | reset <key>: the Site settings, which are stored in
                      the database and are normally edited on the Site settings page (a way back when the
                      page cannot be reached)
  version             print the version

The settings that the service needs before it can reach its database, and its secrets, are read from
TIMEVAULT_* environment variables and from a .env file: the one named by TIMEVAULT_ENV_FILE, else .env
in the working directory, else .env next to the executable. A variable that is set in the environment
wins over the file; the startup log names the ones that do. Everything else is a Site setting, stored in
the database. See docs/runbook.md.
`

// loadBootstrap reads the settings of the environment and the .env file: what the service needs before it can reach its
// database, and its secrets. The Site settings are in the database; they keep their defaults in the result, and the
// returned lookup is what they are checked against together with the settings of the environment.
func loadBootstrap() (config.Config, config.Lookup, error) {
	base, envPath, err := config.LoadEnv(config.OSLookup)
	if err != nil {
		return config.Config{}, nil, err
	}
	cfg, err := config.Load(config.SiteLookup(base, nil))
	if err != nil {
		return config.Config{}, nil, err
	}
	cfg.EnvFile = envPath
	cfg.EnvOverrides = config.EnvOverrides(config.OSLookup, envPath)
	return cfg, base, nil
}

// loadSite reads the Site settings from the database and returns the complete configuration with them, and the
// values that were stored. It connects with a small pool of its own and applies the migrations (or checks that they
// were applied) first, because the table of the Site settings is one of them; the pool of the service is sized by a
// Site setting (job workers), so it can only be made afterwards.
func loadSite(ctx context.Context, boot config.Config, base config.Lookup, log *slog.Logger) (config.Config, map[string]string, error) {
	pool, err := db.Connect(ctx, boot.DatabaseURL, db.Options{MaxConns: 2})
	if err != nil {
		return config.Config{}, nil, err
	}
	defer pool.Close()
	if boot.AutoMigrate {
		applied, err := db.Migrate(ctx, pool, migrations.FS)
		if err != nil {
			return config.Config{}, nil, err
		}
		for _, name := range applied {
			log.Info("applied migration", "name", name)
		}
	} else if err := db.CheckSchema(ctx, pool, migrations.FS); err != nil {
		return config.Config{}, nil, err
	}
	stored, err := sitesettings.New(pool).Load(ctx)
	if err != nil {
		return config.Config{}, nil, err
	}
	cfg, err := config.Load(config.SiteLookup(base, stored))
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("the Site settings in the database are not valid; correct them with \"timevault-server settings\": %w", err)
	}
	cfg.EnvFile, cfg.EnvOverrides = boot.EnvFile, boot.EnvOverrides
	return cfg, stored, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogJSON {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func runMigrate() error {
	cfg, _, err := loadBootstrap()
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
	cfg, _, err := loadBootstrap()
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

// runCheckConfig says whether the settings of the environment and the .env file are valid for the service. It connects
// to nothing: a running service uses it to find out whether a new program would start with the settings it has, before
// it replaces itself with it. The Site settings are not checked here: they are checked when they are saved, and they
// are in the database.
func runCheckConfig() error {
	cfg, _, err := loadBootstrap()
	if err != nil {
		return err
	}
	if err := cfg.ValidateBootstrap(); err != nil {
		return err
	}
	fmt.Println("the settings are valid")
	return nil
}

// restart says that the service stopped to be started again: because it put a newer program in its place, or because the
// local administrator asked for a restart to apply the Site settings. runServe fills it in.
type restart struct {
	// update is set when a newer program is in place.
	update *update.Update
	// site is set when the program is the same and only has to read its Site settings again.
	site bool
	mode string
	log  *slog.Logger
}

// wanted reports whether the service has to be started again.
func (r restart) wanted() bool { return r.update != nil || r.site }

// serveAndRestart runs the service. When it stopped because it put a newer program in its place, or because it was
// asked to restart, it starts the program: it stays alive as a small launcher (the default, which works from a console
// window and from a scheduled task), or it exits with the code a service manager restarts a program on.
func serveAndRestart() error {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	var again restart
	if err := runServe(exe, &again); err != nil || !again.wanted() {
		return err
	}
	what := "the same program"
	if again.update != nil {
		what = "version " + again.update.To
	}
	if update.Decide(again.mode, update.Supervised()) == update.RestartExit {
		again.log.Info("exiting so that the program is started again", "program", what, "exit_code", update.ExitRestart)
		os.Exit(update.ExitRestart)
	}
	again.log.Info("starting the program again", "program", what)
	os.Exit(update.Supervise(exe, os.Args[1:], nil, again.log))
	return nil
}

// auditUpdate writes what the updater does to the audit log. The detail holds no path and no error text: the audit log
// is shown to everybody who signs in, and the details are in the log of the service and on the Settings page.
func auditUpdate(logger *audit.Logger) func(context.Context, update.Event) {
	return func(ctx context.Context, e update.Event) {
		result := audit.ResultOK
		if !e.OK {
			result = audit.ResultFailed
		}
		detail := map[string]any{"stage": e.Stage, "from": e.From, "to": e.To}
		for k, v := range e.Detail {
			detail[k] = v
		}
		_ = logger.Record(ctx, audit.Event{
			Actor: audit.ActorUpdater, Action: audit.ActionAppUpdate, TargetType: "app", TargetID: e.To, Result: result, Detail: detail,
		})
	}
}

// noteUpdateDone tells that this program has taken over from the one an update replaced. The marker is left by the
// service that did the update.
func noteUpdateDone(ctx context.Context, log *slog.Logger, logger *audit.Logger, m update.Marker) {
	if !update.SameVersion(m.To, version) {
		log.Warn("automatic update: the note left by the update names another version than the one that runs, so the program was changed since",
			"noted", m.To, "running", version)
		return
	}
	log.Info("automatic update: this program has taken over", "from", m.From, "to", version, "waited_seconds", m.WaitedSeconds)
	_ = logger.Record(ctx, audit.Event{
		Actor: audit.ActorUpdater, Action: audit.ActionAppUpdate, TargetType: "app", TargetID: version,
		Detail: map[string]any{"stage": "done", "from": m.From, "to": version, "sha256": m.SHA256},
	})
}

// warnIfNotWritable says so when the folder of the program cannot take a new file, which an update needs.
func warnIfNotWritable(log *slog.Logger, exe string) {
	f, err := os.CreateTemp(filepath.Dir(exe), ".timevault-write-test-*")
	if err != nil {
		log.Warn("automatic updates are on, but the folder of the program cannot take a new file, so an update will fail: give the account of the service the right to write there",
			"folder", filepath.Dir(exe), "error", err)
		return
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}

func runServe(exe string, again *restart) error {
	boot, base, err := loadBootstrap()
	if err != nil {
		return err
	}
	if err := boot.ValidateBootstrap(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The Site settings are in the database, so they are read before anything else is built.
	cfg, stored, err := loadSite(ctx, boot, base, newLogger(boot))
	if err != nil {
		return err
	}
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	if cfg.UpdateURL != "" && exe == "" {
		return errors.New("the update address (update_url) is set, but the location of the program is not known, so it could not be replaced")
	}
	log := newLogger(cfg)
	log.Info("starting TimeVault", append([]any{"version", version}, cfg.LogAttrs()...)...)
	if update.Supervised() {
		log.Info("started by the launcher of an earlier program, which starts the next one after an update", "launcher_pid", os.Getenv(update.EnvSupervised))
		go update.StopWithLauncher(os.Stdin, cancel, log)
	}
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
	if legacy := config.LegacySettings(base); len(legacy) > 0 {
		log.Warn("these settings are in the environment or the .env file, but nothing reads them any more: the Site settings are in the database now (enter them on the Site settings page), and the Everything settings are gone with the Everything search",
			"settings", strings.Join(legacy, ","), "env_file", cfg.EnvFile)
	}
	if cfg.AdminPasswordHash == "" && !cfg.AuthDisabled {
		log.Warn("there is no local administrator (TIMEVAULT_ADMIN_PASSWORD_HASH), and only that account may open the Site settings page: change the Site settings with \"timevault-server settings\" instead")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL, db.Options{MaxConns: poolSize(cfg.JobWorkers)})
	if err != nil {
		return err
	}
	defer pool.Close()
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
	// The NTFS index holds the monitored folders: it gives the Explorer its search and its folder sizes, and tells the
	// scheduler which folders did not change. Reading a volume needs administrator rights; without them the index says
	// so on the status page and the rest of the service works as before.
	var provider search.Provider = search.NewDisabled()
	var indexDone chan struct{}
	if cfg.SearchIndex == "auto" {
		index := ntfsindex.New(ntfsindex.Config{Roots: sourceRoots(cat), Log: log})
		provider = search.NewNTFS(index)
		indexDone = make(chan struct{})
		go func() {
			defer close(indexDone)
			index.Run(ctx)
		}()
	} else {
		log.Info("the NTFS index is off (the Site setting search_index): there is no search, no folder size and no skipping of folders that did not change")
	}
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
	// webLog is the writer of the shared web log, nil when the web log is off; the sink is nil then too, so that nothing
	// calls it. The audit trail feeds it with what people and the scheduler do, the server with the pages people open.
	var webLog *weblog.Writer
	var webSink weblog.Sink
	started := false
	if cfg.WebLog {
		conn, err := redisuser.Connect(cfg.RedisURL, cfg.RedisTimeout, 2)
		if err != nil {
			return fmt.Errorf("web log: %w", err)
		}
		defer conn.Close()
		minLevel := slog.LevelInfo
		if cfg.LogLevel <= slog.LevelDebug {
			minLevel = slog.LevelDebug
		}
		webLog = weblog.New(conn.Client, weblog.Options{
			Key: cfg.WebLogKey, Host: cfg.WebLogHost, MinLevel: minLevel, Timeout: cfg.RedisTimeout, Log: log, Redact: conn.Redact,
		})
		webSink = webLog
		log.Info("web log is on: lines are appended to a list in the Redis of the user directory", "key", cfg.WebLogKey, "host", webLog.Host(), "redis", conn.Addr)
		defer func() {
			if started {
				webLog.Write(weblog.Stopped())
			}
			flush, cancelFlush := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelFlush()
			if err := webLog.Close(flush); err != nil {
				log.Warn("web log: the last lines could not be written before the service stopped", "error", err)
			}
		}()
	}
	auditLog := audit.New(pool, log)
	if webLog != nil {
		auditLog.SetMirror(func(e audit.Event) { webLog.Write(weblog.Audit(e)) })
	}
	if exe != "" {
		if marker, ok := update.TakeMarker(exe); ok {
			noteUpdateDone(ctx, log, auditLog, marker)
		}
	}
	var updater *update.Updater
	var updates server.Updater
	if cfg.UpdateURL != "" {
		source, err := update.NewSource(cfg.UpdateURL, nil)
		if err != nil {
			return err
		}
		if updater, err = update.New(update.Options{
			Source: source, Drainer: runner, Current: version, ExePath: exe, Interval: cfg.UpdateInterval,
			Log: log, Record: auditUpdate(auditLog),
		}); err != nil {
			return err
		}
		updates = updater
		warnIfNotWritable(log, exe)
		log.Info("automatic updates are on", "manifest", source.Address(), "interval", cfg.UpdateInterval.String(), "restart", cfg.UpdateRestart)
	}
	serviceConfig := service.Config{
		Pool: pool, Catalog: cat, Repo: repo, Engine: engine, Restore: restoreMgr, Jobs: runner,
		Audit: auditLog, Search: provider, Logger: log, WebLog: webSink,
		ServerHost: cfg.ServerHost, RestoreDisplayRoot: cfg.RestoreDisplayRoot,
		Scheduler: service.SchedulerOptions{
			Parallel: cfg.SchedulerParallel, GapAuto: cfg.SchedulerGapAuto, Gap: cfg.SchedulerGap,
			RestAuto: cfg.SchedulerRestAuto, Rest: cfg.SchedulerRest,
			Probe: cfg.SchedulerProbe, FullScanEvery: cfg.SchedulerFullScanEvery,
		},
	}
	if directory != nil {
		serviceConfig.Directory = directory
	}
	svc := service.New(serviceConfig)

	// A restart that the local administrator asks for on the Site settings page: it is only done while no job runs.
	// Drain stops the runner from taking another job, so once it has returned the number of running jobs can only fall.
	restartSite := make(chan struct{}, 1)
	siteStore := sitesettings.New(pool)
	site := &server.SiteBackend{
		Base: base, Stored: siteStore.Load, Save: siteStore.Save, Running: config.SiteEffective(stored),
		Restart: func() error {
			runner.Drain()
			if runner.Running() > 0 {
				runner.Resume()
				return server.ErrJobsRunning
			}
			select {
			case restartSite <- struct{}{}:
			default:
			}
			return nil
		},
	}

	// Everything that can fail to build comes before the workers start, so that a failure leaves nothing running.
	handler, err := server.New(svc, am, web.FS, server.Options{
		Version: version, Location: time.Local, CookieSecure: cfg.CookieSecure, Deployment: cfg.Describe(),
		SSOHost: cfg.SSOHost, SSORequireSameIP: cfg.SSORequireSameIP, TrustedProxies: cfg.TrustedProxies, BasePath: cfg.BasePath,
		AvatarURL: cfg.AvatarURL, Update: updates, Site: site, WebLog: webSink,
	}, log)
	if err != nil {
		return err
	}
	if cfg.BasePath != "" {
		log.Info("serving under a path prefix", "base_path", cfg.BasePath,
			"note", "pages, redirects and cookies use the prefix; requests with or without it are accepted")
	}
	if cfg.SSOHost != "" && directory == nil {
		log.Warn("the Site setting sso_host is set but TIMEVAULT_REDIS_URL is not: single sign-on needs the user directory, so the login page offers none")
	}
	if cfg.SSOHost != "" && !cfg.SSORequireSameIP {
		log.Warn("single sign-on does not check that the browser has the address the sign-on service saw (the Site setting sso_require_same_ip is false): someone who can make a colleague open a sign-on link could sign in as that colleague; set trusted_proxies and turn the check on")
	}
	srv := &http.Server{
		Addr: cfg.ListenAddr, Handler: handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
	}

	started = true
	if webLog != nil {
		webLog.Write(weblog.Started(version))
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
	// updateDone stays nil, and so is never ready, when the service does not update itself.
	var updateDone chan *update.Update
	if updater != nil {
		updateDone = make(chan *update.Update, 1)
		go func() { updateDone <- updater.Run(ctx) }()
	}

	select {
	case err := <-serveErr:
		stop()
		scheduler.Wait()
		runner.Shutdown(context.Background())
		return err
	case up := <-updateDone:
		// The new program is in place and no job runs; the rest is the usual way out. A nil update means the
		// updater only ended because the service is being stopped.
		if up != nil {
			*again = restart{update: up, mode: cfg.UpdateRestart, log: log}
			log.Info("shutting down to start the new version", "from", up.From, "to", up.To)
			stop()
		}
	case <-restartSite:
		// No job runs, and none was taken since: the Site settings were saved and the local administrator asked for a restart.
		*again = restart{site: true, mode: cfg.UpdateRestart, log: log}
		log.Info("shutting down to start again with the Site settings in the database")
		stop()
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
	if indexDone != nil {
		select {
		case <-indexDone:
		case <-shutdownCtx.Done():
		}
	}
	log.Info("stopped")
	return nil
}

// sourceRoots lists the folders of the monitored folders, the switched-off ones too, for the NTFS index.
func sourceRoots(cat *catalog.Store) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		sources, err := cat.Sources(ctx)
		if err != nil {
			return nil, err
		}
		roots := make([]string, 0, len(sources))
		for _, s := range sources {
			roots = append(roots, s.RootPath)
		}
		return roots, nil
	}
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
