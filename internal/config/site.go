package config

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// A Site setting is a setting that is kept in the database and edited on the Site settings page by the local
// administrator, instead of in the environment or the .env file. The registry below says which settings these are,
// how the page offers each of them and what applies when nothing is stored. Everything else (the database, the
// listen address, the folders of the repository and of the restores, the local administrator, the session key and
// the Redis of the user directory) stays in the environment or the .env file: the service needs it before it can
// reach its database, it is a secret, or a wrong value would lock everybody out.
//
// A Site setting is stored by its key, such as job_workers. Its name in the environment is the key in capitals
// behind TIMEVAULT_ (TIMEVAULT_JOB_WORKERS), which is what Load reads it by. SiteLookup answers those names from the
// stored values, so Load checks a Site setting with the code that always checked it. A setting that is not stored has
// its default; the page stores nothing for a value that equals the default, so a default that changes in a later
// version reaches whoever never chose a value.

// SiteKind says how the Site settings page offers a setting.
type SiteKind string

const (
	// SiteText is a line of text; empty is allowed. Durations and addresses are text too.
	SiteText SiteKind = "text"
	// SiteNumber is a whole number.
	SiteNumber SiteKind = "number"
	// SiteBool is true or false.
	SiteBool SiteKind = "bool"
	// SiteChoice is one of SiteSetting.Choices.
	SiteChoice SiteKind = "choice"
)

// The groups of the Site settings page, in the order it shows them.
const (
	GroupAddress     = "address"
	GroupSignIn      = "signin"
	GroupPerformance = "performance"
	GroupScheduler   = "scheduler"
	GroupSearch      = "search"
	GroupUpdates     = "updates"
	GroupLogging     = "logging"
)

// SiteGroups lists the groups in page order.
func SiteGroups() []string {
	return []string{GroupAddress, GroupSignIn, GroupPerformance, GroupScheduler, GroupSearch, GroupUpdates, GroupLogging}
}

// maxSiteValue bounds the text of one stored value.
const maxSiteValue = 2000

// SiteSetting describes one Site setting.
type SiteSetting struct {
	// Key is how the setting is stored and named on the page and in the API.
	Key   string
	Group string
	Kind  SiteKind
	// Default is what applies when nothing is stored, written as it would be typed.
	Default string
	// Choices are the values of a SiteChoice setting.
	Choices []string
}

// Env is the name Load reads the setting by.
func (s SiteSetting) Env() string { return "TIMEVAULT_" + strings.ToUpper(s.Key) }

var siteSettings = []SiteSetting{
	{Key: "server_host", Group: GroupAddress, Kind: SiteText},
	{Key: "restore_display_root", Group: GroupAddress, Kind: SiteText},
	{Key: "avatar_url", Group: GroupAddress, Kind: SiteText},
	{Key: "base_path", Group: GroupAddress, Kind: SiteText},
	{Key: "trusted_proxies", Group: GroupAddress, Kind: SiteText},

	{Key: "session_ttl", Group: GroupSignIn, Kind: SiteText, Default: "8h"},
	{Key: "cookie_secure", Group: GroupSignIn, Kind: SiteBool, Default: "false"},
	{Key: "sso_host", Group: GroupSignIn, Kind: SiteText},
	{Key: "sso_require_same_ip", Group: GroupSignIn, Kind: SiteBool, Default: "true"},

	{Key: "job_workers", Group: GroupPerformance, Kind: SiteNumber, Default: "2"},
	{Key: "hash_workers", Group: GroupPerformance, Kind: SiteNumber, Default: strconv.Itoa(defaultHashWorkers())},
	{Key: "process_priority", Group: GroupPerformance, Kind: SiteChoice, Default: "below_normal", Choices: []string{"normal", "below_normal", "background"}},

	{Key: "scheduler_parallel", Group: GroupScheduler, Kind: SiteNumber, Default: "1"},
	{Key: "scheduler_gap", Group: GroupScheduler, Kind: SiteText, Default: "auto"},
	{Key: "scheduler_rest", Group: GroupScheduler, Kind: SiteText, Default: "auto"},
	{Key: "scheduler_probe", Group: GroupScheduler, Kind: SiteBool, Default: "false"},
	{Key: "scheduler_full_scan_every", Group: GroupScheduler, Kind: SiteText, Default: "24h"},

	{Key: "search_index", Group: GroupSearch, Kind: SiteChoice, Default: "auto", Choices: []string{"auto", "off"}},

	{Key: "update_url", Group: GroupUpdates, Kind: SiteText},
	{Key: "update_interval", Group: GroupUpdates, Kind: SiteText, Default: "10m"},
	{Key: "update_restart", Group: GroupUpdates, Kind: SiteChoice, Default: "supervise", Choices: []string{"supervise", "exit"}},

	{Key: "log_level", Group: GroupLogging, Kind: SiteChoice, Default: "info", Choices: []string{"debug", "info", "warn", "error"}},
	{Key: "log_format", Group: GroupLogging, Kind: SiteChoice, Default: "text", Choices: []string{"text", "json"}},
	{Key: "web_log", Group: GroupLogging, Kind: SiteChoice, Default: "auto", Choices: []string{"auto", "on", "off"}},
	{Key: "web_log_key", Group: GroupLogging, Kind: SiteText, Default: DefaultWebLogKey},
	{Key: "web_log_host", Group: GroupLogging, Kind: SiteText},
}

// SiteSettings lists the Site settings in the order the page shows them.
func SiteSettings() []SiteSetting {
	out := slices.Clone(siteSettings)
	for i := range out {
		out[i].Choices = slices.Clone(out[i].Choices)
	}
	return out
}

// SiteSettingByKey finds a Site setting by its key.
func SiteSettingByKey(key string) (SiteSetting, bool) {
	for _, s := range siteSettings {
		if s.Key == key {
			s.Choices = slices.Clone(s.Choices)
			return s, true
		}
	}
	return SiteSetting{}, false
}

// siteKeyOfEnv maps the environment name of a Site setting to its key.
var siteKeyOfEnv = func() map[string]string {
	m := make(map[string]string, len(siteSettings))
	for _, s := range siteSettings {
		m[s.Env()] = s.Key
	}
	return m
}()

// removedSettings are the settings of earlier versions that nothing reads any more.
var removedSettings = []string{
	"TIMEVAULT_EVERYTHING_URL", "TIMEVAULT_EVERYTHING_USERNAME", "TIMEVAULT_EVERYTHING_PASSWORD", "TIMEVAULT_EVERYTHING_TIMEOUT",
	"TIMEVAULT_SCHEDULER_EVERYTHING_PROBE",
}

// SiteLookup answers the Site settings from values, the stored ones by key (a key without a value is not set, so its
// default applies) and every other setting from base. What base holds for a Site setting is ignored: since the Site
// settings moved into the database, an old line in the .env file must not be the reason a value that was saved on the
// page does not take effect.
func SiteLookup(base Lookup, values map[string]string) Lookup {
	return func(name string) (string, bool) {
		if key, ok := siteKeyOfEnv[name]; ok {
			v, ok := values[key]
			return v, ok
		}
		return base(name)
	}
}

// SiteEffective gives every Site setting the value that applies to it: the stored one, or the default. The keys of
// values that are not Site settings are dropped.
func SiteEffective(values map[string]string) map[string]string {
	out := make(map[string]string, len(siteSettings))
	for _, s := range siteSettings {
		if v := strings.TrimSpace(values[s.Key]); v != "" {
			out[s.Key] = v
		} else {
			out[s.Key] = s.Default
		}
	}
	return out
}

// LegacySettings names, sorted, the settings of base that no longer have any effect: the Site settings, which are in
// the database now, and the settings of the Everything search that the NTFS index replaced. It returns names only.
func LegacySettings(base Lookup) []string {
	var names []string
	for _, name := range removedSettings {
		if _, ok := base(name); ok {
			names = append(names, name)
		}
	}
	for name := range siteKeyOfEnv {
		if v, ok := base(name); ok && strings.TrimSpace(v) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// NormalizeSite checks that value suits the kind of the setting and returns it in the form that is stored: a
// trimmed text, true or false, a plain decimal number, a choice in lower case. Whether a number is in range, or a text
// is an address, is for Load; SiteProblems asks it. An empty value means "use the default" and stays empty.
func NormalizeSite(key, value string) (string, error) {
	s, ok := SiteSettingByKey(key)
	if !ok {
		return "", fmt.Errorf("%s is not a Site setting", key)
	}
	value = strings.TrimSpace(value)
	if len(value) > maxSiteValue {
		return "", fmt.Errorf("%s is too long (at most %d characters)", key, maxSiteValue)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s must be one line without control characters", key)
		}
	}
	if value == "" {
		return "", nil
	}
	switch s.Kind {
	case SiteBool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", fmt.Errorf("%s must be true or false", key)
		}
		return strconv.FormatBool(b), nil
	case SiteNumber:
		n, err := strconv.Atoi(value)
		if err != nil {
			return "", fmt.Errorf("%s must be a whole number", key)
		}
		return strconv.Itoa(n), nil
	case SiteChoice:
		v := strings.ToLower(value)
		if !slices.Contains(s.Choices, v) {
			return "", fmt.Errorf("%s must be one of %s", key, strings.Join(s.Choices, ", "))
		}
		return v, nil
	}
	return value, nil
}

// SiteProblems says what is wrong with values, Site settings as they would be stored (key to value, a missing key or
// an empty value meaning the default), checked together with the settings of base that they depend on. It returns
// nil when they are valid, and otherwise one sentence per problem, written with the keys of the Site settings
// instead of the names of the environment. The sentences never repeat a value.
func SiteProblems(base Lookup, values map[string]string) []string {
	var problems []string
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := NormalizeSite(k, values[k]); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return problems
	}
	cfg, err := Load(SiteLookup(base, values))
	if err == nil {
		err = cfg.validateSiteCross()
	}
	if err == nil {
		return nil
	}
	if inner := errors.Unwrap(err); inner != nil {
		err = inner
	}
	var parts []error
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts = joined.Unwrap()
	} else {
		parts = []error{err}
	}
	for _, e := range parts {
		problems = append(problems, siteWords.Replace(e.Error()))
	}
	return problems
}

// validateSiteCross checks what depends on a Site setting and a setting of the environment together.
func (c Config) validateSiteCross() error {
	if c.UpdateURL != "" && !c.AutoMigrate {
		return errors.New("TIMEVAULT_UPDATE_URL needs TIMEVAULT_AUTO_MIGRATE=true in the environment or the .env file: a new version may change the database, and nobody runs the migrate command when the service updates itself")
	}
	return nil
}

// siteWords turns the names of the environment in an error text into the keys of the Site settings. The longer names
// come first, because TIMEVAULT_WEB_LOG is the start of TIMEVAULT_WEB_LOG_KEY.
var siteWords = func() *strings.Replacer {
	envs := make([]string, 0, len(siteKeyOfEnv))
	for env := range siteKeyOfEnv {
		envs = append(envs, env)
	}
	sort.Slice(envs, func(i, j int) bool { return len(envs[i]) > len(envs[j]) })
	pairs := make([]string, 0, 2*len(envs))
	for _, env := range envs {
		pairs = append(pairs, env, siteKeyOfEnv[env])
	}
	return strings.NewReplacer(pairs...)
}()
