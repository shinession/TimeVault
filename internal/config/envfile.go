package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	envFileName   = ".env"
	envFileSetter = "TIMEVAULT_ENV_FILE"
	maxEnvFile    = 1 << 20
)

// ParseEnv reads KEY=VALUE lines.
//
// Blank lines and lines starting with # are ignored, and so is a leading
// "export ". A value is taken literally up to the end of the line, so a path
// with backslashes or a password containing # needs no escaping; one pair of
// matching single or double quotes around the value is removed. The last
// assignment of a key wins. Errors name the line only: a line may hold a secret.
func ParseEnv(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxEnvFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxEnvFile {
		return nil, errors.New("the file is larger than 1 MiB")
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	out := map[string]string{}
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validEnvKey(key) {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", i+1)
		}
		out[key] = unquoteEnv(strings.TrimSpace(value))
	}
	return out, nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		letter := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func unquoteEnv(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// LoadEnv layers a .env file under the process environment: a variable that
// is set in the environment always wins over the file. The file is
// TIMEVAULT_ENV_FILE when that is set (it must exist), otherwise .env in the
// working directory, otherwise .env next to the executable. The returned path
// is empty when there is no file.
func LoadEnv(base Lookup) (Lookup, string, error) {
	var dirs []string
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	return loadEnv(base, dirs)
}

func loadEnv(base Lookup, dirs []string) (Lookup, string, error) {
	path, err := findEnvFile(base, dirs)
	if err != nil || path == "" {
		return base, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	values, err := ParseEnv(f)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	return func(key string) (string, bool) {
		if v, ok := base(key); ok {
			return v, true
		}
		v, ok := values[key]
		return v, ok
	}, path, nil
}

// EnvOverrides names, sorted, the TIMEVAULT_ settings that the file at path sets
// to one value and the process environment to another. The environment wins, so
// the value in the file is not in effect, and editing the file changes nothing:
// a variable left behind in a terminal is an easy way to end up there. Only
// names are returned, never values. An empty path, or a file that cannot be read
// (LoadEnv has already reported that), gives nil.
func EnvOverrides(base Lookup, path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	values, err := ParseEnv(f)
	if err != nil {
		return nil
	}
	var names []string
	for key, fileValue := range values {
		if envValue, ok := base(key); ok && strings.HasPrefix(key, "TIMEVAULT_") && envValue != fileValue {
			names = append(names, key)
		}
	}
	slices.Sort(names)
	return names
}

func findEnvFile(base Lookup, dirs []string) (string, error) {
	if p, ok := base(envFileSetter); ok && strings.TrimSpace(p) != "" {
		p = strings.TrimSpace(p)
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return "", fmt.Errorf("%s %q is not a readable file", envFileSetter, p)
		}
		return filepath.Abs(p)
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, envFileName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", nil
}
