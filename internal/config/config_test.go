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
		"search index":        {"TIMEVAULT_SEARCH_INDEX": "always"},
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
		"TIMEVAULT_ADMIN_PASSWORD_HASH": "hash-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.SessionKey) != 32 {
		t.Fatalf("session key has %d bytes", len(c.SessionKey))
	}
	shown := fmt.Sprintf("%v %+v", c.LogAttrs(), c.Describe())
	for _, secret := range []string{"supersecret", "redis-secret", "hash-secret", key, "dash:"} {
		if strings.Contains(shown, secret) {
			t.Errorf("the log or the Settings page would leak %q: %s", secret, shown)
		}
	}
	d := c.Describe()
	if d.Database != "127.0.0.1:5432/tv" || d.UserDirectory != "10.1.2.3:6379/2 (tls)" || d.SearchIndex != "auto" || d.LocalAdmin != "admin" {
		t.Errorf("Describe = %+v", d)
	}
	if c, _ := Load(lookupFrom(nil)); c.Describe().LocalAdmin != "" || c.Describe().UserDirectory != "" {
		t.Error("nothing configured must describe nothing")
	}
}

func TestDescribeReportsThatSignInIsSwitchedOff(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_AUTH_DISABLED": "true"}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Describe().AuthDisabled {
		t.Error("Describe() does not say that sign-in is switched off")
	}
	if empty, _ := Load(lookupFrom(nil)); empty.Describe().AuthDisabled {
		t.Error("sign-in must be on by default")
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

func TestServerHost(t *testing.T) {
	load := func(host string) (Config, error) {
		return Load(lookupFrom(map[string]string{"TIMEVAULT_SERVER_HOST": host}))
	}
	if c, err := Load(lookupFrom(nil)); err != nil || c.ServerHost != "" {
		t.Fatalf("the server host must default to empty: %q, %v", c.ServerHost, err)
	}
	for in, want := range map[string]string{
		"sgmeslt595.co.murata.local":    "sgmeslt595.co.murata.local",
		"  SGMESLT595.co.murata.local ": "SGMESLT595.co.murata.local", // trimmed, the spelling is kept
		"srv_01-a":                      "srv_01-a",
		"10.0.0.5":                      "10.0.0.5",
		"   ":                           "",
	} {
		c, err := load(in)
		if err != nil || c.ServerHost != want {
			t.Errorf("TIMEVAULT_SERVER_HOST=%q: got %q, %v; want %q", in, c.ServerHost, err, want)
		}
	}
	for _, bad := range []string{
		"srv:445", `\\srv`, `srv\share`, "srv/share", "https://srv", "my srv", "srv name", ".srv", "srv.", "a..b", "srv@x", "sérver",
		strings.Repeat("h", 254),
	} {
		_, err := load(bad)
		if err == nil {
			t.Errorf("TIMEVAULT_SERVER_HOST=%q was accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "TIMEVAULT_SERVER_HOST") {
			t.Errorf("TIMEVAULT_SERVER_HOST=%q: the error %q must name the setting", bad, err)
		}
		if len(bad) > 3 && strings.Contains(err.Error(), bad) {
			t.Errorf("TIMEVAULT_SERVER_HOST=%q: the error repeats the value: %v", bad, err)
		}
	}
}

func TestRestoreDisplayRoot(t *testing.T) {
	load := func(restoreRoot, display, host string) (Config, error) {
		return Load(lookupFrom(map[string]string{
			"TIMEVAULT_RESTORE_ROOT":         restoreRoot,
			"TIMEVAULT_RESTORE_DISPLAY_ROOT": display,
			"TIMEVAULT_SERVER_HOST":          host,
		}))
	}
	const root = `D:\TimeVault\restores`

	// The value is stored the way ParseUNC normalises it; the host keeps the spelling it was written with.
	for _, tc := range []struct{ display, host, want string }{
		{`\\sgmeslt595.co.murata.local\TimeVault\restores`, "sgmeslt595.co.murata.local", `\\sgmeslt595.co.murata.local\TimeVault\restores`},
		{`//SGMESLT595.CO.MURATA.LOCAL/TimeVault/restores/`, "sgmeslt595.co.murata.local", `\\SGMESLT595.CO.MURATA.LOCAL\TimeVault\restores`},
		{"  \\\\srv\\share  ", "", `\\srv\share`},
		{`\\any-host\share\a\b`, "", `\\any-host\share\a\b`},
		{``, "srv", ``},
	} {
		c, err := load(root, tc.display, tc.host)
		if err != nil || c.RestoreDisplayRoot != tc.want {
			t.Errorf("display %q with host %q: got %q, %v; want %q", tc.display, tc.host, c.RestoreDisplayRoot, err, tc.want)
		}
	}

	invalid := []string{`D:\restores`, `\\srv`, `\\srv\C$`, `\\srv\share\..\x`, `\\srv\share\a*b`, `\\sr:v\share`, `restores`, `https://srv/share`}
	for _, bad := range invalid {
		_, err := load(root, bad, "")
		if err == nil {
			t.Errorf("TIMEVAULT_RESTORE_DISPLAY_ROOT=%q was accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "TIMEVAULT_RESTORE_DISPLAY_ROOT") || strings.Contains(err.Error(), strings.TrimLeft(bad, `\/`)) {
			t.Errorf("TIMEVAULT_RESTORE_DISPLAY_ROOT=%q: the error must name the setting and not repeat the value: %v", bad, err)
		}
	}

	// A host other than the server's is refused, and the error names both settings.
	for _, host := range []string{`\\other\TimeVault\restores`, `\\sgmeslt595\TimeVault\restores`, `\\sgmeslt595.co.murata.local.evil\x\y`} {
		_, err := load(root, host, "sgmeslt595.co.murata.local")
		if err == nil || !strings.Contains(err.Error(), "TIMEVAULT_RESTORE_DISPLAY_ROOT") || !strings.Contains(err.Error(), "TIMEVAULT_SERVER_HOST") {
			t.Errorf("display %q: want an error naming both settings, got %v", host, err)
		} else if strings.Contains(err.Error(), "evil") || strings.Contains(err.Error(), "murata") {
			t.Errorf("display %q: the error repeats a value: %v", host, err)
		}
	}

	// Without a restore folder nothing is restored, so the value is accepted and dropped, even a bad one.
	for _, display := range []string{`\\srv\share`, `not a path`, `\\other\share`} {
		c, err := load("", display, "sgmeslt595.co.murata.local")
		if err != nil || c.RestoreDisplayRoot != "" {
			t.Errorf("display %q without a restore folder: got %q, %v; want it ignored", display, c.RestoreDisplayRoot, err)
		}
	}

	// Every problem is reported at once.
	_, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_RESTORE_ROOT": root, "TIMEVAULT_RESTORE_DISPLAY_ROOT": `\\srv`, "TIMEVAULT_SERVER_HOST": "bad host", "TIMEVAULT_LOG_LEVEL": "loud",
	}))
	if err == nil {
		t.Fatal("want errors")
	}
	for _, key := range []string{"TIMEVAULT_RESTORE_DISPLAY_ROOT", "TIMEVAULT_SERVER_HOST", "TIMEVAULT_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the error %q should mention %s", err, key)
		}
	}
}

func TestDisplaySettingsAreDescribedAndLogged(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_SERVER_HOST":          "sgmeslt595.co.murata.local",
		"TIMEVAULT_RESTORE_ROOT":         `D:\TimeVault\restores`,
		"TIMEVAULT_RESTORE_DISPLAY_ROOT": `//sgmeslt595.co.murata.local/TimeVault/restores/`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	d := c.Describe()
	if d.ServerHost != "sgmeslt595.co.murata.local" || d.RestoreDisplayRoot != `\\sgmeslt595.co.murata.local\TimeVault\restores` {
		t.Errorf("Describe = %+v", d)
	}
	logged := map[string]any{}
	attrs := c.LogAttrs()
	for i := 0; i+1 < len(attrs); i += 2 {
		logged[attrs[i].(string)] = attrs[i+1]
	}
	if logged["server_host"] != d.ServerHost || logged["restore_display_root"] != d.RestoreDisplayRoot {
		t.Errorf("LogAttrs = %v", attrs)
	}
	if empty, _ := Load(lookupFrom(nil)); empty.Describe().ServerHost != "" || empty.Describe().RestoreDisplayRoot != "" {
		t.Error("nothing configured must describe nothing")
	}
}
