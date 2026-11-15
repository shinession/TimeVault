// Package config loads TimeVault's settings from environment variables.
//
// Every setting has the TIMEVAULT_ prefix. Secrets (database URL, password
// hash, session key, Everything credentials) are only ever read from the
// environment and are redacted when the configuration is logged.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"runtime"
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

	AuthDisabled      bool
	AdminUser         string
	AdminPasswordHash string
	SessionKey        []byte
	SessionTTL        time.Duration
	CookieSecure      bool

	EverythingURL      string
	EverythingUsername string
	EverythingPassword string
	EverythingTimeout  time.Duration

	JobWorkers  int
	HashWorkers int

	LogLevel slog.Level
	LogJSON  bool
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
		ListenAddr:         l.str("TIMEVAULT_LISTEN_ADDR", "127.0.0.1:8470"),
		AllowNonLoopback:   l.boolean("TIMEVAULT_ALLOW_NON_LOOPBACK", false),
		DatabaseURL:        l.str("TIMEVAULT_DATABASE_URL", ""),
		AutoMigrate:        l.boolean("TIMEVAULT_AUTO_MIGRATE", false),
		RepositoryPath:     l.str("TIMEVAULT_REPOSITORY_PATH", ""),
		RestoreRoot:        l.str("TIMEVAULT_RESTORE_ROOT", ""),
		AuthDisabled:       l.boolean("TIMEVAULT_AUTH_DISABLED", false),
		AdminUser:          l.str("TIMEVAULT_ADMIN_USER", "admin"),
		AdminPasswordHash:  l.str("TIMEVAULT_ADMIN_PASSWORD_HASH", ""),
		SessionTTL:         l.duration("TIMEVAULT_SESSION_TTL", 8*time.Hour),
		CookieSecure:       l.boolean("TIMEVAULT_COOKIE_SECURE", false),
		EverythingURL:      l.str("TIMEVAULT_EVERYTHING_URL", ""),
		EverythingUsername: l.str("TIMEVAULT_EVERYTHING_USERNAME", ""),
		EverythingPassword: l.str("TIMEVAULT_EVERYTHING_PASSWORD", ""),
		EverythingTimeout:  l.duration("TIMEVAULT_EVERYTHING_TIMEOUT", 5*time.Second),
		JobWorkers:         l.integer("TIMEVAULT_JOB_WORKERS", 2, 1, 16),
		HashWorkers:        l.integer("TIMEVAULT_HASH_WORKERS", min(4, runtime.NumCPU()), 1, 32),
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
	if err := c.validateListen(); err != nil {
		l.errs = append(l.errs, err)
	}
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
	if !c.AuthDisabled && c.AdminPasswordHash == "" {
		errs = append(errs, errors.New("TIMEVAULT_ADMIN_PASSWORD_HASH is required (create one with: timevault-server hash-password)"))
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
	return []any{
		"listen", c.ListenAddr,
		"database", redactURL(c.DatabaseURL),
		"auto_migrate", c.AutoMigrate,
		"repository", c.RepositoryPath,
		"restore_root", c.RestoreRoot,
		"auth_disabled", c.AuthDisabled,
		"admin_user", c.AdminUser,
		"session_ttl", c.SessionTTL.String(),
		"cookie_secure", c.CookieSecure,
		"everything_configured", c.EverythingURL != "",
		"job_workers", c.JobWorkers,
		"hash_workers", c.HashWorkers,
	}
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
