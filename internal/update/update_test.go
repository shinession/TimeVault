package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	envHelper        = "TV_UPDATE_HELPER"
	envHelperVersion = "TV_UPDATE_HELPER_VERSION"
	envHelperRaw     = "TV_UPDATE_HELPER_RAW"
	envHelperConfig  = "TV_UPDATE_HELPER_CONFIG"
	envHelperExit    = "TV_UPDATE_HELPER_EXIT"
	envHelperDir     = "TV_UPDATE_HELPER_DIR"
)

// TestMain lets the test program play the programs the updater runs: one that holds still until its input is closed,
// one that answers the commands of timevault-server that the updater uses, and a supervisor with the program that
// stops when the supervisor is gone.
func TestMain(m *testing.M) {
	switch os.Getenv(envHelper) {
	case "":
		os.Exit(m.Run())
	case "hold":
		fmt.Println("running")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "launcher":
		exe, _ := os.Executable()
		_ = os.Setenv(envHelper, "watched")
		os.Exit(Supervise(exe, nil, nil, slog.New(slog.DiscardHandler)))
	case "watched":
		dir := os.Getenv(envHelperDir)
		_ = os.WriteFile(filepath.Join(dir, "started"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		StopWithLauncher(os.Stdin, func() {
			_ = os.WriteFile(filepath.Join(dir, "stopped"), nil, 0o600)
		}, slog.New(slog.DiscardHandler))
		os.Exit(0)
	default:
		os.Exit(fakeProgram(os.Args[1:]))
	}
}

func fakeProgram(args []string) int {
	if len(args) != 1 {
		return 2
	}
	switch args[0] {
	case "version":
		if raw := os.Getenv(envHelperRaw); raw != "" {
			fmt.Println(raw)
			return 0
		}
		fmt.Println("timevault-server " + os.Getenv(envHelperVersion))
		return 0
	case "check-config":
		switch os.Getenv(envHelperConfig) {
		case "bad":
			fmt.Fprintln(os.Stderr, "error: TIMEVAULT_DB_PASSWORD is not set")
			return 1
		case "hang":
			time.Sleep(time.Minute)
		case "flood":
			line := strings.Repeat("x", 100)
			for i := 0; i < 1<<16; i++ {
				fmt.Fprintln(os.Stderr, line)
			}
			return 1
		}
		return 0
	case "exit":
		n, _ := strconv.Atoi(os.Getenv(envHelperExit))
		return n
	}
	return 2
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// waitFor waits until cond holds; the conditions it is used for are reached within milliseconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
