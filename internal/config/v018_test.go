package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUpdateAndAvatarDefaults(t *testing.T) {
	c, err := Load(lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.UpdateURL != "" || c.UpdateInterval != 10*time.Minute || c.UpdateRestart != "supervise" {
		t.Errorf("update defaults: %q, %v, %q", c.UpdateURL, c.UpdateInterval, c.UpdateRestart)
	}
	if c.AvatarURL != "/api/static/hr/{id}.webp" {
		t.Errorf("avatar default: %q", c.AvatarURL)
	}
}

func TestUpdateURL(t *testing.T) {
	folder := t.TempDir()
	good := []string{
		"https://tools.example.test/tv/release/",
		"http://tools.example.test:8080/release/timevault.json",
		folder,
		filepath.Join(folder, "timevault.json"),
	}
	if runtime.GOOS == "windows" {
		good = append(good, `\\server\share\release`, `D:\TimeVault\release`)
	}
	for _, in := range good {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_UPDATE_URL": "  " + in + " "}))
		if err != nil || c.UpdateURL != in {
			t.Errorf("TIMEVAULT_UPDATE_URL=%q -> %q, %v", in, c.UpdateURL, err)
		}
	}
	for _, bad := range []string{
		"release", `release\timevault.json`, "ftp://host/release/", "file:///c:/release", "http:///release",
		"https://host/release?token=abc", "https://host/release?", "https://host/release#top",
		"https://user:hunter2-secret@host/release/",
	} {
		_, err := Load(lookupFrom(map[string]string{"TIMEVAULT_UPDATE_URL": bad}))
		if err == nil || !strings.Contains(err.Error(), "TIMEVAULT_UPDATE_URL") {
			t.Errorf("TIMEVAULT_UPDATE_URL=%q: error = %v, want a refusal that names the setting", bad, err)
		} else if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "token=abc") {
			t.Errorf("the error repeats a secret: %v", err)
		}
	}
}

func TestUpdateIntervalAndRestart(t *testing.T) {
	for in, want := range map[string]time.Duration{"1m": time.Minute, "10m": 10 * time.Minute, "24h": 24 * time.Hour} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_UPDATE_INTERVAL": in}))
		if err != nil || c.UpdateInterval != want {
			t.Errorf("TIMEVAULT_UPDATE_INTERVAL=%q -> %v, %v", in, c.UpdateInterval, err)
		}
	}
	for _, bad := range []string{"30s", "25h", "0s", "-5m", "often"} {
		if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_UPDATE_INTERVAL": bad})); err == nil || !strings.Contains(err.Error(), "TIMEVAULT_UPDATE_INTERVAL") {
			t.Errorf("TIMEVAULT_UPDATE_INTERVAL=%q: error = %v", bad, err)
		}
	}
	for in, want := range map[string]string{"supervise": "supervise", "EXIT": "exit", " exit ": "exit"} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_UPDATE_RESTART": in}))
		if err != nil || c.UpdateRestart != want {
			t.Errorf("TIMEVAULT_UPDATE_RESTART=%q -> %q, %v", in, c.UpdateRestart, err)
		}
	}
	if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_UPDATE_RESTART": "restart"})); err == nil || !strings.Contains(err.Error(), "TIMEVAULT_UPDATE_RESTART") {
		t.Errorf("an unknown restart mode was accepted: %v", err)
	}
}

func TestAutomaticUpdatesNeedAutoMigrate(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_UPDATE_URL": "https://tools.example.test/release/"}))
	if err != nil {
		t.Fatal(err)
	}
	c.DatabaseURL = "postgres://u:p@127.0.0.1/db"
	c.RepositoryPath = t.TempDir()
	c.AuthDisabled = true
	err = c.ValidateServe()
	if err == nil || !strings.Contains(err.Error(), "TIMEVAULT_UPDATE_URL") || !strings.Contains(err.Error(), "TIMEVAULT_AUTO_MIGRATE") {
		t.Fatalf("an updating service that does not migrate was accepted: %v", err)
	}
	c.AutoMigrate = true
	if err := c.ValidateServe(); err != nil {
		t.Fatalf("ValidateServe: %v", err)
	}
	c.UpdateURL, c.AutoMigrate = "", false
	if err := c.ValidateServe(); err != nil {
		t.Fatalf("a service that does not update itself needs no migration setting: %v", err)
	}
}

func TestAvatarURL(t *testing.T) {
	for in, want := range map[string]string{
		"/api/static/hr/{id}.webp": "/api/static/hr/{id}.webp",
		" /pics/{id}.png ":         "/pics/{id}.png",
		"/avatar?user={id}&s=48":   "/avatar?user={id}&s=48",
		"off":                      "",
		"OFF":                      "",
		"none":                     "",
	} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_AVATAR_URL": in}))
		if err != nil || c.AvatarURL != want {
			t.Errorf("TIMEVAULT_AVATAR_URL=%q -> %q, %v; want %q", in, c.AvatarURL, err, want)
		}
	}
	for _, bad := range []string{
		"http://pics.example.test/{id}.webp", "//pics.example.test/{id}.webp", "pics/{id}.webp", `/pics\{id}.webp`,
		"/pics/photo.webp", "/pics/{id}/{id}.webp", "/pics/{id} .webp", "/pics/{id}.webp#x", `/pics/"{id}".webp`,
		"/pics/%zz{id}", "/" + strings.Repeat("a", 300) + "{id}",
	} {
		if _, err := Load(lookupFrom(map[string]string{"TIMEVAULT_AVATAR_URL": bad})); err == nil || !strings.Contains(err.Error(), "TIMEVAULT_AVATAR_URL") {
			t.Errorf("TIMEVAULT_AVATAR_URL=%q: error = %v, want a refusal that names the setting", bad, err)
		}
	}
}

func TestUpdateAndAvatarAreDescribedAndLogged(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_UPDATE_URL": "https://tools.example.test/release/", "TIMEVAULT_UPDATE_INTERVAL": "15m", "TIMEVAULT_UPDATE_RESTART": "exit",
	}))
	if err != nil {
		t.Fatal(err)
	}
	d := c.Describe()
	if d.UpdateURL != "https://tools.example.test/release/" || d.UpdateInterval != 15*time.Minute || d.UpdateRestart != "exit" || d.AvatarURL != "/api/static/hr/{id}.webp" {
		t.Errorf("Describe() = %+v", d)
	}
	logged := ""
	attrs := c.LogAttrs()
	for i := 0; i+1 < len(attrs); i += 2 {
		logged += fmt.Sprintf("%v=%v ", attrs[i], attrs[i+1])
	}
	for _, want := range []string{"update_url=https://tools.example.test/release/", "update_interval=15m0s", "update_restart=exit", "avatar_url=/api/static/hr/{id}.webp"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the startup log lacks %q: %s", want, logged)
		}
	}
}
