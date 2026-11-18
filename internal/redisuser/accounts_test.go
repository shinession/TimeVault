package redisuser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"timevault/internal/auth"
)

func ids(list []auth.Account) string {
	var out []string
	for _, a := range list {
		out = append(out, a.ID+"="+a.Name)
	}
	return strings.Join(out, ",")
}

func TestAccountsListsWhoCanSignInOnce(t *testing.T) {
	older := now.Add(-48 * time.Hour)

	moved := alice() // the e-mail changed: dash left the old record behind, with an older date
	stale := moved
	stale.email, stale.updated = "alice.old@example.test", older
	gone := user{ecode: "E4004", name: "Dan Ong", email: "dan.ong@example.test", typ: "user", password: "x1", active: true, updated: older}
	goneNow := gone // the newest record of E4004 is deactivated, so the account is not listed
	goneNow.email, goneNow.active, goneNow.updated = "dan.new@example.test", false, now
	inactive := user{ecode: "E3003", name: "Eve Koh", email: "eve.koh@example.test", typ: "user", password: "x2", active: false, updated: now}
	admin := dirAdmin()

	d, mr, _ := newDirectoryOf(t, moved, stale, gone, goneNow, inactive, admin)
	// A record that cannot be read, and a key that is no hash, are left out without failing the list.
	broken := user{ecode: "E5005", name: "Bad Record", email: "bad@example.test", typ: "user", password: "x3", active: true, updated: now}
	seedAs(mr, broken.key(), stored(broken, map[string][]byte{"ee_code": []byte("not a pickle")}))
	if _, err := mr.Push("app_user_e6006_list_x@example.test", "a"); err != nil {
		t.Fatal(err)
	}

	list, err := d.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(list), "E1001=Alice Tan,E2002=Carol Lim"; got != want {
		t.Errorf("accounts = %q, want %q", got, want)
	}
	for _, a := range list {
		if strings.Contains(a.Name, "@") || strings.Contains(a.ID, "@") {
			t.Errorf("the list carries more than the ID and the name of a person: %+v", a)
		}
	}
}

func TestAccountsAreOrderedByNameThenID(t *testing.T) {
	a := user{ecode: "E2", name: "zoe Tan", email: "z2@example.test", typ: "user", password: "p", active: true, updated: now}
	b := user{ecode: "E1", name: "Zoe Tan", email: "z1@example.test", typ: "user", password: "p", active: true, updated: now}
	c := user{ecode: "E3", name: "adam Lee", email: "a@example.test", typ: "user", password: "p", active: true, updated: now}
	d, _, _ := newDirectoryOf(t, a, b, c)
	list, err := d.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(list), "E3=adam Lee,E1=Zoe Tan,E2=zoe Tan"; got != want {
		t.Errorf("accounts = %q, want %q (names without regard to case, then the employee ID)", got, want)
	}
}

func TestAccountsAreKeptForAMinuteAndThenReadAgain(t *testing.T) {
	d, mr, _ := newDirectoryOf(t, alice())
	at := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return at }

	first, err := d.Accounts(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatalf("first = %v, %v", first, err)
	}
	first[0].Name = "changed by the caller" // the caller owns its copy

	// Within the minute Redis is not asked again: not even a new account shows, and a stopped Redis does not matter.
	seed(t, mr, dirAdmin())
	again, err := d.Accounts(context.Background())
	if err != nil || ids(again) != "E1001=Alice Tan" {
		t.Fatalf("within the minute = %v, %v; want the kept list, not changed by the caller", again, err)
	}
	mr.Close()
	if _, err := d.Accounts(context.Background()); err != nil {
		t.Errorf("a list that is kept needs no Redis: %v", err)
	}

	// After the minute Redis is asked, and when it cannot answer the list is unavailable, not stale.
	at = at.Add(accountsTTL + time.Second)
	if _, err := d.Accounts(context.Background()); !errors.Is(err, auth.ErrUnavailable) {
		t.Errorf("after the minute with Redis down = %v, want auth.ErrUnavailable", err)
	}
}

func TestAccountsSeeANewAccountAfterTheMinute(t *testing.T) {
	d, mr, _ := newDirectoryOf(t, alice())
	at := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return at }
	if _, err := d.Accounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	seed(t, mr, dirAdmin())
	at = at.Add(accountsTTL)
	list, err := d.Accounts(context.Background())
	if err != nil || ids(list) != "E1001=Alice Tan,E2002=Carol Lim" {
		t.Errorf("after the minute = %v, %v", ids(list), err)
	}
}

func TestAccountsOfAnEmptyDirectoryIsAnEmptyList(t *testing.T) {
	d := NewDirectory(openStore(t, Options{URL: "redis://" + miniredis.RunT(t).Addr()}), nil)
	list, err := d.Accounts(context.Background())
	if err != nil || len(list) != 0 {
		t.Errorf("accounts = %v, %v; want none", list, err)
	}
}

func TestTheManagerListsAndFindsAccounts(t *testing.T) {
	d, _, _ := newDirectoryOf(t, alice(), dirAdmin())
	m, err := auth.NewManager(auth.Options{Directory: d, SessionKey: []byte(strings.Repeat("k", 32)), TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	list, err := m.Accounts(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("Accounts = %v, %v", list, err)
	}
	if a, err := m.FindAccount(ctx, "e1001"); err != nil || a.ID != "E1001" || a.Name != "Alice Tan" {
		t.Errorf("FindAccount = %+v, %v", a, err)
	}
	if _, err := m.FindAccount(ctx, "E9999"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("FindAccount of an unknown ID = %v, want ErrInvalidCredentials", err)
	}

	// Without a directory that can list there is no list, and no way to check an ID.
	hash, err := auth.HashPassword("fake-pw-1")
	if err != nil {
		t.Fatal(err)
	}
	local, err := auth.NewManager(auth.Options{AdminUser: "admin", AdminPasswordHash: hash, SessionKey: []byte(strings.Repeat("k", 32)), TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.Accounts(ctx); !errors.Is(err, auth.ErrNoAccountList) {
		t.Errorf("Accounts without a directory = %v, want ErrNoAccountList", err)
	}
	if _, err := local.FindAccount(ctx, "E1001"); !errors.Is(err, auth.ErrNoAccountList) {
		t.Errorf("FindAccount without a directory = %v, want ErrNoAccountList", err)
	}
}
