package config

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// siteAlternatives is, for every Site setting, a valid value that differs from its default in a way Load shows.
// A new Site setting must be added here: that is how the test knows Load reads it.
var siteAlternatives = map[string]string{
	"server_host": "files01", "restore_display_root": `\\files01\restores`, "avatar_url": "/pictures/{id}.png",
	"base_path": "/tv", "trusted_proxies": "10.0.0.1",
	"session_ttl": "2h", "cookie_secure": "true", "sso_host": "sso.example.test", "sso_require_same_ip": "false",
	"job_workers": "3", "hash_workers": "7", "process_priority": "background",
	"scheduler_parallel": "2", "scheduler_gap": "5m", "scheduler_rest": "0s", "scheduler_probe": "true", "scheduler_full_scan_every": "12h",
	"search_index": "off",
	"update_url":   "https://example.test/timevault/", "update_interval": "30m", "update_restart": "exit",
	"log_level": "debug", "log_format": "json", "web_log": "off", "web_log_key": "my_web_log", "web_log_host": "thehost",
}

// siteBase is an environment in which every Site setting can show its effect: Redis is there for the web log, and a
// restore folder for the display root of the restores.
func siteBase() Lookup {
	return lookupFrom(map[string]string{
		"TIMEVAULT_REDIS_URL": "redis://:p@127.0.0.1:6379/0", "TIMEVAULT_RESTORE_ROOT": `D:\restores`,
	})
}

func TestSiteRegistryIsConsistent(t *testing.T) {
	keyForm := regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	groups := SiteGroups()
	seen := map[string]bool{}
	for _, s := range SiteSettings() {
		if !keyForm.MatchString(s.Key) || seen[s.Key] {
			t.Errorf("key %q is not a plain lower-case key, or it is listed twice", s.Key)
		}
		seen[s.Key] = true
		if !slices.Contains(groups, s.Group) {
			t.Errorf("%s: unknown group %q", s.Key, s.Group)
		}
		switch s.Kind {
		case SiteChoice:
			if !slices.Contains(s.Choices, s.Default) {
				t.Errorf("%s: the default %q is not one of its choices", s.Key, s.Default)
			}
		case SiteBool:
			if s.Default != "true" && s.Default != "false" {
				t.Errorf("%s: the default of a bool is %q", s.Key, s.Default)
			}
		case SiteText, SiteNumber:
		default:
			t.Errorf("%s: unknown kind %q", s.Key, s.Kind)
		}
		if got, err := NormalizeSite(s.Key, s.Default); err != nil || got != s.Default {
			t.Errorf("%s: the default %q does not survive NormalizeSite: %q, %v", s.Key, s.Default, got, err)
		}
		if env := s.Env(); env != "TIMEVAULT_"+strings.ToUpper(s.Key) || siteKeyOfEnv[env] != s.Key {
			t.Errorf("%s: environment name %q is not mapped back", s.Key, env)
		}
	}
	if len(seen) < 20 {
		t.Errorf("only %d Site settings are registered", len(seen))
	}
}

func TestEverySiteSettingIsReadByLoad(t *testing.T) {
	base := siteBase()
	defaults, err := Load(SiteLookup(base, nil))
	if err != nil {
		t.Fatalf("the defaults must be valid: %v", err)
	}
	if want := SiteEffective(nil); len(want) != len(SiteSettings()) {
		t.Fatalf("SiteEffective(nil) has %d entries", len(want))
	}
	for _, s := range SiteSettings() {
		alt, ok := siteAlternatives[s.Key]
		if !ok {
			t.Errorf("%s has no entry in siteAlternatives", s.Key)
			continue
		}
		if alt == s.Default {
			t.Errorf("%s: the alternative equals the default", s.Key)
			continue
		}
		cfg, err := Load(SiteLookup(base, map[string]string{s.Key: alt}))
		if err != nil {
			t.Errorf("%s=%q: %v", s.Key, alt, err)
			continue
		}
		if reflect.DeepEqual(cfg, defaults) {
			t.Errorf("%s=%q changes nothing: Load does not read it", s.Key, alt)
		}
	}
}

func TestSiteLookupIgnoresTheEnvironmentForSiteSettings(t *testing.T) {
	base := lookupFrom(map[string]string{"TIMEVAULT_JOB_WORKERS": "9", "TIMEVAULT_DATABASE_URL": "postgres://u:p@127.0.0.1/tv", "TIMEVAULT_REPOSITORY_PATH": "/r"})
	c, err := Load(SiteLookup(base, nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.JobWorkers != 2 || c.DatabaseURL == "" || c.RepositoryPath != "/r" {
		t.Errorf("a Site setting comes from the database and everything else from the environment: workers=%d db=%q repo=%q", c.JobWorkers, c.DatabaseURL, c.RepositoryPath)
	}
	if c, err = Load(SiteLookup(base, map[string]string{"job_workers": "5"})); err != nil || c.JobWorkers != 5 {
		t.Errorf("a stored value is used: %d, %v", c.JobWorkers, err)
	}
	if got := LegacySettings(base); !reflect.DeepEqual(got, []string{"TIMEVAULT_JOB_WORKERS"}) {
		t.Errorf("LegacySettings = %v", got)
	}
}

func TestLegacySettingsNamesWhatNothingReadsAnyMore(t *testing.T) {
	base := lookupFrom(map[string]string{
		"TIMEVAULT_EVERYTHING_URL": "http://127.0.0.1:1/", "TIMEVAULT_SCHEDULER_EVERYTHING_PROBE": "true", "TIMEVAULT_SESSION_TTL": "4h",
		"TIMEVAULT_LOG_LEVEL": "  ", "TIMEVAULT_DATABASE_URL": "postgres://x",
	})
	want := []string{"TIMEVAULT_EVERYTHING_URL", "TIMEVAULT_SCHEDULER_EVERYTHING_PROBE", "TIMEVAULT_SESSION_TTL"}
	if got := LegacySettings(base); !reflect.DeepEqual(got, want) {
		t.Errorf("LegacySettings = %v, want %v", got, want)
	}
	if got := LegacySettings(lookupFrom(nil)); len(got) != 0 {
		t.Errorf("nothing is left over, got %v", got)
	}
}

func TestSiteEffectiveFillsDefaults(t *testing.T) {
	got := SiteEffective(map[string]string{"job_workers": " 4 ", "log_level": "", "not_a_setting": "x"})
	if got["job_workers"] != "4" || got["log_level"] != "info" || got["session_ttl"] != "8h" {
		t.Errorf("SiteEffective = %v", got)
	}
	if _, ok := got["not_a_setting"]; ok {
		t.Error("a key that is not a Site setting must be dropped")
	}
}

func TestNormalizeSite(t *testing.T) {
	good := []struct{ key, in, want string }{
		{"cookie_secure", " TRUE ", "true"}, {"cookie_secure", "0", "false"}, {"job_workers", " 03 ", "3"},
		{"process_priority", "Background", "background"}, {"sso_host", "  sso.example.test ", "sso.example.test"}, {"base_path", "", ""},
	}
	for _, c := range good {
		if got, err := NormalizeSite(c.key, c.in); err != nil || got != c.want {
			t.Errorf("NormalizeSite(%s, %q) = %q, %v; want %q", c.key, c.in, got, err, c.want)
		}
	}
	bad := []struct{ key, in string }{
		{"cookie_secure", "sometimes"}, {"job_workers", "two"}, {"process_priority", "high"}, {"sso_host", "a\nb"},
		{"base_path", strings.Repeat("x", 2001)}, {"nope", "x"},
	}
	for _, c := range bad {
		if got, err := NormalizeSite(c.key, c.in); err == nil {
			t.Errorf("NormalizeSite(%s, %q) = %q, want an error", c.key, c.in, got)
		}
	}
}

func TestSiteProblemsSpeakInTheWordsOfThePage(t *testing.T) {
	base := lookupFrom(map[string]string{"TIMEVAULT_REDIS_URL": "redis://:p@127.0.0.1:6379/0"})
	if got := SiteProblems(base, map[string]string{"job_workers": "4", "scheduler_probe": "true"}); got != nil {
		t.Fatalf("valid values were refused: %v", got)
	}
	cases := map[string]struct {
		values map[string]string
		want   string
	}{
		"range":      {map[string]string{"job_workers": "99"}, "job_workers must be an integer between 1 and 16"},
		"duration":   {map[string]string{"session_ttl": "1s"}, "session_ttl must be between 5m and 720h"},
		"unknown":    {map[string]string{"colour": "blue"}, "colour is not a Site setting"},
		"not a bool": {map[string]string{"cookie_secure": "perhaps"}, "cookie_secure must be true or false"},
		"web log":    {map[string]string{"web_log": "on"}, ""}, // valid: the base has Redis
		"long key":   {map[string]string{"web_log_key": "bad key!"}, "web_log_key may only have letters"},
		"base path":  {map[string]string{"base_path": "/api"}, "base_path must not start with /api"},
	}
	for name, c := range cases {
		got := SiteProblems(base, c.values)
		if c.want == "" {
			if got != nil {
				t.Errorf("%s: %v", name, got)
			}
			continue
		}
		joined := strings.Join(got, " | ")
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems %q do not say %q", name, joined, c.want)
		}
		if strings.Contains(joined, "TIMEVAULT_") {
			t.Errorf("%s: the problems still name the environment: %q", name, joined)
		}
	}
	// The web log needs the Redis of the environment, which this base does not have.
	got := SiteProblems(lookupFrom(nil), map[string]string{"web_log": "on"})
	if len(got) != 1 || !strings.Contains(got[0], "web_log is on") || !strings.Contains(got[0], "TIMEVAULT_REDIS_URL") {
		t.Errorf("web log without Redis: %v", got)
	}
	// An update address needs automatic migrations, which are a setting of the environment.
	got = SiteProblems(lookupFrom(nil), map[string]string{"update_url": "https://example.test/tv/"})
	if len(got) != 1 || !strings.Contains(got[0], "update_url needs TIMEVAULT_AUTO_MIGRATE=true") {
		t.Errorf("update url without automatic migrations: %v", got)
	}
	if got := SiteProblems(lookupFrom(map[string]string{"TIMEVAULT_AUTO_MIGRATE": "true"}), map[string]string{"update_url": "https://example.test/tv/"}); got != nil {
		t.Errorf("update url with automatic migrations: %v", got)
	}
	// A value is never repeated in a problem.
	for _, p := range SiteProblems(base, map[string]string{"sso_host": "http://user:hunter2-secret@sso.example.test"}) {
		if strings.Contains(p, "hunter2-secret") {
			t.Errorf("problem repeats the value: %q", p)
		}
	}
}
