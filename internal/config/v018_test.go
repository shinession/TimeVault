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
	if c.AvatarURL != "" {
		t.Errorf("without a server host there is no place to read the pictures from, but the address is %q", c.AvatarURL)
	}
}

func TestTheAddressOfTheUpdateIsNeverDerivedFromTheServerHost(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SERVER_HOST": "server.example.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.UpdateURL != "" {
		t.Errorf("the update address is %q although TIMEVAULT_UPDATE_URL is not set: updates must be asked for explicitly", c.UpdateURL)
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
		"/api/static/hr/{id}.webp":                      "/api/static/hr/{id}.webp",
		" /pics/{id}.png ":                              "/pics/{id}.png",
		"/avatar?user={id}&s=48":                        "/avatar?user={id}&s=48",
		"http://pics.example.test/api/static/{id}.webp": "http://pics.example.test/api/static/{id}.webp",
		"HTTPS://pics.example.test:8443/p/{id}.png":     "HTTPS://pics.example.test:8443/p/{id}.png",
		"http://prod_eng.co.example.test/hr/{id}.webp":  "http://prod_eng.co.example.test/hr/{id}.webp",
		"http://10.1.2.3/hr/{id}.webp":                  "http://10.1.2.3/hr/{id}.webp",
		"http://pics.example.test/photo?user={id}&s=48": "http://pics.example.test/photo?user={id}&s=48",
		"off":  "",
		"OFF":  "",
		"none": "",
	} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_AVATAR_URL": in}))
		if err != nil || c.AvatarURL != want {
			t.Errorf("TIMEVAULT_AVATAR_URL=%q -> %q, %v; want %q", in, c.AvatarURL, err, want)
		}
	}
	for _, bad := range []string{
		"//pics.example.test/{id}.webp", "pics/{id}.webp", `/pics\{id}.webp`,
		"/pics/photo.webp", "/pics/{id}/{id}.webp", "/pics/{id} .webp", "/pics/{id}.webp#x", `/pics/"{id}".webp`,
		"/pics/%zz{id}", "/" + strings.Repeat("a", 300) + "{id}",
		"ftp://pics.example.test/{id}.webp", "http:///{id}.webp", "http://{id}.example.test/photo",
		"http://pics.example.test/photo.webp", "http://pics.example.test/{id}.webp#x",
		"http://user:hunter2-secret@pics.example.test/{id}.webp", "http://pics.example.test:notaport/{id}.webp",
		"http://pics example.test/{id}.webp", "http://pics.example.test;x/{id}.webp", "http://[::1]/{id}.webp",
	} {
		_, err := Load(lookupFrom(map[string]string{"TIMEVAULT_AVATAR_URL": bad}))
		if err == nil || !strings.Contains(err.Error(), "TIMEVAULT_AVATAR_URL") {
			t.Errorf("TIMEVAULT_AVATAR_URL=%q: error = %v, want a refusal that names the setting", bad, err)
		} else if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("the error repeats a secret: %v", err)
		}
	}
}

// The pictures of the company accounts are kept by the application of the server this service runs on, so with the
// server host named the address needs no setting.
func TestAvatarURLDefaultsToThePicturesOnTheServerHost(t *testing.T) {
	for host, want := range map[string]string{
		"server.example.test":   "http://server.example.test/api/static/hr/{id}.webp",
		"prod_eng.example.test": "http://prod_eng.example.test/api/static/hr/{id}.webp",
		"10.1.2.3":              "http://10.1.2.3/api/static/hr/{id}.webp",
		"":                      "",
	} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SERVER_HOST": host}))
		if err != nil || c.AvatarURL != want {
			t.Errorf("TIMEVAULT_SERVER_HOST=%q -> %q, %v; want %q", host, c.AvatarURL, err, want)
		}
	}
	// An address that is written down wins over the host, and so does off.
	for in, want := range map[string]string{"off": "", "/pics/{id}.png": "/pics/{id}.png", "http://pics.example.test/{id}.png": "http://pics.example.test/{id}.png"} {
		c, err := Load(lookupFrom(map[string]string{"TIMEVAULT_SERVER_HOST": "server.example.test", "TIMEVAULT_AVATAR_URL": in}))
		if err != nil || c.AvatarURL != want {
			t.Errorf("TIMEVAULT_AVATAR_URL=%q with a server host -> %q, %v; want %q", in, c.AvatarURL, err, want)
		}
	}
}

func TestUpdateAndAvatarAreDescribedAndLogged(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{
		"TIMEVAULT_UPDATE_URL": "https://tools.example.test/release/", "TIMEVAULT_UPDATE_INTERVAL": "15m", "TIMEVAULT_UPDATE_RESTART": "exit",
		"TIMEVAULT_SERVER_HOST": "server.example.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	const picture = "http://server.example.test/api/static/hr/{id}.webp"
	d := c.Describe()
	if d.UpdateURL != "https://tools.example.test/release/" || d.UpdateInterval != 15*time.Minute || d.UpdateRestart != "exit" || d.AvatarURL != picture {
		t.Errorf("Describe() = %+v", d)
	}
	logged := ""
	attrs := c.LogAttrs()
	for i := 0; i+1 < len(attrs); i += 2 {
		logged += fmt.Sprintf("%v=%v ", attrs[i], attrs[i+1])
	}
	for _, want := range []string{"update_url=https://tools.example.test/release/", "update_interval=15m0s", "update_restart=exit", "avatar_url=" + picture} {
		if !strings.Contains(logged, want) {
			t.Errorf("the startup log lacks %q: %s", want, logged)
		}
	}
}
