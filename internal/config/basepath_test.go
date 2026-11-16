package config

import (
	"strings"
	"testing"
)

func TestBasePathIsNormalised(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"   ":             "",
		"/":               "",
		"tv":              "/tv",
		"/tv":             "/tv",
		"/tv/":            "/tv",
		"  /tv/  ":        "/tv",
		"/apps/tv":        "/apps/tv",
		"/Time_Vault-1.x": "/Time_Vault-1.x",
		"/~tv":            "/~tv",
		"/tvx":            "/tvx",
	} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_BASE_PATH": in}))
		if err != nil || c.BasePath != want {
			t.Errorf("TIMEVAULT_BASE_PATH=%q -> %q, %v; want %q", in, c.BasePath, err, want)
		}
	}
	if c, err := Load(lookupFrom(nil)); err != nil || c.BasePath != "" {
		t.Errorf("the base path is empty by default: %q, %v", c.BasePath, err)
	}
}

func TestBasePathRefusesWhatCannotBeAPrefix(t *testing.T) {
	for _, bad := range []string{
		"//", "/tv//x", "/tv/../x", "/./tv", "/..", "/tv?x=1", "/tv#top", "/t v", `/tv\x`, "/%74v", "http://host/tv", "/tv:8080", "/é",
		"/" + strings.Repeat("a", 200),
		// The application's own pages: a request is only taken to carry the base path when it starts with it.
		"/api", "/static/x", "/explorer", "/login", "/healthz", "/backups/tv",
	} {
		_, err := Load(lookupFrom(map[string]string{"TIMEVAULT_BASE_PATH": bad}))
		if err == nil || !strings.Contains(err.Error(), "TIMEVAULT_BASE_PATH") {
			t.Errorf("TIMEVAULT_BASE_PATH=%q: error = %v, want a refusal that names the setting", bad, err)
		}
	}
	// A segment further in may be a name the application uses.
	if c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_BASE_PATH": "/apps/api"})); err != nil || c.BasePath != "/apps/api" {
		t.Errorf("/apps/api: %q, %v", c.BasePath, err)
	}
}

func TestBasePathIsDescribedAndLogged(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_BASE_PATH": "/tv/"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Describe().BasePath; got != "/tv" {
		t.Errorf("Describe().BasePath = %q", got)
	}
	attrs := c.LogAttrs()
	found := false
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i] == "base_path" && attrs[i+1] == "/tv" {
			found = true
		}
	}
	if !found {
		t.Errorf("the startup log does not name the base path: %v", attrs)
	}
}
