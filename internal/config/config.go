// Package config loads TimeVault's settings from environment variables, with a
// .env file as a fallback (see LoadEnv).
//
// Every setting has the TIMEVAULT_ prefix. Secrets (database URL, Redis URL,
// password hash, session key, Everything credentials) are only ever read from
// the environment or the .env file and are redacted when the configuration is
// logged or shown.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"timevault/internal/pathutil"
)

// Config is the validated runtime configuration.
type Config struct {
	ListenAddr       string
	AllowNonLoopback bool

	DatabaseURL string
	AutoMigrate bool

	RepositoryPath string
	RestoreRoot    string

	// ServerHost is the name of this server as users type it in \\server\share paths. The monitored folders live on
	// this server only, so display paths must use it. Empty means any host is accepted.
	ServerHost string
	// RestoreDisplayRoot is the network path users reach RestoreRoot by, in its normalised form
	// (\\server\share\folder). Empty when it is not set, or when there is no restore folder.
	RestoreDisplayRoot string

	AuthDisabled      bool
	AdminUser         string
	AdminPasswordHash string
	SessionKey        []byte
	SessionTTL        time.Duration
	CookieSecure      bool

	// The company accounts live in Redis as app_user_* hashes (see internal/redisuser).
	RedisURL       string
	RedisKeyPrefix string
	RedisTimeout   time.Duration

	EverythingURL      string
	EverythingUsername string
	EverythingPassword string
	EverythingTimeout  time.Duration

	JobWorkers  int
	HashWorkers int

	// SSOHost is where the single sign-on service lives, as scheme://host[:port] with no path (TIMEVAULT_SSO_HOST).
	// Empty means the login page offers no single sign-on.
	SSOHost string
	// SSORequireSameIP lets a single sign-on succeed only when the browser that asked for it has the address the
	// sign-on service saw (TIMEVAULT_SSO_REQUIRE_SAME_IP).
	SSORequireSameIP bool
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header is believed (TIMEVAULT_TRUSTED_PROXIES).
	TrustedProxies []netip.Prefix
	// BasePath is the path prefix the site is served under behind a reverse proxy, such as /tv
	// (TIMEVAULT_BASE_PATH). Empty means the site is at the root of its address.
	BasePath string
	// AvatarURL is where the picture of an account is, as a path on the host that serves the site, with {id} where
	// the account name goes (TIMEVAULT_AVATAR_URL). Empty means there are no pictures.
	AvatarURL string

	// UpdateURL is where the release manifest (timevault.json) is: an http(s) address, or the absolute path of the
	// file or of its folder (TIMEVAULT_UPDATE_URL). Empty means the service does not update itself.
	UpdateURL string
	// UpdateInterval is the time between two looks at the manifest (TIMEVAULT_UPDATE_INTERVAL).
	UpdateInterval time.Duration
	// UpdateRestart is how the service starts the new program: "supervise" keeps the process as a launcher, "exit"
	// ends it with the code 75 for a service manager (TIMEVAULT_UPDATE_RESTART).
	UpdateRestart string

	// SchedulerParallel is how many automatic backups may run at the same time.
	SchedulerParallel int
	// SchedulerGapAuto derives the time between two automatic backups from the schedules; otherwise
	// SchedulerGap is that time.
	SchedulerGapAuto bool
	SchedulerGap     time.Duration
	// SchedulerRestAuto makes the pause after an automatic backup as long as the backup took; otherwise
	// SchedulerRest is that pause.
	SchedulerRestAuto bool
	SchedulerRest     time.Duration
	// SchedulerEverythingProbe lets the scheduler ask Everything whether a folder changed before it scans it.
	SchedulerEverythingProbe bool
	// SchedulerFullScanEvery is the longest a folder goes without a complete scan when the probe is on.
	SchedulerFullScanEvery time.Duration
	// ProcessPriority is normal, below_normal or background (TIMEVAULT_PROCESS_PRIORITY).
	ProcessPriority string

	LogLevel slog.Level
	LogJSON  bool

	// EnvFile is the .env file the settings were read from, if any. It is set by the caller of LoadEnv.
	EnvFile string
	// EnvOverrides names the settings of EnvFile that the process environment overrides (see EnvOverrides).
	// It is set by the caller of LoadEnv.
	EnvOverrides []string
}

// Lookup resolves one environment variable.
type Lookup func(key string) (string, bool)

// OSLookup reads the process environment.
func OSLookup(key string) (string, bool) { return os.LookupEnv(key) }

// Load reads and validates the settings that are valid for every command.
// Use ValidateServe for the extra requirements of the long-running server.
func Load(lookup Lookup) (Config, error) {
	l := loader{lookup: lookup}
	c := Config{
		ListenAddr:         l.str("TIMEVAULT_LISTEN_ADDR", "127.0.0.1:8660"),
		AllowNonLoopback:   l.boolean("TIMEVAULT_ALLOW_NON_LOOPBACK", false),
		DatabaseURL:        l.str("TIMEVAULT_DATABASE_URL", ""),
		AutoMigrate:        l.boolean("TIMEVAULT_AUTO_MIGRATE", false),
		RepositoryPath:     l.str("TIMEVAULT_REPOSITORY_PATH", ""),
		RestoreRoot:        l.str("TIMEVAULT_RESTORE_ROOT", ""),
		ServerHost:         l.str("TIMEVAULT_SERVER_HOST", ""),
		RestoreDisplayRoot: l.str("TIMEVAULT_RESTORE_DISPLAY_ROOT", ""),
		AuthDisabled:       l.boolean("TIMEVAULT_AUTH_DISABLED", false),
		AdminUser:          l.str("TIMEVAULT_ADMIN_USER", "admin"),
		AdminPasswordHash:  l.str("TIMEVAULT_ADMIN_PASSWORD_HASH", ""),
		SessionTTL:         l.duration("TIMEVAULT_SESSION_TTL", 8*time.Hour),
		CookieSecure:       l.boolean("TIMEVAULT_COOKIE_SECURE", false),
		RedisURL:           l.str("TIMEVAULT_REDIS_URL", ""),
		RedisKeyPrefix:     l.str("TIMEVAULT_REDIS_KEY_PREFIX", "app_user_"),
		RedisTimeout:       l.duration("TIMEVAULT_REDIS_TIMEOUT", 3*time.Second),
		EverythingURL:      l.str("TIMEVAULT_EVERYTHING_URL", ""),
		EverythingUsername: l.str("TIMEVAULT_EVERYTHING_USERNAME", ""),
		EverythingPassword: l.str("TIMEVAULT_EVERYTHING_PASSWORD", ""),
		EverythingTimeout:  l.duration("TIMEVAULT_EVERYTHING_TIMEOUT", 5*time.Second),
		JobWorkers:         l.integer("TIMEVAULT_JOB_WORKERS", 2, 1, 16),
		HashWorkers:        l.integer("TIMEVAULT_HASH_WORKERS", min(4, runtime.NumCPU()), 1, 32),

		SSORequireSameIP:         l.boolean("TIMEVAULT_SSO_REQUIRE_SAME_IP", true),
		SchedulerParallel:        l.integer("TIMEVAULT_SCHEDULER_PARALLEL", 1, 1, 4),
		SchedulerEverythingProbe: l.boolean("TIMEVAULT_SCHEDULER_EVERYTHING_PROBE", false),
		SchedulerFullScanEvery:   l.duration("TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY", 24*time.Hour),
		UpdateInterval:           l.duration("TIMEVAULT_UPDATE_INTERVAL", 10*time.Minute),
	}
	var err error
	if c.SSOHost, err = parseSSOHost(l.str("TIMEVAULT_SSO_HOST", "")); err != nil {
		l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_SSO_HOST: %w", err))
	}
	if c.TrustedProxies, err = parseProxies(l.str("TIMEVAULT_TRUSTED_PROXIES", "")); err != nil {
		l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_TRUSTED_PROXIES: %w", err))
	}
	if c.BasePath, err = ParseBasePath(l.str("TIMEVAULT_BASE_PATH", "")); err != nil {
		l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_BASE_PATH %w", err))
	}
	if c.AvatarURL, err = parseAvatarURL(l.str("TIMEVAULT_AVATAR_URL", DefaultAvatarURL)); err != nil {
		l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_AVATAR_URL %w", err))
	}
	if c.UpdateURL, err = parseUpdateURL(l.str("TIMEVAULT_UPDATE_URL", "")); err != nil {
		l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_UPDATE_URL %w", err))
	}
	switch m := strings.ToLower(l.str("TIMEVAULT_UPDATE_RESTART", "supervise")); m {
	case "supervise", "exit":
		c.UpdateRestart = m
	default:
		l.errs = append(l.errs, errors.New("TIMEVAULT_UPDATE_RESTART must be supervise or exit"))
	}
	if c.UpdateInterval < time.Minute || c.UpdateInterval > 24*time.Hour {
		l.errs = append(l.errs, errors.New("TIMEVAULT_UPDATE_INTERVAL must be between 1m and 24h"))
	}
	if c.SchedulerGap, c.SchedulerGapAuto, err = autoOrDuration(l.str("TIMEVAULT_SCHEDULER_GAP", "auto")); err != nil {
		l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_SCHEDULER_GAP %w", err))
	}
	if c.SchedulerRest, c.SchedulerRestAuto, err = autoOrDuration(l.str("TIMEVAULT_SCHEDULER_REST", "auto")); err != nil {
		l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_SCHEDULER_REST %w", err))
	}
	switch p := strings.ToLower(l.str("TIMEVAULT_PROCESS_PRIORITY", "below_normal")); p {
	case "normal", "below_normal", "background":
		c.ProcessPriority = p
	default:
		l.errs = append(l.errs, errors.New("TIMEVAULT_PROCESS_PRIORITY must be normal, below_normal or background"))
	}
	if c.SchedulerFullScanEvery < time.Hour || c.SchedulerFullScanEvery > 30*24*time.Hour {
		l.errs = append(l.errs, errors.New("TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY must be between 1h and 720h"))
	}
	if key := l.str("TIMEVAULT_SESSION_KEY", ""); key != "" {
		decoded, err := decodeKey(key)
		if err != nil {
			l.errs = append(l.errs, fmt.Errorf("TIMEVAULT_SESSION_KEY: %w", err))
		}
		c.SessionKey = decoded
	}
	switch strings.ToLower(l.str("TIMEVAULT_LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		l.errs = append(l.errs, errors.New("TIMEVAULT_LOG_LEVEL must be debug, info, warn or error"))
	}
	switch strings.ToLower(l.str("TIMEVAULT_LOG_FORMAT", "text")) {
	case "text":
	case "json":
		c.LogJSON = true
	default:
		l.errs = append(l.errs, errors.New("TIMEVAULT_LOG_FORMAT must be text or json"))
	}

	if c.SessionTTL < 5*time.Minute || c.SessionTTL > 30*24*time.Hour {
		l.errs = append(l.errs, errors.New("TIMEVAULT_SESSION_TTL must be between 5m and 720h"))
	}
	if c.EverythingTimeout < 100*time.Millisecond || c.EverythingTimeout > 30*time.Second {
		l.errs = append(l.errs, errors.New("TIMEVAULT_EVERYTHING_TIMEOUT must be between 100ms and 30s"))
	}
	if c.RedisTimeout < 100*time.Millisecond || c.RedisTimeout > 30*time.Second {
		l.errs = append(l.errs, errors.New("TIMEVAULT_REDIS_TIMEOUT must be between 100ms and 30s"))
	}
	if c.RedisURL != "" {
		if u, err := url.Parse(c.RedisURL); err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
			l.errs = append(l.errs, errors.New("TIMEVAULT_REDIS_URL must look like redis://[:password@]host:port/db (or rediss:// for TLS)"))
		}
	}
	if err := c.validateListen(); err != nil {
		l.errs = append(l.errs, err)
	}
	l.errs = append(l.errs, c.normalizeDisplay()...)
	if err := errors.Join(l.errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return c, nil
}

// ValidateServe checks the settings the long-running server cannot start without.
func (c Config) ValidateServe() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("TIMEVAULT_DATABASE_URL is required"))
	}
	if c.RepositoryPath == "" {
		errs = append(errs, errors.New("TIMEVAULT_REPOSITORY_PATH is required"))
	}
	if !c.AuthDisabled && c.AdminPasswordHash == "" && c.RedisURL == "" {
		errs = append(errs, errors.New("no way to sign in: set TIMEVAULT_REDIS_URL (company accounts) and/or TIMEVAULT_ADMIN_PASSWORD_HASH (local administrator, create one with: timevault-server hash-password)"))
	}
	if c.UpdateURL != "" && !c.AutoMigrate {
		errs = append(errs, errors.New("TIMEVAULT_UPDATE_URL needs TIMEVAULT_AUTO_MIGRATE=true: a new version may change the database, and nobody runs the migrate command when the service updates itself"))
	}
	if c.RepositoryPath != "" && c.RestoreRoot != "" {
		if err := checkApart(c.RepositoryPath, c.RestoreRoot); err != nil {
			errs = append(errs, fmt.Errorf("TIMEVAULT_RESTORE_ROOT: %w", err))
		}
	}
	return errors.Join(errs...)
}

// ValidateDatabase checks the settings the migrate command needs.
func (c Config) ValidateDatabase() error {
	if c.DatabaseURL == "" {
		return errors.New("TIMEVAULT_DATABASE_URL is required")
	}
	return nil
}

func checkApart(repository, restoreRoot string) error {
	if pathutil.Overlaps(pathutil.RealOrClean(repository), pathutil.RealOrClean(restoreRoot)) {
		return errors.New("must not be inside or contain the repository")
	}
	return nil
}

// normalizeDisplay validates the settings that tell users how to reach the folders of this server and stores
// the restore display root in its canonical form. The errors name the settings and say what is wrong, but never
// repeat the values.
func (c *Config) normalizeDisplay() []error {
	var errs []error
	if c.ServerHost != "" {
		if err := pathutil.ValidateHost(c.ServerHost); err != nil {
			errs = append(errs, fmt.Errorf("TIMEVAULT_SERVER_HOST: %w", err))
		}
	}
	switch {
	case c.RestoreRoot == "":
		// Nothing is restored, so there is nothing to display.
		c.RestoreDisplayRoot = ""
	case c.RestoreDisplayRoot != "":
		u, err := pathutil.ParseUNC(c.RestoreDisplayRoot)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("TIMEVAULT_RESTORE_DISPLAY_ROOT: %w", err))
		case c.ServerHost != "" && !strings.EqualFold(u.Host, c.ServerHost):
			errs = append(errs, errors.New("TIMEVAULT_RESTORE_DISPLAY_ROOT must be on the server named in TIMEVAULT_SERVER_HOST"))
		default:
			c.RestoreDisplayRoot = u.String()
		}
	}
	return errs
}

func (c Config) validateListen() error {
	host, port, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("TIMEVAULT_LISTEN_ADDR %q must look like host:port", c.ListenAddr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("TIMEVAULT_LISTEN_ADDR has an invalid port %q", port)
	}
	if c.IsLoopbackListen(host) {
		return nil
	}
	if c.AuthDisabled {
		return errors.New("TIMEVAULT_AUTH_DISABLED is only allowed when listening on a loopback address")
	}
	if !c.AllowNonLoopback {
		return fmt.Errorf("TIMEVAULT_LISTEN_ADDR %q is not a loopback address; set TIMEVAULT_ALLOW_NON_LOOPBACK=true to confirm that a TLS reverse proxy protects it", c.ListenAddr)
	}
	return nil
}

// IsLoopbackListen reports whether host only accepts local connections.
func (c Config) IsLoopbackListen(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LogAttrs describes the configuration for the startup log without secrets.
func (c Config) LogAttrs() []any {
	d := c.Describe()
	return []any{
		"listen", c.ListenAddr,
		"database", redactURL(c.DatabaseURL),
		"user_directory", d.UserDirectory,
		"env_file", c.EnvFile,
		"auto_migrate", c.AutoMigrate,
		"repository", c.RepositoryPath,
		"restore_root", c.RestoreRoot,
		"server_host", c.ServerHost,
		"restore_display_root", c.RestoreDisplayRoot,
		"auth_disabled", c.AuthDisabled,
		"local_admin", d.LocalAdmin,
		"session_ttl", c.SessionTTL.String(),
		"cookie_secure", c.CookieSecure,
		"everything_configured", c.EverythingURL != "",
		"job_workers", c.JobWorkers,
		"hash_workers", c.HashWorkers,
		"sso_host", c.SSOHost,
		"sso_require_same_ip", c.SSORequireSameIP,
		"trusted_proxies", len(c.TrustedProxies),
		"base_path", c.BasePath,
		"avatar_url", c.AvatarURL,
		"update_url", c.UpdateURL,
		"update_interval", c.UpdateInterval.String(),
		"update_restart", c.UpdateRestart,
		"scheduler_parallel", c.SchedulerParallel,
		"scheduler_gap", autoText(c.SchedulerGapAuto, c.SchedulerGap),
		"scheduler_rest", autoText(c.SchedulerRestAuto, c.SchedulerRest),
		"scheduler_everything_probe", c.SchedulerEverythingProbe,
		"process_priority", c.ProcessPriority,
	}
}

func autoText(auto bool, d time.Duration) string {
	if auto {
		return "auto"
	}
	return d.String()
}

// Deployment is what the Settings page shows about the running configuration. It holds no secret.
type Deployment struct {
	ListenAddr         string
	Database           string // host:port/name
	UserDirectory      string // host:port/db of Redis, empty when company accounts are not configured
	RepositoryPath     string
	RestoreRoot        string
	ServerHost         string // name of this server in \\server\share paths, empty when not set
	RestoreDisplayRoot string // network path of the restore folder as users see it, empty when not set
	EnvFile            string
	SearchHost         string // host:port of the Everything server, empty when search is off
	SSOHost            string // address of the single sign-on service, empty when single sign-on is off
	BasePath           string // path prefix the site is served under behind a reverse proxy, empty when there is none
	AvatarURL          string // where the picture of an account is, {id} for the account name, empty when there are no pictures
	UpdateURL          string // address of the release manifest, empty when the service does not update itself
	UpdateInterval     time.Duration
	UpdateRestart      string // supervise or exit
	LocalAdmin         string // name of the local administrator, empty when there is none
	AuthDisabled       bool
	SessionTTL         time.Duration
	CookieSecure       bool
}

// Describe summarises the configuration for display.
func (c Config) Describe() Deployment {
	d := Deployment{
		ListenAddr:         c.ListenAddr,
		Database:           describeURL(c.DatabaseURL, "postgres", "postgresql"),
		UserDirectory:      describeURL(c.RedisURL, "redis", "rediss"),
		RepositoryPath:     c.RepositoryPath,
		RestoreRoot:        c.RestoreRoot,
		ServerHost:         c.ServerHost,
		RestoreDisplayRoot: c.RestoreDisplayRoot,
		EnvFile:            c.EnvFile,
		SSOHost:            c.SSOHost,
		BasePath:           c.BasePath,
		AuthDisabled:       c.AuthDisabled,
		AvatarURL:          c.AvatarURL,
		UpdateURL:          c.UpdateURL,
		UpdateInterval:     c.UpdateInterval,
		UpdateRestart:      c.UpdateRestart,
		SessionTTL:         c.SessionTTL,
		CookieSecure:       c.CookieSecure,
	}
	if c.AdminPasswordHash != "" {
		d.LocalAdmin = c.AdminUser
	}
	if u, err := url.Parse(c.EverythingURL); err == nil && c.EverythingURL != "" {
		d.SearchHost = u.Host
	}
	return d
}

// describeURL names the server and database of a connection URL for display,
// without the user, the password or any setting.
func describeURL(raw string, schemes ...string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || !slices.Contains(schemes, u.Scheme) {
		return "(not shown)"
	}
	out := u.Host
	if db := strings.Trim(u.Path, "/"); db != "" {
		out += "/" + db
	}
	if u.Scheme == "rediss" {
		out += " (tls)"
	}
	return out
}

// redactURL describes a PostgreSQL URL for the log without anything that could
// be a secret: the password, wherever it is written (user info or a query
// setting), is never echoed. Only the host, database and a few harmless
// settings are kept; anything that is not a URL is not echoed at all.
func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "(not shown)"
	}
	out := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	if u.User != nil {
		out.User = url.User(u.User.Username())
	}
	q := url.Values{}
	for _, key := range []string{"sslmode", "search_path", "application_name", "connect_timeout"} {
		if v := u.Query().Get(key); v != "" {
			q.Set(key, v)
		}
	}
	out.RawQuery = q.Encode()
	return out.String()
}

func decodeKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) < 32 {
				return nil, errors.New("must decode to at least 32 bytes")
			}
			return b, nil
		}
	}
	return nil, errors.New("must be base64")
}

// autoOrDuration reads a setting that is either "auto" or a duration from 0s to 6h.
func autoOrDuration(raw string) (d time.Duration, auto bool, err error) {
	if strings.EqualFold(raw, "auto") {
		return 0, true, nil
	}
	d, perr := time.ParseDuration(raw)
	if perr != nil || d < 0 || d > 6*time.Hour {
		return 0, false, errors.New("must be auto or a duration between 0s and 6h")
	}
	return d, false, nil
}

// parseSSOHost normalises the address of the single sign-on service: host, host:port or
// scheme://host[:port] becomes scheme://host[:port], with http when no scheme is given, because
// that is how the company sign-on service is reached. Empty stays empty.
func parseSSOHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return "", errors.New("not a valid address; write it as host, host:port or http://host:port")
	case u.Scheme != "http" && u.Scheme != "https":
		return "", errors.New("the scheme must be http or https")
	case u.Hostname() == "":
		return "", errors.New("the host is missing")
	case u.User != nil:
		return "", errors.New("must not contain a user name or password")
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "":
		return "", errors.New("must be only a host and an optional port, without a path")
	}
	host := u.Hostname()
	if net.ParseIP(host) == nil {
		// Windows host names may contain an underscore, which a strict host name check would refuse.
		for _, r := range host {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
				return "", errors.New("the host contains a character that cannot be part of a host name")
			}
		}
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", errors.New("the port is out of range")
		}
	}
	return u.Scheme + "://" + u.Host, nil
}

// DefaultAvatarURL is where the pictures of the company accounts are: the application that serves the static files of
// the other company applications, reached through the same host as this site.
const DefaultAvatarURL = "/api/static/hr/{id}.webp"

// parseAvatarURL checks the address of the picture of an account: a path on the host that serves the site, with {id}
// where the account name goes. "off" (or "none") means there are no pictures and gives "". The error text starts with
// a verb, so a caller can put a setting name in front of it.
func parseAvatarURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "off") || strings.EqualFold(raw, "none") {
		return "", nil
	}
	switch {
	case !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, `\`):
		return "", errors.New("must be a path on the host of this site, such as /api/static/hr/{id}.webp, or off")
	case strings.Count(raw, "{id}") != 1:
		return "", errors.New("must contain {id} once, where the account name goes")
	case len(raw) > 256:
		return "", errors.New("is too long (at most 256 characters)")
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f || r == '"' || r == '\'' || r == '<' || r == '>' || r == '#' {
			return "", errors.New("must not contain spaces, quotes, # or other characters that cannot be part of an address")
		}
	}
	if u, err := url.Parse(raw); err != nil || u.Scheme != "" || u.Host != "" {
		return "", errors.New("is not a valid path")
	}
	return raw, nil
}

// parseUpdateURL checks the address of the release manifest: an http(s) address without a user name, a password, a
// query or a fragment, or the absolute path of the manifest or of the folder it is in. Empty stays empty. The error
// text starts with a verb, so a caller can put a setting name in front of it, and never repeats the value.
func parseUpdateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if l := strings.ToLower(raw); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		u, err := url.Parse(raw)
		switch {
		case err != nil || u.Hostname() == "":
			return "", errors.New("is not a valid address; write it as https://host/path/")
		case u.User != nil:
			return "", errors.New("must not contain a user name or password")
		case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
			return "", errors.New("must not have a query or a fragment")
		}
		return raw, nil
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New(`must be an http(s) address or an absolute path, such as D:\TimeVault\release or \\server\share\release`)
	}
	return raw, nil
}

// reservedBasePaths are the first path segments of the pages and endpoints the application serves itself. A request is
// only taken to carry the base path when it starts with it, so a base path that begins with one of these would make
// the application's own addresses ambiguous.
var reservedBasePaths = []string{"static", "api", "login", "logout", "auth", "lang", "explorer", "history", "versions",
	"backups", "jobs", "restore", "audit", "status", "settings", "healthz"}

// ParseBasePath normalises the path prefix the site is served under: "tv", "/tv" and "/tv/" all become "/tv", while ""
// and "/" become "" (no prefix). Every segment may only hold letters, digits and - _ . ~, so the prefix is the same
// in a URL, in a cookie path and in a proxy configuration. The error text starts with a verb, so a caller can put a
// setting name in front of it.
func ParseBasePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return "", nil
	}
	segments := strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "/"), "/"), "/")
	for _, seg := range segments {
		switch {
		case seg == "":
			return "", errors.New("must not have an empty segment (two slashes in a row)")
		case seg == "." || seg == "..":
			return "", errors.New("must not have a . or .. segment")
		}
		for _, r := range seg {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '~') {
				return "", errors.New("may only have letters, digits and - _ . ~ between the slashes, for example /tv")
			}
		}
	}
	if slices.Contains(reservedBasePaths, segments[0]) {
		return "", fmt.Errorf("must not start with /%s, which the application serves itself", segments[0])
	}
	p := "/" + strings.Join(segments, "/")
	if len(p) > 128 {
		return "", errors.New("is too long (at most 128 characters)")
	}
	return p, nil
}

// parseProxies reads a comma or space separated list of addresses and CIDR ranges.
func parseProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' }) {
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or a CIDR range such as 10.0.0.0/8", item)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// IsTrustedProxy reports whether addr is one of the configured reverse proxies.
func (c Config) IsTrustedProxy(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range c.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

type loader struct {
	lookup Lookup
	errs   []error
}

func (l *loader) str(key, def string) string {
	if v, ok := l.lookup(key); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return def
}

func (l *loader) boolean(key string, def bool) bool {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s must be true or false", key))
		return def
	}
	return v
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s must be a duration such as 30s or 8h", key))
		return def
	}
	return v
}

func (l *loader) integer(key string, def, lo, hi int) int {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < lo || v > hi {
		l.errs = append(l.errs, fmt.Errorf("%s must be an integer between %d and %d", key, lo, hi))
		return def
	}
	return v
}
