package config

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestSchedulerAndSSODefaults(t *testing.T) {
	c, err := Load(lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.SSOHost != "" || !c.SSORequireSameIP || len(c.TrustedProxies) != 0 {
		t.Errorf("single sign-on is off by default and checks the address when it is on: %+v", c)
	}
	if c.SchedulerParallel != 1 || !c.SchedulerGapAuto || !c.SchedulerRestAuto || c.SchedulerProbe ||
		c.SchedulerFullScanEvery != 24*time.Hour || c.ProcessPriority != "below_normal" {
		t.Errorf("scheduler defaults: %+v", c)
	}
}

func TestSSOHostIsNormalised(t *testing.T) {
	for in, want := range map[string]string{
		"prod_eng.co.murata.local":        "http://prod_eng.co.murata.local",
		"  sso.example.test:8080  ":       "http://sso.example.test:8080",
		"https://sso.example.test":        "https://sso.example.test",
		"HTTP://sso.example.test:80/":     "http://sso.example.test:80",
		"10.1.2.3":                        "http://10.1.2.3",
		"[2001:db8::1]:8080":              "http://[2001:db8::1]:8080",
		"":                                "",
		"https://sso.example.test:8443/ ": "https://sso.example.test:8443",
	} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SSO_HOST": in}))
		if err != nil || c.SSOHost != want {
			t.Errorf("TIMEVAULT_SSO_HOST=%q -> %q, %v; want %q", in, c.SSOHost, err, want)
		}
	}
	for _, bad := range []string{
		"ftp://sso.example.test", "http://", "http://user:pw@sso.example.test", "http://sso.example.test/sso/api",
		"http://sso.example.test?x=1", "http://sso.example.test#top", "sso example", "http://sso.example.test:0", "http://sso.example.test:99999",
		"http://sso.example.test:abc", "http://sso/<script>",
	} {
		_, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SSO_HOST": bad}))
		if err == nil || !strings.Contains(err.Error(), "TIMEVAULT_SSO_HOST") {
			t.Errorf("TIMEVAULT_SSO_HOST=%q: error = %v, want a refusal that names the setting", bad, err)
		}
	}
	// An error never echoes a password that was typed into the address.
	_, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SSO_HOST": "http://user:hunter2-secret@sso.example.test"}))
	if err == nil || strings.Contains(err.Error(), "hunter2-secret") {
		t.Errorf("error = %v", err)
	}
}

func TestTrustedProxies(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_TRUSTED_PROXIES": " 127.0.0.1, ::1;10.8.0.0/16  192.168.7.9 ::ffff:10.9.9.9"}))
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.8.255.1": true, "192.168.7.9": true, "10.9.9.9": true,
		"10.9.9.8": false, "192.168.7.10": false, "203.0.113.7": false, "10.7.0.1": false,
	} {
		if got := c.IsTrustedProxy(netip.MustParseAddr(ip)); got != want {
			t.Errorf("IsTrustedProxy(%s) = %v, want %v", ip, got, want)
		}
	}
	// An IPv4 address mapped into IPv6 is the same computer.
	if !c.IsTrustedProxy(netip.MustParseAddr("::ffff:127.0.0.1")) {
		t.Error("a mapped IPv4 address must match its IPv4 entry")
	}
	for _, bad := range []string{"proxy.example.test", "10.0.0.0/33", "10.0.0.1/8/8", "999.1.1.1"} {
		if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_TRUSTED_PROXIES": bad})); err == nil {
			t.Errorf("TIMEVAULT_TRUSTED_PROXIES=%q was accepted", bad)
		}
	}
}

func TestSchedulerSettings(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_SCHEDULER_PARALLEL": "2", "TIMEVAULT_SCHEDULER_GAP": "5m", "TIMEVAULT_SCHEDULER_REST": "0s",
		"TIMEVAULT_SCHEDULER_PROBE": "true", "TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY": "12h",
		"TIMEVAULT_PROCESS_PRIORITY": "Background", "TIMEVAULT_SSO_REQUIRE_SAME_IP": "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SchedulerParallel != 2 || c.SchedulerGapAuto || c.SchedulerGap != 5*time.Minute || c.SchedulerRestAuto || c.SchedulerRest != 0 ||
		!c.SchedulerProbe || c.SchedulerFullScanEvery != 12*time.Hour || c.ProcessPriority != "background" || c.SSORequireSameIP {
		t.Errorf("settings = %+v", c)
	}
	if c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SCHEDULER_GAP": "AUTO"})); err != nil || !c.SchedulerGapAuto {
		t.Errorf("auto in capitals: %+v, %v", c, err)
	}
	for name, env := range map[string]map[string]string{
		"parallel zero":     {"TIMEVAULT_SCHEDULER_PARALLEL": "0"},
		"parallel too many": {"TIMEVAULT_SCHEDULER_PARALLEL": "5"},
		"gap not a time":    {"TIMEVAULT_SCHEDULER_GAP": "soon"},
		"gap negative":      {"TIMEVAULT_SCHEDULER_GAP": "-1m"},
		"gap too long":      {"TIMEVAULT_SCHEDULER_GAP": "7h"},
		"rest not a time":   {"TIMEVAULT_SCHEDULER_REST": "long"},
		"full scan short":   {"TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY": "10m"},
		"full scan long":    {"TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY": "800h"},
		"unknown priority":  {"TIMEVAULT_PROCESS_PRIORITY": "high"},
		"probe not a bool":  {"TIMEVAULT_SCHEDULER_PROBE": "sometimes"},
	} {
		if _, err := Load(lookupFrom(env)); err == nil {
			t.Errorf("%s: Load succeeded, want an error", name)
		}
	}
}

func TestSSOAndSchedulerAreDescribedAndLogged(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SSO_HOST": "sso.example.test", "TIMEVAULT_TRUSTED_PROXIES": "10.0.0.1"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Describe().SSOHost != "http://sso.example.test" {
		t.Errorf("Describe().SSOHost = %q", c.Describe().SSOHost)
	}
	logged := ""
	attrs := c.LogAttrs()
	for i := 0; i+1 < len(attrs); i += 2 {
		logged += attrs[i].(string) + "=" + stringify(attrs[i+1]) + " "
	}
	for _, want := range []string{"sso_host=http://sso.example.test", "sso_require_same_ip=true", "trusted_proxies=1", "scheduler_parallel=1",
		"scheduler_gap=auto", "scheduler_rest=auto", "process_priority=below_normal"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the start-up log lacks %q: %s", want, logged)
		}
	}
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return string(rune('0' + x))
	}
	return "?"
}
