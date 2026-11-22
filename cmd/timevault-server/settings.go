package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"syscall"

	"timevault/internal/config"
	"timevault/internal/db"
	"timevault/internal/sitesettings"
)

const settingsUsage = `usage: timevault-server settings <command>

commands:
  list                 every Site setting with the value in effect; * marks a stored value
  get <key>            the value in effect
  set <key> <value>    store a value (a value that equals the default is not stored)
  reset <key>          forget the stored value, so the default applies

This is the way back when the Site settings page cannot be reached, for example because a setting
locked everybody out. It works on the database that TIMEVAULT_DATABASE_URL names, and the service
has to be restarted to use what was changed.
`

// runSettings lists and changes the Site settings from the command line.
func runSettings(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(settingsUsage)
		return nil
	}
	boot, base, err := loadBootstrap()
	if err != nil {
		return err
	}
	if err := boot.ValidateDatabase(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Connect(ctx, boot.DatabaseURL, db.Options{MaxConns: 2})
	if err != nil {
		return err
	}
	defer pool.Close()
	store := sitesettings.New(pool)
	stored, err := store.Load(ctx)
	if err != nil {
		return fmt.Errorf("%w (has \"timevault-server migrate\" been run?)", err)
	}
	return settingsCommand(ctx, store, base, stored, args)
}

// settingsCommand carries out one command of runSettings on the stored values it was given.
func settingsCommand(ctx context.Context, store *sitesettings.Store, base config.Lookup, stored map[string]string, args []string) error {
	effective := config.SiteEffective(stored)
	switch args[0] {
	case "list":
		for _, s := range config.SiteSettings() {
			mark := " "
			if _, ok := stored[s.Key]; ok {
				mark = "*"
			}
			fmt.Printf("%s %-28s %s\n", mark, s.Key, effective[s.Key])
		}
		var unknown []string
		for k := range stored {
			if _, ok := config.SiteSettingByKey(k); !ok {
				unknown = append(unknown, k)
			}
		}
		sort.Strings(unknown)
		if len(unknown) > 0 {
			fmt.Printf("\nstored, but not Site settings of this version (ignored): %s\n", strings.Join(unknown, ", "))
		}
		return nil
	case "get":
		if len(args) != 2 {
			return errors.New("usage: timevault-server settings get <key>")
		}
		if _, ok := config.SiteSettingByKey(args[1]); !ok {
			return fmt.Errorf("%s is not a Site setting (see: timevault-server settings list)", args[1])
		}
		fmt.Println(effective[args[1]])
		return nil
	case "set":
		if len(args) != 3 {
			return errors.New("usage: timevault-server settings set <key> <value>")
		}
		return settingsSet(ctx, store, base, stored, args[1], args[2])
	case "reset":
		if len(args) != 2 {
			return errors.New("usage: timevault-server settings reset <key>")
		}
		if _, ok := config.SiteSettingByKey(args[1]); !ok {
			return fmt.Errorf("%s is not a Site setting (see: timevault-server settings list)", args[1])
		}
		if _, ok := stored[args[1]]; !ok {
			fmt.Printf("%s has no stored value: the default applies\n", args[1])
			return nil
		}
		if err := store.Save(ctx, nil, []string{args[1]}, "cli"); err != nil {
			return err
		}
		fmt.Printf("%s was reset to its default; restart the service to use it\n", args[1])
		return nil
	}
	return fmt.Errorf("unknown settings command %q\n\n%s", args[0], settingsUsage)
}

// settingsSet stores one value. It refuses a value that makes the settings worse: a problem that was there before
// and that is not about this setting does not count, so a value can still be set while another one is wrong; reset
// is the way out of that.
func settingsSet(ctx context.Context, store *sitesettings.Store, base config.Lookup, stored map[string]string, key, value string) error {
	setting, ok := config.SiteSettingByKey(key)
	if !ok {
		return fmt.Errorf("%s is not a Site setting (see: timevault-server settings list)", key)
	}
	norm, err := config.NormalizeSite(key, value)
	if err != nil {
		return err
	}
	next := map[string]string{}
	for k, v := range stored {
		if _, known := config.SiteSettingByKey(k); known {
			next[k] = v
		}
	}
	if norm == "" || norm == setting.Default {
		delete(next, key)
	} else {
		next[key] = norm
	}
	before := config.SiteProblems(base, knownOnly(stored))
	var worse []string
	for _, p := range config.SiteProblems(base, next) {
		if !slices.Contains(before, p) || strings.Contains(p, key) {
			worse = append(worse, p)
		}
	}
	if len(worse) > 0 {
		return fmt.Errorf("not saved: %s", strings.Join(worse, "; "))
	}
	if next[key] == "" {
		if err := store.Save(ctx, nil, []string{key}, "cli"); err != nil {
			return err
		}
		fmt.Printf("%s equals its default, so nothing is stored for it; restart the service to use it\n", key)
		return nil
	}
	if err := store.Save(ctx, map[string]string{key: next[key]}, nil, "cli"); err != nil {
		return err
	}
	fmt.Printf("%s was saved; restart the service to use it\n", key)
	return nil
}

func knownOnly(values map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		if _, ok := config.SiteSettingByKey(k); ok {
			out[k] = v
		}
	}
	return out
}
