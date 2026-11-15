package config

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The template people copy to .env must parse and may only name settings the service reads.
func TestExampleFileNamesOnlySettingsTheServerReads(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "configs", "timevault.example.env"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseEnv(bytes.NewReader(text)); err != nil {
		t.Fatalf("the example file does not parse: %v", err)
	}
	asked := map[string]bool{envFileSetter: true}
	_, _ = Load(func(key string) (string, bool) { asked[key] = true; return "", false })
	named := regexp.MustCompile(`(?m)^#?\s*(TIMEVAULT_[A-Z_]+)=`).FindAllStringSubmatch(string(text), -1)
	if len(named) < 10 {
		t.Fatalf("found only %d settings in the example file", len(named))
	}
	for _, m := range named {
		if !asked[m[1]] {
			t.Errorf("the example file names %s, which the service does not read", m[1])
		}
	}
}

func TestParseEnv(t *testing.T) {
	src := "\xef\xbb\xbf# comment\r\n" +
		"\r\n" +
		"TIMEVAULT_REPOSITORY_PATH=D:\\Backups\\TimeVault\r\n" +
		"export TIMEVAULT_LISTEN_ADDR = 127.0.0.1:8660 \n" +
		"TIMEVAULT_REDIS_URL=\"redis://:pa#ss@127.0.0.1:6379/0\"\n" +
		"TIMEVAULT_ADMIN_USER='root'\n" +
		"TIMEVAULT_LOG_LEVEL=debug # not a comment\n" +
		"TIMEVAULT_EMPTY=\n" +
		"TIMEVAULT_DUP=first\n" +
		"TIMEVAULT_DUP=last\n"
	got, err := ParseEnv(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"TIMEVAULT_REPOSITORY_PATH": `D:\Backups\TimeVault`,
		"TIMEVAULT_LISTEN_ADDR":     "127.0.0.1:8660",
		"TIMEVAULT_REDIS_URL":       "redis://:pa#ss@127.0.0.1:6379/0",
		"TIMEVAULT_ADMIN_USER":      "root",
		"TIMEVAULT_LOG_LEVEL":       "debug # not a comment",
		"TIMEVAULT_EMPTY":           "",
		"TIMEVAULT_DUP":             "last",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d keys, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// A bad line is reported by number only: the line may be a password.
func TestParseEnvErrorsNeverEchoTheLine(t *testing.T) {
	for _, src := range []string{
		"TIMEVAULT_OK=1\nhunter2-is-my-password\n",
		"1BAD=hunter2\n",
		"BAD KEY=hunter2\n",
		"=hunter2\n",
	} {
		_, err := ParseEnv(strings.NewReader(src))
		if err == nil {
			t.Errorf("%q: want an error", src)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "line ") {
			t.Errorf("%q: error %q must name the line and nothing else", src, err)
		}
	}
	if _, err := ParseEnv(strings.NewReader(strings.Repeat("A=1\n", 400_000))); err == nil {
		t.Error("a file over 1 MiB must be refused")
	}
}

func writeEnv(t *testing.T, dir, text string) string {
	t.Helper()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadEnvLayersTheFileUnderTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := writeEnv(t, dir, "TIMEVAULT_LISTEN_ADDR=127.0.0.1:9001\nTIMEVAULT_LOG_LEVEL=debug\n")
	lookup, found, err := loadEnv(lookupFrom(map[string]string{"TIMEVAULT_LOG_LEVEL": "warn"}), []string{dir})
	if err != nil || found != path {
		t.Fatalf("loadEnv = %q, %v", found, err)
	}
	c, err := Load(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:9001" {
		t.Errorf("the file must supply a setting that is not in the environment, got %q", c.ListenAddr)
	}
	if c.LogLevel.String() != "WARN" {
		t.Errorf("the environment must win over the file, level = %v", c.LogLevel)
	}
}

func TestLoadEnvFindsTheFileInOrder(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	if _, found, err := loadEnv(lookupFrom(nil), []string{first, second}); err != nil || found != "" {
		t.Fatalf("no file anywhere: %q, %v", found, err)
	}
	writeEnv(t, second, "TIMEVAULT_LOG_LEVEL=warn\n")
	if _, found, _ := loadEnv(lookupFrom(nil), []string{first, second}); found != filepath.Join(second, ".env") {
		t.Errorf("found %q, want the second directory's file", found)
	}
	writeEnv(t, first, "TIMEVAULT_LOG_LEVEL=error\n")
	lookup, found, _ := loadEnv(lookupFrom(nil), []string{first, second})
	if found != filepath.Join(first, ".env") {
		t.Errorf("found %q, want the first directory's file", found)
	}
	if v, _ := lookup("TIMEVAULT_LOG_LEVEL"); v != "error" {
		t.Errorf("level = %q", v)
	}
}

func TestLoadEnvExplicitFile(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.env")
	if err := os.WriteFile(custom, []byte("TIMEVAULT_LOG_LEVEL=warn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, dir, "TIMEVAULT_LOG_LEVEL=error\n")
	lookup, found, err := loadEnv(lookupFrom(map[string]string{"TIMEVAULT_ENV_FILE": custom}), []string{dir})
	if err != nil || found != custom {
		t.Fatalf("loadEnv = %q, %v", found, err)
	}
	if v, _ := lookup("TIMEVAULT_LOG_LEVEL"); v != "warn" {
		t.Errorf("the explicit file must be used, level = %q", v)
	}
	// Asking for a file that is not there is a mistake, not a reason to fall back silently.
	for _, bad := range []string{filepath.Join(dir, "missing.env"), dir} {
		if _, _, err := loadEnv(lookupFrom(map[string]string{"TIMEVAULT_ENV_FILE": bad}), []string{dir}); err == nil {
			t.Errorf("TIMEVAULT_ENV_FILE=%q must be an error", bad)
		}
	}
	// A malformed file stops the start and names the file, never a value.
	writeEnv(t, dir, "this is not valid hunter2\n")
	_, _, err = loadEnv(lookupFrom(nil), []string{dir})
	if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), ".env") {
		t.Errorf("malformed file error = %v", err)
	}
}
