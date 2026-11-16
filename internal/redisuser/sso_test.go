package redisuser

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"timevault/internal/auth"
)

// pyDictPickle writes what pickle.dumps({...}) writes with protocol 4 for a dict of str keys and values that are
// str, float, int or None, the shape of a result of the single sign-on service.
func pyDictPickle(pairs ...any) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x80, 0x04, '}', 0x94, '('})
	str := func(s string) {
		b.WriteByte(0x8c)
		b.WriteByte(byte(len(s)))
		b.WriteString(s)
		b.WriteByte(0x94)
	}
	for i := 0; i < len(pairs); i += 2 {
		str(pairs[i].(string))
		switch v := pairs[i+1].(type) {
		case string:
			str(v)
		case float64:
			b.WriteByte('G')
			var f [8]byte
			binary.BigEndian.PutUint64(f[:], math.Float64bits(v))
			b.Write(f[:])
		case int:
			b.WriteByte('K')
			b.WriteByte(byte(v))
		case nil:
			b.WriteByte('N')
		default:
			panic("unsupported value")
		}
	}
	b.WriteString("u.")
	return b.Bytes()
}

const ssoTestToken = "abcdefghijklmnopqrstuvwx"

func TestSSOResultReadsWhatTheSignOnServiceWrote(t *testing.T) {
	d, mr, _ := newDirectoryOf(t, alice())
	mr.Set(ssoPrefix+ssoTestToken, string(zl(pyDictPickle(
		"status", "ok", "windows_user", `CO\E1001`, "ee_code", "E1001", "ip", "10.1.2.3", "ts", 1763256000.25))))

	got, err := d.SSOResult(context.Background(), ssoTestToken)
	if err != nil {
		t.Fatal(err)
	}
	want := auth.SSOResult{Status: "ok", ECode: "E1001", WindowsUser: `CO\E1001`, IP: "10.1.2.3", At: time.Unix(1763256000, 250_000_000).UTC()}
	if got.Status != want.Status || got.ECode != want.ECode || got.WindowsUser != want.WindowsUser || got.IP != want.IP || !got.At.Equal(want.At) {
		t.Fatalf("SSOResult = %+v, want %+v", got, want)
	}
	if !mr.Exists(ssoPrefix + ssoTestToken) {
		t.Error("the directory is read-only: the result must stay for the sign-on service to expire")
	}
}

func TestSSOResultOfABrowserWithoutAWindowsUser(t *testing.T) {
	d, mr, _ := newDirectoryOf(t)
	mr.Set(ssoPrefix+ssoTestToken, string(zl(pyDictPickle("status", "no_user", "ip", "10.1.2.3", "ts", 1763256000.0))))
	got, err := d.SSOResult(context.Background(), ssoTestToken)
	if err != nil || got.Status != "no_user" || got.ECode != "" || got.IP != "10.1.2.3" {
		t.Fatalf("SSOResult = %+v, %v", got, err)
	}
}

func TestSSOResultIsPendingUntilTheSignOnServiceWrites(t *testing.T) {
	d, mr, _ := newDirectoryOf(t)
	if _, err := d.SSOResult(context.Background(), ssoTestToken); !errors.Is(err, auth.ErrSSOPending) {
		t.Fatalf("no key yet = %v, want pending", err)
	}
	// What cannot be a token of the sign-on service is never looked up.
	for _, token := range []string{"", "short", strings.Repeat("a", 41), "UPPERCASE0123456789ABCDEF", "has space 0123456789abc", "e*"} {
		if _, err := d.SSOResult(context.Background(), token); !errors.Is(err, auth.ErrSSOPending) {
			t.Errorf("token %q = %v, want pending", token, err)
		}
	}
	mr.Set(ssoPrefix+"abcdefghijklmnopqrstuvw*", "x")
	if _, err := d.SSOResult(context.Background(), "abcdefghijklmnopqrstuvw*"); !errors.Is(err, auth.ErrSSOPending) {
		t.Errorf("a token with a glob character must not reach Redis, got %v", err)
	}
}

func TestSSOResultRefusesWhatIsNotAResult(t *testing.T) {
	cases := map[string][]byte{
		"not zlib":          []byte("plain text"),
		"not a dict":        zl([]byte{0x80, 0x04, 'N', '.'}),
		"no status":         zl(pyDictPickle("ee_code", "E1001")),
		"ts is text":        zl(pyDictPickle("status", "ok", "ts", "yesterday")),
		"ts is NaN":         zl(pyDictPickle("status", "ok", "ts", math.NaN())),
		"status is a float": zl(pyDictPickle("status", 1.5)),
		"control character": zl(pyDictPickle("status", "ok", "ee_code", "E1001\x00")),
		"huge name":         zl(pyDictPickle("status", "ok", "windows_user", strings.Repeat("x", 255))),
	}
	for name, value := range cases {
		d, mr, logs := newDirectoryOf(t)
		mr.Set(ssoPrefix+ssoTestToken, string(value))
		if _, err := d.SSOResult(context.Background(), ssoTestToken); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("%s: error = %v, want invalid credentials", name, err)
		}
		if !strings.Contains(logs.String(), "single sign-on refused") {
			t.Errorf("%s: the refusal is not logged: %q", name, logs.String())
		}
	}
}

func TestSSOResultWhileRedisIsDownIsUnavailable(t *testing.T) {
	d, mr, _ := newDirectoryOf(t)
	mr.Close()
	if _, err := d.SSOResult(context.Background(), ssoTestToken); !errors.Is(err, auth.ErrUnavailable) || errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("error = %v, want unavailable", err)
	}
}

func TestIdentifyFindsAnActiveAccountByEmployeeID(t *testing.T) {
	inactive := user{ecode: "E4004", name: "Gone Person", email: "gone@example.test", typ: "user", password: "fake-pw-4", active: false, updated: now}
	d, mr, _ := newDirectoryOf(t, alice(), dirAdmin(), inactive)

	want := auth.Identity{ID: "E1001", Name: "Alice Tan", Role: auth.RoleUser, Ref: alice().key()}
	for _, ecode := range []string{"E1001", "e1001", " E1001 "} {
		if got, err := d.Identify(context.Background(), ecode); err != nil || got != want {
			t.Errorf("Identify(%q) = %+v, %v, want %+v", ecode, got, err, want)
		}
	}
	if got, err := d.Identify(context.Background(), "E2002"); err != nil || got.Role != auth.RoleAdmin {
		t.Errorf("Identify of an administrator = %+v, %v", got, err)
	}
	for _, ecode := range []string{"E4004", "E9999", "", "E10*", "*", "alice.tan@example.test", "Alice Tan"} {
		if _, err := d.Identify(context.Background(), ecode); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("Identify(%q) = %v, want invalid credentials: only an active employee ID signs in", ecode, err)
		}
	}
	mr.Close()
	if _, err := d.Identify(context.Background(), "E1001"); !errors.Is(err, auth.ErrUnavailable) || errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("Identify while Redis is down = %v, want unavailable", err)
	}
}
