package config

import (
	"strings"
	"testing"
)

const testRedis = "redis://:hunter2-secret@redis.example.test:6379/0"

func TestWebLogIsOnWhenRedisIsConfigured(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_REDIS_URL": testRedis}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.WebLog || c.WebLogKey != "web_log" || c.WebLogHost == "" || c.WebLogHost != strings.ToLower(c.WebLogHost) {
		t.Errorf("web log = %v, key %q, host %q; want on, web_log and the name of the computer in lower case", c.WebLog, c.WebLogKey, c.WebLogHost)
	}
	if c, err := Load(lookupFrom(nil)); err != nil || c.WebLog {
		t.Errorf("without Redis the web log is off: %v, %v", c.WebLog, err)
	}
}

func TestWebLogSwitch(t *testing.T) {
	for in, want := range map[string]bool{"on": true, "TRUE": true, "1": true, "yes": true, "auto": true, "off": false, "false": false, "0": false, "No": false} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_REDIS_URL": testRedis, "TIMEVAULT_WEB_LOG": in}))
		if err != nil || c.WebLog != want {
			t.Errorf("TIMEVAULT_WEB_LOG=%q -> %v, %v; want %v", in, c.WebLog, err, want)
		}
	}
	if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_REDIS_URL": testRedis, "TIMEVAULT_WEB_LOG": "sometimes"})); err == nil || !strings.Contains(err.Error(), "TIMEVAULT_WEB_LOG") {
		t.Errorf("a value that is not on, off or auto: %v", err)
	}
	// Off needs no Redis; on does, because that is where the lines go.
	if c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_WEB_LOG": "off"})); err != nil || c.WebLog {
		t.Errorf("off without Redis: %v, %v", c.WebLog, err)
	}
	if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_WEB_LOG": "on"})); err == nil || !strings.Contains(err.Error(), "TIMEVAULT_REDIS_URL") {
		t.Errorf("on without Redis: %v", err)
	}
}

func TestWebLogKeyAndHost(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_REDIS_URL": testRedis, "TIMEVAULT_WEB_LOG_KEY": "tv:web_log-2", "TIMEVAULT_WEB_LOG_HOST": "SGMESLT595",
	}))
	if err != nil || c.WebLogKey != "tv:web_log-2" || c.WebLogHost != "SGMESLT595" {
		t.Errorf("key %q, host %q, %v; an explicit host is kept as it is written", c.WebLogKey, c.WebLogHost, err)
	}
	for _, bad := range []struct{ key, value string }{
		{"TIMEVAULT_WEB_LOG_KEY", "web log"},
		{"TIMEVAULT_WEB_LOG_KEY", "web\x01log"},
		{"TIMEVAULT_WEB_LOG_KEY", strings.Repeat("a", 129)},
		{"TIMEVAULT_WEB_LOG_KEY", "app_user_x"},        // inside the namespace of the accounts
		{"TIMEVAULT_WEB_LOG_KEY", "APP_USER_log"},      // Redis keys are case sensitive, a person's eye is not
		{"TIMEVAULT_WEB_LOG_KEY", "sso_login_web_log"}, // inside the namespace of the single sign-on results
		{"TIMEVAULT_WEB_LOG_HOST", "host | evil"},      // would start another field
		{"TIMEVAULT_WEB_LOG_HOST", strings.Repeat("h", 64)},
	} {
		_, err := Load(lookupFrom(map[string]string{"TIMEVAULT_REDIS_URL": testRedis, bad.key: bad.value}))
		if err == nil || !strings.Contains(err.Error(), bad.key) {
			t.Errorf("%s=%q: error = %v, want a refusal that names the setting", bad.key, bad.value, err)
		}
	}
	// The prefix of the accounts is a setting too.
	_, err = Load(lookupFrom(map[string]string{"TIMEVAULT_REDIS_URL": testRedis, "TIMEVAULT_REDIS_KEY_PREFIX": "web_", "TIMEVAULT_WEB_LOG_KEY": "web_log"}))
	if err == nil || !strings.Contains(err.Error(), "TIMEVAULT_WEB_LOG_KEY") {
		t.Errorf("a key inside a changed account prefix: %v", err)
	}
}

func TestWebLogIsDescribedWithoutSecrets(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_REDIS_URL": testRedis, "TIMEVAULT_WEB_LOG_HOST": "host1"}))
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Describe(); !d.WebLog || d.WebLogKey != "web_log" || d.WebLogHost != "host1" {
		t.Errorf("Describe() = %+v", d)
	}
	attrs := c.LogAttrs()
	found := 0
	for i := 0; i+1 < len(attrs); i += 2 {
		switch attrs[i] {
		case "web_log", "web_log_key", "web_log_host":
			found++
		}
		if s, ok := attrs[i+1].(string); ok && strings.Contains(s, "hunter2") {
			t.Errorf("the start-up log would show a secret: %v=%q", attrs[i], s)
		}
	}
	if found != 3 {
		t.Errorf("the start-up log has %d of the 3 web log settings", found)
	}
}

func TestHelpIsAPageOfTheApplicationSoItCannotBeABasePath(t *testing.T) {
	if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_BASE_PATH": "/help"})); err == nil || !strings.Contains(err.Error(), "TIMEVAULT_BASE_PATH") {
		t.Errorf("TIMEVAULT_BASE_PATH=/help: error = %v", err)
	}
}
