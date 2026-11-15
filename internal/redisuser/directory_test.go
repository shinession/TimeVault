package redisuser

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"timevault/internal/auth"
)

var _ auth.Directory = (*Directory)(nil)

func dirAdmin() user {
	return user{ecode: "E2002", name: "Carol Lim", email: "carol.lim@example.test", typ: "admin", password: "fake-pw-2", active: true, updated: now}
}

// newDirectoryOf starts an in-process Redis with the users and a Directory on it; the log can be read back.
func newDirectoryOf(t *testing.T, users ...user) (*Directory, *miniredis.Miniredis, *bytes.Buffer) {
	t.Helper()
	store, mr := newDirectory(t, users...)
	logs := &bytes.Buffer{}
	return NewDirectory(store, slog.New(slog.NewTextHandler(logs, nil))), mr, logs
}

func setField(mr *miniredis.Miniredis, key, field string, value any) {
	mr.HSet(key, field, string(zl(pyPickle(value))))
}

func TestDirectoryAuthenticatesByEmployeeIDNameOrEmail(t *testing.T) {
	d, _, _ := newDirectoryOf(t, alice())
	want := auth.Identity{ID: "E1001", Name: "Alice Tan", Role: auth.RoleUser, Ref: alice().key()}
	for _, login := range []string{"E1001", "e1001", "Alice Tan", "alice.tan@example.test", "ALICE.TAN@EXAMPLE.TEST"} {
		got, err := d.Authenticate(context.Background(), login, "fake-pw-1")
		if err != nil {
			t.Errorf("%q: %v", login, err)
			continue
		}
		if got != want {
			t.Errorf("%q: identity = %+v, want %+v", login, got, want)
		}
	}
}

func TestDirectoryRoleFollowsTheTypeOfTheAccount(t *testing.T) {
	admin := dirAdmin()
	odd := user{ecode: "E3003", name: "Odd Type", email: "odd@example.test", typ: "manager", password: "fake-pw-3", active: true, updated: now}
	shouting := user{ecode: "E3004", name: "Loud Admin", email: "loud@example.test", typ: " ADMIN ", password: "fake-pw-4", active: true, updated: now}
	d, _, _ := newDirectoryOf(t, alice(), admin, odd, shouting)
	for _, c := range []struct {
		login, password string
		role            auth.Role
	}{
		{"E1001", "fake-pw-1", auth.RoleUser}, {"E2002", "fake-pw-2", auth.RoleAdmin},
		{"E3003", "fake-pw-3", auth.RoleUser}, {"E3004", "fake-pw-4", auth.RoleAdmin},
	} {
		got, err := d.Authenticate(context.Background(), c.login, c.password)
		if err != nil || got.Role != c.role {
			t.Errorf("%s: role %q, error %v, want %q", c.login, got.Role, err, c.role)
		}
	}
}

func TestDirectoryRefusesWithoutSayingWhy(t *testing.T) {
	inactive := user{ecode: "E4004", name: "Dan Off", email: "dan@example.test", typ: "user", password: "fake-pw-4", active: false, updated: now}
	noPassword := user{ecode: "E5005", name: "Eve None", email: "eve@example.test", typ: "user", password: "", active: true, updated: now}
	d, _, _ := newDirectoryOf(t, alice(), inactive, noPassword)
	for name, c := range map[string][2]string{
		"wrong password":  {"E1001", "nope"},
		"empty password":  {"E1001", ""},
		"unknown account": {"E9999", "fake-pw-1"},
		"not activated":   {"E4004", "fake-pw-4"},
		// dash turns a missing password into 0; here a record without a password never signs in.
		"no password stored":      {"E5005", ""},
		"no password stored, '0'": {"E5005", "0"},
	} {
		_, err := d.Authenticate(context.Background(), c[0], c[1])
		if !errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUnavailable) {
			t.Errorf("%s: error = %v, want invalid credentials", name, err)
		}
	}
}

func TestDirectoryRefusesAnAmbiguousLoginAndLogsNoPersonalData(t *testing.T) {
	first, second := alice(), alice()
	second.ecode, second.email = "E7007", "other@example.test" // same name, another employee ID
	d, _, logs := newDirectoryOf(t, first, second)
	_, err := d.Authenticate(context.Background(), "Alice Tan", "fake-pw-1")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("error = %v, want invalid credentials", err)
	}
	text := logs.String()
	if !strings.Contains(text, "more than one account") {
		t.Errorf("the refusal is not logged: %q", text)
	}
	for _, secret := range []string{"Alice", "fake-pw-1", "E1001", "E7007", "example.test"} {
		if strings.Contains(text, secret) {
			t.Errorf("the log contains %q: %s", secret, text)
		}
	}
	// The person typing an employee ID is not ambiguous.
	if _, err := d.Authenticate(context.Background(), "E1001", "fake-pw-1"); err != nil {
		t.Errorf("sign-in by employee ID: %v", err)
	}
}

func TestDirectoryRefusesAnUnreadableRecordAndPointsToTheCheck(t *testing.T) {
	u := alice()
	mr := miniredis.RunT(t)
	seedAs(mr, u.key(), stored(u, map[string][]byte{"ee_code": []byte("not a pickle")}))
	logs := &bytes.Buffer{}
	d := NewDirectory(openStore(t, Options{URL: "redis://" + mr.Addr()}), slog.New(slog.NewTextHandler(logs, nil)))
	_, err := d.Authenticate(context.Background(), "Alice Tan", "fake-pw-1")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("error = %v, want invalid credentials", err)
	}
	if !strings.Contains(logs.String(), "check-redis-users") || strings.Contains(logs.String(), "fake-pw-1") {
		t.Errorf("log = %q", logs.String())
	}
}

func TestDirectoryUnavailableIsNotAWrongPassword(t *testing.T) {
	d, mr, _ := newDirectoryOf(t, alice())
	id, err := d.Authenticate(context.Background(), "E1001", "fake-pw-1")
	if err != nil {
		t.Fatal(err)
	}
	mr.Close()
	if _, err := d.Authenticate(context.Background(), "E1001", "fake-pw-1"); !errors.Is(err, auth.ErrUnavailable) || errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("Authenticate while Redis is down: %v", err)
	}
	if _, err := d.Refresh(context.Background(), id); !errors.Is(err, auth.ErrUnavailable) || errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("Refresh while Redis is down: %v", err)
	}
	if err := d.Ping(context.Background()); err == nil {
		t.Error("Ping while Redis is down succeeded")
	}
}

func TestDirectoryErrorsNeverContainThePasswordOfTheURL(t *testing.T) {
	store := openStore(t, Options{URL: "redis://:hunter2-secret@127.0.0.1:1/0", Timeout: 300 * time.Millisecond})
	d := NewDirectory(store, nil)
	_, err := d.Authenticate(context.Background(), "E1001", "fake-pw-1")
	if !errors.Is(err, auth.ErrUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if strings.Contains(err.Error(), "hunter2-secret") || strings.Contains(d.Addr(), "hunter2-secret") {
		t.Errorf("the password of the URL is in %q / %q", err, d.Addr())
	}
}

func TestDirectoryRefreshFollowsTheRecord(t *testing.T) {
	u := alice()
	d, mr, _ := newDirectoryOf(t, u)
	id, err := d.Authenticate(context.Background(), "E1001", "fake-pw-1")
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() (auth.Identity, error) { return d.Refresh(context.Background(), id) }

	if got, err := refresh(); err != nil || got != id {
		t.Fatalf("unchanged record: %+v, %v", got, err)
	}
	setField(mr, u.key(), "Type", "admin")
	if got, err := refresh(); err != nil || got.Role != auth.RoleAdmin {
		t.Errorf("promoted: %+v, %v", got, err)
	}
	setField(mr, u.key(), "Name", "Alice T.")
	if got, err := refresh(); err != nil || got.Name != "Alice T." {
		t.Errorf("renamed: %+v, %v", got, err)
	}

	// The other application moves a record to a new key when the e-mail address changes.
	moved := u
	moved.email = "alice.new@example.test"
	mr.Del(u.key())
	seed(t, mr, moved)
	if got, err := refresh(); err != nil || got.ID != "E1001" || got.Ref != moved.key() {
		t.Errorf("moved: %+v, %v", got, err)
	}

	setField(mr, moved.key(), "activated", false)
	if _, err := refresh(); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("deactivated: %v", err)
	}
	mr.Del(moved.key())
	if _, err := refresh(); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("deleted: %v", err)
	}
}

type stoppedClock struct{ t time.Time }

func (c *stoppedClock) Now() time.Time { return c.t }

// The Directory as the sign-in manager uses it: a deactivation takes effect once the answer expired.
func TestDirectoryBehindTheSessionManager(t *testing.T) {
	d, mr, _ := newDirectoryOf(t, alice())
	clock := &stoppedClock{t: time.Now()}
	m, err := auth.NewManager(auth.Options{Directory: d, SessionKey: bytes.Repeat([]byte("k"), 32), Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := m.Login(ctx, "Alice Tan", "fake-pw-1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := m.StartSession(httptest.NewRecorder(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.Verify(ctx, session); err != nil || got.Role != auth.RoleUser || got.Name != "Alice Tan" {
		t.Fatalf("Verify = %+v, %v", got, err)
	}
	setField(mr, alice().key(), "activated", false)
	if _, err := m.Verify(ctx, session); err != nil {
		t.Errorf("the answer of the directory holds for a while, got %v", err)
	}
	clock.t = clock.t.Add(2 * time.Minute)
	if _, err := m.Verify(ctx, session); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("a deactivated account after the answer expired: %v", err)
	}
	if _, err := m.Login(ctx, "Alice Tan", "fake-pw-1"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("a deactivated account signs in: %v", err)
	}
}
