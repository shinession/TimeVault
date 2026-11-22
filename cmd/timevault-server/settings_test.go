package main

import (
	"context"
	"strings"
	"testing"

	"timevault/internal/db/dbtest"
	"timevault/internal/sitesettings"
)

func noEnv(string) (string, bool) { return "", false }

func TestSettingsCommandStoresAndResets(t *testing.T) {
	ctx := context.Background()
	store := sitesettings.New(dbtest.New(t))
	run := func(args ...string) error {
		stored, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return settingsCommand(ctx, store, noEnv, stored, args)
	}
	value := func(key string) (string, bool) {
		stored, _ := store.Load(ctx)
		v, ok := stored[key]
		return v, ok
	}

	if err := run("set", "job_workers", "4"); err != nil {
		t.Fatal(err)
	}
	if v, _ := value("job_workers"); v != "4" {
		t.Errorf("job_workers = %q", v)
	}
	// A value that equals the default is not stored: it removes what was stored.
	if err := run("set", "job_workers", "2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := value("job_workers"); ok {
		t.Error("a value that equals the default was stored")
	}
	if err := run("set", "cookie_secure", "TRUE"); err != nil {
		t.Fatal(err)
	}
	if v, _ := value("cookie_secure"); v != "true" {
		t.Errorf("cookie_secure = %q, want the normalised value", v)
	}
	if err := run("reset", "cookie_secure"); err != nil {
		t.Fatal(err)
	}
	if _, ok := value("cookie_secure"); ok {
		t.Error("reset left the value")
	}
	// What is not stored can be reset, read and listed without an error.
	for _, args := range [][]string{{"reset", "cookie_secure"}, {"get", "job_workers"}, {"list"}} {
		if err := run(args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}

	for name, args := range map[string][]string{
		"unknown key":   {"set", "colour", "blue"},
		"bad number":    {"set", "job_workers", "many"},
		"out of range":  {"set", "job_workers", "99"},
		"missing value": {"set", "job_workers"},
		"unknown reset": {"reset", "colour"},
		"unknown get":   {"get", "colour"},
		"unknown verb":  {"frobnicate"},
	} {
		if err := run(args...); err == nil {
			t.Errorf("%s: %v succeeded", name, args)
		}
	}
	if stored, _ := store.Load(ctx); len(stored) != 0 {
		t.Errorf("a refused command stored %v", stored)
	}
}

// The way back must work when a setting is wrong: reset is not blocked by it, and a value for another setting can be set.
func TestSettingsCommandIsAWayBackFromABadSetting(t *testing.T) {
	ctx := context.Background()
	store := sitesettings.New(dbtest.New(t))
	if err := store.Save(ctx, map[string]string{"session_ttl": "1s"}, nil, "test"); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		stored, _ := store.Load(ctx)
		return settingsCommand(ctx, store, noEnv, stored, args)
	}
	if err := run("set", "job_workers", "3"); err != nil {
		t.Fatalf("a value for another setting was refused because session_ttl is wrong: %v", err)
	}
	err := run("set", "session_ttl", "2s")
	if err == nil || !strings.Contains(err.Error(), "session_ttl must be between 5m and 720h") {
		t.Errorf("another bad value for the bad setting = %v", err)
	}
	if err := run("reset", "session_ttl"); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Load(ctx)
	if _, ok := stored["session_ttl"]; ok || stored["job_workers"] != "3" {
		t.Errorf("stored = %v", stored)
	}
}
