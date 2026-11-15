package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func lookupFrom(env map[string]string) Lookup {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(lookupFrom(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != "127.0.0.1:8660" || c.AdminUser != "admin" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.SessionTTL != 8*time.Hour || c.JobWorkers != 2 || c.HashWorkers < 1 {
		t.Errorf("unexpected numeric defaults: %+v", c)
	}
	if c.RedisURL != "" || c.RedisKeyPrefix != "app_user_" || c.RedisTimeout != 3*time.Second {
		t.Errorf("unexpected Redis defaults: %+v", c)
	}
	if c.AutoMigrate || c.AuthDisabled || c.CookieSecure {
		t.Errorf("risky options must default to off: %+v", c)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"bad listen":          {"TIMEVAULT_LISTEN_ADDR": "not-an-address"},
		"bad port":            {"TIMEVAULT_LISTEN_ADDR": "127.0.0.1:99999"},
		"all interfaces":      {"TIMEVAULT_LISTEN_ADDR": ":8660"},
		"public address":      {"TIMEVAULT_LISTEN_ADDR": "0.0.0.0:8660"},
		"auth off and public": {"TIMEVAULT_LISTEN_ADDR": "10.0.0.5:8660", "TIMEVAULT_ALLOW_NON_LOOPBACK": "true", "TIMEVAULT_AUTH_DISABLED": "true"},
		"bad bool":            {"TIMEVAULT_AUTO_MIGRATE": "maybe"},
		"bad duration":        {"TIMEVAULT_SESSION_TTL": "soon"},
		"short ttl":           {"TIMEVAULT_SESSION_TTL": "1s"},
		"bad workers":         {"TIMEVAULT_JOB_WORKERS": "0"},
		"short key":           {"TIMEVAULT_SESSION_KEY": "c2hvcnQ="},
		"non base64 key":      {"TIMEVAULT_SESSION_KEY": "!!!"},
		"bad level":           {"TIMEVAULT_LOG_LEVEL": "loud"},
		"bad format":          {"TIMEVAULT_LOG_FORMAT": "xml"},
		"everything timeout":  {"TIMEVAULT_EVERYTHING_TIMEOUT": "5m"},
		"redis wrong scheme":  {"TIMEVAULT_REDIS_URL": "http://127.0.0.1:6379/0"},
		"redis without host":  {"TIMEVAULT_REDIS_URL": "redis:///0"},
		"redis timeout":       {"TIMEVAULT_REDIS_TIMEOUT": "5m"},
	}
	for name, env := range cases {
		if _, err := Load(lookupFrom(env)); err == nil {
			t.Errorf("%s: Load succeeded, want error", name)
		}
	}
}

func TestLoadAllowsExplicitNonLoopback(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_LISTEN_ADDR":        "10.0.0.5:8660",
		"TIMEVAULT_ALLOW_NON_LOOPBACK": "true",
	}))
	if err != nil {
		t.Fatalf("explicit non-loopback should be allowed: %v", err)
	}
	for _, addr := range []string{"localhost:1", "[::1]:8660", "127.0.0.2:9"} {
		if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_LISTEN_ADDR": addr})); err != nil {
			t.Errorf("%s should be accepted: %v", addr, err)
		}
	}
}

func TestValidateServe(t *testing.T) {
	c, _ := Load(lookupFrom(nil))
	err := c.ValidateServe()
	if err == nil {
		t.Fatal("empty configuration must not be servable")
	}
	for _, want := range []string{"DATABASE_URL", "REPOSITORY_PATH", "REDIS_URL", "ADMIN_PASSWORD_HASH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %s", err, want)
		}
	}

	c.DatabaseURL = "postgres://u:p@127.0.0.1/db"
	c.RepositoryPath = t.TempDir()
	if err := c.ValidateServe(); err == nil || !strings.Contains(err.Error(), "no way to sign in") {
		t.Fatalf("a service nobody can sign in to must be rejected, got %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"local administrator": func(c *Config) { c.AdminPasswordHash = "pbkdf2-sha256$1$AA$AA" },
		"company accounts":    func(c *Config) { c.RedisURL = "redis://:p@127.0.0.1:6379/0" },
		"sign-in disabled":    func(c *Config) { c.AuthDisabled = true },
	} {
		cc := c
		mutate(&cc)
		if err := cc.ValidateServe(); err != nil {
			t.Fatalf("%s: valid configuration rejected: %v", name, err)
		}
	}
	c.AdminPasswordHash = "pbkdf2-sha256$1$AA$AA"
	c.RestoreRoot = c.RepositoryPath
	if err := c.ValidateServe(); err == nil {
		t.Fatal("restore root equal to the repository must be rejected")
	}
}

func TestSessionKeyAndRedaction(t *testing.T) {
	key := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	c, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_SESSION_KEY":         key,
		"TIMEVAULT_DATABASE_URL":        "postgres://tv:supersecret@127.0.0.1:5432/tv",
		"TIMEVAULT_REDIS_URL":           "rediss://dash:redis-secret@10.1.2.3:6379/2",
		"TIMEVAULT_EVERYTHING_URL":      "http://127.0.0.1:38480/",
		"TIMEVAULT_EVERYTHING_PASSWORD": "everything-secret",
		"TIMEVAULT_ADMIN_PASSWORD_HASH": "hash-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.SessionKey) != 32 {
		t.Fatalf("session key has %d bytes", len(c.SessionKey))
	}
	shown := fmt.Sprintf("%v %+v", c.LogAttrs(), c.Describe())
	for _, secret := range []string{"supersecret", "redis-secret", "everything-secret", "hash-secret", key, "dash:"} {
		if strings.Contains(shown, secret) {
			t.Errorf("the log or the Settings page would leak %q: %s", secret, shown)
		}
	}
	d := c.Describe()
	if d.Database != "127.0.0.1:5432/tv" || d.UserDirectory != "10.1.2.3:6379/2 (tls)" || d.SearchHost != "127.0.0.1:38480" || d.LocalAdmin != "admin" {
		t.Errorf("Describe = %+v", d)
	}
	if c, _ := Load(lookupFrom(nil)); c.Describe().LocalAdmin != "" || c.Describe().UserDirectory != "" {
		t.Error("nothing configured must describe nothing")
	}
}

// URL.Redacted only hides the password in the user info; a password given as a
// query setting, or in the key/value form pgx also accepts, must not reach the log either.
func TestDatabasePasswordNeverReachesTheLog(t *testing.T) {
	cases := map[string]string{
		"password in user info":     "postgres://tv:hunter2@127.0.0.1:5432/tv?sslmode=disable",
		"password in the query":     "postgres://tv@127.0.0.1:5432/tv?password=hunter2&sslmode=disable",
		"password after the others": "postgres://127.0.0.1:5432/tv?sslmode=disable&user=tv&password=hunter2",
		"key/value form":            "host=127.0.0.1 port=5432 user=tv password=hunter2 dbname=tv",
		"unparseable":               "postgres://tv:hunter2@127.0.0.1:99999999999/%zz",
	}
	for name, dsn := range cases {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_DATABASE_URL": dsn}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		logged := ""
		for _, v := range c.LogAttrs() {
			if s, ok := v.(string); ok {
				logged += s + " "
			}
		}
		if strings.Contains(logged, "hunter2") {
			t.Errorf("%s: the password reached the log: %s", name, logged)
		}
	}
	// What is useful to an operator is still there.
	c, _ := Load(lookupFrom(map[string]string{"TIMEVAULT_DATABASE_URL": "postgres://tv:hunter2@db.example:5432/tv?sslmode=require"}))
	if got := redactURL(c.DatabaseURL); got != "postgres://tv@db.example:5432/tv?sslmode=require" {
		t.Fatalf("redactURL = %q", got)
	}
}
