package redisuser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

var ctx = context.Background()

func bob() user {
	return user{ecode: "E1002", name: "Bob Lee", email: "bob.lee@example.test", typ: "admin", password: "fake-pw-2", updated: now} // not activated
}

func chen() user {
	return user{ecode: "E1003", name: "陈小明", email: "chen.xm@example.test", typ: "user", password: "fake-密码-3", active: true, updated: now}
}

func TestLookupRules(t *testing.T) {
	s, _ := newDirectory(t, alice(), bob(), chen())
	cases := []struct {
		name, login string
		want        string // ee_code of the user found
		err         error
	}{
		{"ee_code", "E1001", "E1001", nil},
		{"ee_code in lower case", "e1001", "E1001", nil},
		{"ee_code with blanks around it", " \tE1001\n", "E1001", nil},
		{"name", "Alice Tan", "E1001", nil},
		{"name in another case", "alice tan", "", ErrNotFound},
		{"part of a name", "Alice", "", ErrNotFound},
		{"name that is not ASCII", "陈小明", "E1003", nil},
		{"e-mail", "alice.tan@example.test", "E1001", nil},
		{"e-mail in another case", "Alice.Tan@EXAMPLE.test", "E1001", nil},
		{"ee_code as the part before the @", "e1001@other.test", "E1001", nil},
		{"e-mail that is not the user's", "alice.tan@other.test", "", ErrNotFound},
		{"not activated", "bob.lee@example.test", "E1002", nil},
		{"unknown user", "nobody", "", ErrNotFound},
		{"e-mail of nobody", "nobody@example.test", "", ErrNotFound},
		{"empty", "", "", ErrNotFound},
		{"blank", "  \t ", "", ErrNotFound},
		{"nothing before the @", "@example.test", "", ErrNotFound},
		{"control character", "alice\x00", "", ErrNotFound},
		{"newline inside", "alice\ntan", "", ErrNotFound},
		{"DEL", "alice\x7f", "", ErrNotFound},
		{"254 characters", strings.Repeat("a", 254), "", ErrNotFound},
		{"255 characters", strings.Repeat("a", 255), "", ErrNotFound},
		{"invalid UTF-8", "\xff\xfe", "", ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := s.Lookup(ctx, c.login)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Errorf("got %v, %v; want %v", rec, err, c.err)
				}
				return
			}
			if err != nil || rec.ECode != c.want {
				t.Errorf("got %v, %v; want %s", rec, err, c.want)
			}
		})
	}
}

func TestLookupReturnsTheRecord(t *testing.T) {
	s, _ := newDirectory(t, alice(), bob())
	rec, err := s.Lookup(ctx, "Alice.Tan@Example.test")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Ref != alice().key() || rec.ECode != "E1001" || rec.Name != "Alice Tan" || rec.Email != "alice.tan@example.test" ||
		rec.Type != "user" || !rec.Activated || !rec.Updated.Equal(now) || rec.IsAdmin() {
		t.Errorf("got %#v", rec)
	}
	if !rec.HasPassword() || !rec.CheckPassword("fake-pw-1") || rec.CheckPassword("fake-pw-2") || rec.CheckPassword("FAKE-PW-1") {
		t.Error("the password does not check out")
	}
	rec, err = s.Lookup(ctx, "E1002")
	if err != nil || rec.Activated || !rec.IsAdmin() || !rec.CheckPassword("fake-pw-2") {
		t.Errorf("an inactive administrator must be returned as such: %#v, %v", rec, err)
	}
}

func TestLookupPicksTheNewestRecord(t *testing.T) {
	variant := func(email, password string, updated time.Time) user {
		u := alice()
		u.email, u.password, u.updated = email, password, updated
		return u
	}
	cases := []struct {
		name  string
		users []user
		want  string // password of the record that must win
	}{
		{"the key of the old record sorts first", []user{variant("alice.old@example.test", "old-pw", now.Add(-time.Hour)), alice()}, "fake-pw-1"},
		{"the key of the old record sorts last", []user{variant("zed@example.test", "old-pw", now.Add(-time.Hour)), alice()}, "fake-pw-1"},
		{"a record without a date is the oldest", []user{variant("zed@example.test", "old-pw", time.Time{}), alice()}, "fake-pw-1"},
		{"a record without a date, other keys", []user{variant("alice.old@example.test", "old-pw", time.Time{}), alice()}, "fake-pw-1"},
		{"same time: the smaller key wins", []user{variant("b@example.test", "pw-b", now), variant("a@example.test", "pw-a", now)}, "pw-a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newDirectory(t, c.users...)
			for i := 0; i < 5; i++ {
				for _, login := range []string{"E1001", "Alice Tan"} {
					rec, err := s.Lookup(ctx, login)
					if err != nil || !rec.CheckPassword(c.want) {
						t.Fatalf("Lookup(%q) = %#v, %v", login, rec, err)
					}
				}
			}
		})
	}
	s, _ := newDirectory(t, variant("alice.old@example.test", "old-pw", now.Add(-time.Hour)), alice())
	if rec, err := s.Lookup(ctx, "alice.old@example.test"); err != nil || !rec.CheckPassword("old-pw") {
		t.Errorf("the old e-mail address finds the old record, as in dash: %#v, %v", rec, err)
	}
}

func TestLookupAmbiguity(t *testing.T) {
	pat := func(ecode, email string, updated time.Time) user {
		return user{ecode: ecode, name: "Pat Kim", email: email, typ: "user", password: "pw-" + ecode, active: true, updated: updated}
	}
	t.Run("one name, two ee_codes", func(t *testing.T) {
		s, _ := newDirectory(t, pat("E2001", "pat.kim@a.test", now), pat("E2002", "pat.kim@b.test", now))
		if _, err := s.Lookup(ctx, "Pat Kim"); !errors.Is(err, ErrAmbiguous) {
			t.Errorf("got %v", err)
		}
		for login, want := range map[string]string{"E2001": "E2001", "pat.kim@b.test": "E2002"} {
			if rec, err := s.Lookup(ctx, login); err != nil || rec.ECode != want {
				t.Errorf("Lookup(%q) = %v, %v", login, rec, err)
			}
		}
	})
	t.Run("the name of one is the ee_code of another", func(t *testing.T) {
		tricky := user{ecode: "E3001", name: "E3002", email: "tricky@example.test", typ: "user", password: "x", active: true, updated: now}
		other := user{ecode: "E3002", name: "Other", email: "other@example.test", typ: "user", password: "y", active: true, updated: now}
		s, _ := newDirectory(t, tricky, other)
		if _, err := s.Lookup(ctx, "E3002"); !errors.Is(err, ErrAmbiguous) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("same name and e-mail under two ee_codes: the newest wins, as in dash", func(t *testing.T) {
		s, _ := newDirectory(t, pat("E2001", "pat.kim@a.test", now), pat("E2003", "pat.kim@a.test", now.Add(time.Hour)))
		if rec, err := s.Lookup(ctx, "Pat Kim"); err != nil || rec.ECode != "E2003" {
			t.Errorf("got %v, %v", rec, err)
		}
	})
}

// A name may contain what is a wildcard in a Redis pattern; the user is still found.
func TestLookupFindsNamesWithGlobCharacters(t *testing.T) {
	odd := user{ecode: "E4001", name: `A*B?[x]\z`, email: "odd@example.test", typ: "user", password: "pw", active: true, updated: now}
	s, _ := newDirectory(t, odd, alice())
	if rec, err := s.Lookup(ctx, odd.name); err != nil || rec.ECode != "E4001" {
		t.Errorf("got %v, %v", rec, err)
	}
	if _, err := s.Lookup(ctx, "A*B?[x]"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a part of a name is not the name: %v", err)
	}
}

// literalOf reads the end of a pattern the way Redis does: it returns the text
// before a final unescaped *, and fails if anything else in it is special.
func literalOf(rest string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			if i++; i == len(rest) {
				return "", false
			}
			b.WriteByte(rest[i])
		case '*':
			return b.String(), i == len(rest)-1
		case '?', '[':
			return "", false
		default:
			b.WriteByte(rest[i])
		}
	}
	return "", false
}

// scanPatterns returns the MATCH argument of every first SCAN call. go-redis
// sends the keywords in lower case.
func scanPatterns(f *fakeRedis) []string {
	const head, tail = "scan 0 match ", " count 1000"
	var patterns []string
	for _, c := range f.matching("SCAN 0 ") {
		if lower := strings.ToLower(c); strings.HasPrefix(lower, head) && strings.HasSuffix(lower, tail) {
			patterns = append(patterns, c[len(head):len(c)-len(tail)])
		}
	}
	return patterns
}

func TestUserInputNeverActsAsAPattern(t *testing.T) {
	inputs := []string{"*", "?", "[a-z]", `\`, "a*b", "a?ice", "[", "]", `alice\`, `\*`, "*@example.test", `[^a]*`, "E1001 [x]", "ünï*cödé"}
	for _, input := range inputs {
		for _, call := range []string{"Lookup", "Get"} {
			f := newFakeRedis(t)
			f.add(alice(), bob())
			s := openStore(t, Options{URL: f.url()})
			var err error
			if call == "Lookup" {
				_, err = s.Lookup(ctx, input)
			} else {
				_, err = s.Get(ctx, "", input)
			}
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s(%q): %v", call, input, err)
			}
			patterns := scanPatterns(f)
			if len(patterns) != 1 {
				t.Errorf("%s(%q): %d SCAN calls", call, input, len(patterns))
				continue
			}
			rest, ok := strings.CutPrefix(patterns[0], "app_user_")
			if call == "Lookup" {
				var star bool
				rest, star = strings.CutPrefix(rest, "*")
				ok = ok && star
			}
			literal, valid := literalOf(rest)
			wantLiteral := strings.ToLower(strings.SplitN(input, "@", 2)[0])
			if call == "Get" {
				wantLiteral = strings.ToLower(input) + "_"
			}
			if !ok || !valid || literal != wantLiteral {
				t.Errorf("%s(%q) scanned for %q, which reads as %q (%v, %v), want the literal %q", call, input, patterns[0], literal, ok, valid, wantLiteral)
			}
			if f.count("KEYS") != 0 {
				t.Errorf("%s(%q) used KEYS", call, input)
			}
		}
	}
	f := newFakeRedis(t)
	s := openStore(t, Options{URL: f.url()})
	for _, login := range []string{"", "   ", "@example.test", "a\x00b", strings.Repeat("a", 255)} {
		s.Lookup(ctx, login)
	}
	if n := f.count("SCAN"); n != 0 {
		t.Errorf("a login that is rejected up front must not reach Redis; %d SCAN calls", n)
	}
}

func TestOtherKeysAreIgnored(t *testing.T) {
	t.Run("a key that is not a hash", func(t *testing.T) {
		s, mr := newDirectory(t, alice())
		if err := mr.Set("app_user_e1001_alice tan_note", "plain string"); err != nil {
			t.Fatal(err)
		}
		if rec, err := s.Lookup(ctx, "E1001"); err != nil || rec.ECode != "E1001" {
			t.Errorf("got %v, %v", rec, err)
		}
		if _, err := s.Lookup(ctx, "note"); !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a key that vanished after SCAN listed it", func(t *testing.T) {
		f := newFakeRedis(t)
		f.add(alice())
		f.ghost("app_user_e9_ghost_ghost@example.test")
		f.wrongType("app_user_e9_ghost_note")
		s := openStore(t, Options{URL: f.url()})
		if _, err := s.Lookup(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v", err)
		}
		if rec, err := s.Lookup(ctx, "E1001"); err != nil || rec.ECode != "E1001" {
			t.Errorf("got %v, %v", rec, err)
		}
	})
}

func TestUnreadableRecords(t *testing.T) {
	hostile := zl([]byte("cos\nsystem\n(S'echo pwned'\ntR."))
	damage := map[string]map[string][]byte{
		"required field missing": {"ee_code": nil},
		"password missing":       {"HashPassword": nil},
		"not zlib":               {"Name": []byte("not zlib at all")},
		"truncated pickle":       {"Name": zl(pyPickle("Alice Tan")[:8])},
		"a pickle that calls os": {"Type": hostile},
		"activated is a string":  {"activated": zl(pyPickle("yes"))},
		"password is a bool":     {"HashPassword": zl(pyPickle(true))},
		"password is NaN":        {"HashPassword": vector(t, "float_nan")},
		"e-mail is None":         {"Email": zl(pyPickle(nil))},
	}
	for name, fields := range damage {
		t.Run(name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			seedAs(mr, alice().key(), stored(alice(), fields))
			s := openStore(t, Options{URL: "redis://" + mr.Addr()})
			_, err := s.Lookup(ctx, "E1001")
			if !errors.Is(err, ErrUnreadable) {
				t.Fatalf("got %v", err)
			}
			text := strings.ToLower(err.Error())
			for _, leak := range []string{"e1001", "alice", "example.test", "app_user", "echo"} {
				if strings.Contains(text, leak) {
					t.Errorf("the error %q contains %q", err, leak)
				}
			}
			if !strings.Contains(text, "1 candidate") {
				t.Errorf("the error should count the candidates: %q", err)
			}
		})
	}

	t.Run("a readable match wins over an unreadable candidate", func(t *testing.T) {
		old := alice()
		old.email = "alice.old@example.test"
		mr := miniredis.RunT(t)
		seedAs(mr, old.key(), stored(old, map[string][]byte{"Name": []byte("junk")}))
		seed(t, mr, alice())
		s := openStore(t, Options{URL: "redis://" + mr.Addr()})
		if rec, err := s.Lookup(ctx, "E1001"); err != nil || rec.Email != alice().email {
			t.Errorf("got %v, %v", rec, err)
		}
	})
	t.Run("an unreadable candidate that is not the login still blocks a miss", func(t *testing.T) {
		mr := miniredis.RunT(t)
		other := user{ecode: "E5001", name: "Bobby Tables", email: "bobby@example.test", typ: "user", password: "x", active: true, updated: now}
		seedAs(mr, other.key(), stored(other, map[string][]byte{"activated": []byte("junk")}))
		s := openStore(t, Options{URL: "redis://" + mr.Addr()})
		if _, err := s.Lookup(ctx, "bobby"); !errors.Is(err, ErrUnreadable) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("an unreadable date only loses the time", func(t *testing.T) {
		mr := miniredis.RunT(t)
		seedAs(mr, alice().key(), stored(alice(), map[string][]byte{"UpdateDate": []byte("junk")}))
		s := openStore(t, Options{URL: "redis://" + mr.Addr()})
		rec, err := s.Lookup(ctx, "E1001")
		if err != nil || !rec.Updated.IsZero() || !rec.CheckPassword("fake-pw-1") {
			t.Errorf("got %#v, %v", rec, err)
		}
		if rep, err := s.Check(ctx); err != nil || rep.Fields["UpdateDate"].Failed != 1 {
			t.Errorf("an unreadable date is reported: %+v, %v", rep.Fields["UpdateDate"], err)
		}
	})
	t.Run("a missing date, None or NaT, is no problem", func(t *testing.T) {
		for _, name := range []string{"none", "ts_nat"} {
			mr := miniredis.RunT(t)
			seedAs(mr, alice().key(), stored(alice(), map[string][]byte{"UpdateDate": vector(t, name)}))
			s := openStore(t, Options{URL: "redis://" + mr.Addr()})
			rec, err := s.Lookup(ctx, "E1001")
			if err != nil || !rec.Updated.IsZero() {
				t.Errorf("%s: got %#v, %v", name, rec, err)
			}
			if rep, err := s.Check(ctx); err != nil || rep.Fields["UpdateDate"].Failed != 0 || rep.ProblemCount != 0 {
				t.Errorf("%s: a missing date is not a problem: %+v, %v", name, rep.Fields["UpdateDate"], err)
			}
		}
	})
}

func TestStoredPasswords(t *testing.T) {
	cases := []struct {
		name   string
		stored []byte
		ok     []string // passwords that must pass
		no     []string // and that must not
	}{
		{"text", zl(pyPickle("fake-pw-1")), []string{"fake-pw-1"}, []string{"", "0", " fake-pw-1", "FAKE-PW-1"}},
		{"text with blanks", zl(pyPickle("  pw  ")), []string{"  pw  "}, []string{"pw"}},
		{"the text 0", zl(pyPickle("0")), []string{"0"}, []string{"", "00"}},
		{"an int", vector(t, "int_65536"), []string{"65536"}, []string{"", "0"}},
		{"a whole float, as Python prints it", vector(t, "float_123456"), []string{"123456.0"}, []string{"123456", "123456.00"}},
		{"a numpy int", vector(t, "np_int64"), []string{"123456"}, []string{"0"}},
		{"None", vector(t, "none"), nil, []string{"", "0", "None", "none", "null", "nan", "NaN", "False"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			seedAs(mr, alice().key(), stored(alice(), map[string][]byte{"HashPassword": c.stored}))
			s := openStore(t, Options{URL: "redis://" + mr.Addr()})
			rec, err := s.Lookup(ctx, "E1001")
			if err != nil {
				t.Fatal(err)
			}
			if rec.HasPassword() != (len(c.ok) > 0) {
				t.Errorf("HasPassword = %v", rec.HasPassword())
			}
			for _, p := range c.ok {
				if !rec.CheckPassword(p) {
					t.Errorf("%q must pass", p)
				}
			}
			for _, p := range c.no {
				if rec.CheckPassword(p) {
					t.Errorf("%q must not pass", p)
				}
			}
		})
	}
}

func TestGet(t *testing.T) {
	s, mr := newDirectory(t, alice(), bob())
	cases := []struct {
		name, ref, ecode string
		want             string // ee_code, or "" for ErrNotFound
	}{
		{"by key", alice().key(), "E1001", "E1001"},
		{"by key, ee_code in lower case", alice().key(), "e1001", "E1001"},
		{"by key alone", alice().key(), "", "E1001"},
		{"the key of another user falls back to the ee_code", bob().key(), "E1001", "E1001"},
		{"key is gone: by ee_code", "app_user_e1001_gone_gone@example.test", "E1001", "E1001"},
		{"no key: by ee_code", "", "e1001", "E1001"},
		{"key and ee_code unknown", "app_user_e9_x_y", "E9", ""},
		{"nothing at all", "", "", ""},
		{"a key outside the directory is not read", "mes_user_dict", "", ""},
		{"a key with a control character", "app_user_e1001\n", "", ""},
		{"ee_code that is a pattern", "", "E1*", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := s.Get(ctx, c.ref, c.ecode)
			if c.want == "" {
				if !errors.Is(err, ErrNotFound) {
					t.Errorf("got %v, %v", rec, err)
				}
				return
			}
			if err != nil || rec.ECode != c.want {
				t.Errorf("got %v, %v", rec, err)
			}
		})
	}

	t.Run("the key moved after an e-mail change", func(t *testing.T) {
		moved := alice()
		moved.email, moved.password, moved.updated = "alice.new@example.test", "new-pw", now.Add(time.Hour)
		older := alice()
		older.email, older.password, older.updated = "alice.older@example.test", "older-pw", now.Add(-time.Hour)
		seed(t, mr, moved, older)
		mr.Del(alice().key())
		rec, err := s.Get(ctx, alice().key(), "E1001")
		if err != nil || rec.Ref != moved.key() || !rec.CheckPassword("new-pw") {
			t.Errorf("got %#v, %v", rec, err)
		}
		if rec, err = s.Get(ctx, older.key(), "E1001"); err != nil || rec.Ref != older.key() {
			t.Errorf("an existing key is used as it is: %#v, %v", rec, err)
		}
	})
	t.Run("an ee_code that is the start of another", func(t *testing.T) {
		long := user{ecode: "E1_X", name: "Long Code", email: "long@example.test", typ: "user", password: "x", active: true, updated: now}
		short := user{ecode: "E1", name: "Short Code", email: "short@example.test", typ: "user", password: "y", active: true, updated: now}
		s, _ := newDirectory(t, long, short)
		if rec, err := s.Get(ctx, "", "e1"); err != nil || rec.ECode != "E1" {
			t.Errorf("got %v, %v", rec, err)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		mr := miniredis.RunT(t)
		seedAs(mr, alice().key(), stored(alice(), map[string][]byte{"activated": []byte("junk")}))
		s := openStore(t, Options{URL: "redis://" + mr.Addr()})
		if _, err := s.Get(ctx, alice().key(), "E1001"); !errors.Is(err, ErrUnreadable) {
			t.Errorf("got %v", err)
		}
	})
}

func TestGetReadsOnlyKeysOfTheDirectory(t *testing.T) {
	f := newFakeRedis(t)
	f.add(alice())
	s := openStore(t, Options{URL: f.url()})
	for _, ref := range []string{"mes_user_dict", "../app_user_e1001", "", "app_user_e1001\x00"} {
		s.Get(ctx, ref, "")
	}
	if n := f.count("HGETALL"); n != 0 {
		t.Errorf("HGETALL ran %d times: %v", n, f.matching("HGETALL"))
	}
}

func TestRedisDown(t *testing.T) {
	s, mr := newDirectory(t, alice())
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mr.Close()
	calls := map[string]func() error{
		"Ping":   func() error { return s.Ping(ctx) },
		"Lookup": func() error { _, err := s.Lookup(ctx, "E1001"); return err },
		"Get":    func() error { _, err := s.Get(ctx, alice().key(), "E1001"); return err },
		"Check":  func() error { _, err := s.Check(ctx); return err },
	}
	for name, call := range calls {
		start := time.Now()
		err := call()
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: got %v", name, err)
		}
		if errors.Is(err, ErrNotFound) {
			t.Errorf("%s: a dead Redis must not look like an unknown user", name)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s took %s", name, d)
		}
	}
}

func TestCanceledContext(t *testing.T) {
	s, _ := newDirectory(t, alice())
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Lookup(canceled, "E1001"); !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Errorf("got %v", err)
	}
}

func TestTimeout(t *testing.T) {
	f := newFakeRedis(t)
	f.hangPings()
	s := openStore(t, Options{URL: f.url(), Timeout: 150 * time.Millisecond})
	start := time.Now()
	err := s.Ping(ctx)
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("got %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Ping took %s with a timeout of 150ms", d)
	}
}

func TestWrongPasswordInURL(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("right-pw")
	seed(t, mr, alice())
	cases := []struct {
		name, url string
		ok        bool
	}{
		{"right password", "redis://:right-pw@" + mr.Addr(), true},
		{"right password for the default user", "redis://default:right-pw@" + mr.Addr(), true},
		{"wrong password", "redis://:wrong-pw@" + mr.Addr(), false},
		{"no password", "redis://" + mr.Addr(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openStore(t, Options{URL: c.url})
			errs := []error{s.Ping(ctx)}
			_, err := s.Lookup(ctx, "E1001")
			errs = append(errs, err)
			for _, err := range errs {
				switch {
				case c.ok && err != nil:
					t.Errorf("got %v", err)
				case !c.ok && !errors.Is(err, ErrUnavailable):
					t.Errorf("got %v", err)
				}
				if err != nil && (strings.Contains(err.Error(), "wrong-pw") || strings.Contains(err.Error(), "right-pw")) {
					t.Errorf("the password is in the error: %v", err)
				}
			}
		})
	}
}

// A server that does not know AUTH repeats the arguments of the command in its error.
func TestPasswordEchoedByTheServerIsMasked(t *testing.T) {
	f := newFakeRedis(t)
	s := openStore(t, Options{URL: strings.Replace(f.url(), "redis://", "redis://:s3cret-pw@", 1)})
	err := s.Ping(ctx)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "s3cret-pw") {
		t.Errorf("the password is in the error: %v", err)
	}
	if !strings.Contains(err.Error(), "***") || !strings.Contains(strings.ToLower(err.Error()), "auth") {
		t.Errorf("the error should still say what failed: %v", err)
	}
}

// The servers dash may run on, Redis 3.x and 5.x on Windows, have no HELLO,
// CLIENT or ACL; and the scan must page, deduplicate and stop at MaxScan.
func TestOldServersPagedScanAndMaxScan(t *testing.T) {
	var users []user
	for i := 1; i <= 11; i++ {
		users = append(users, user{ecode: fmt.Sprintf("E60%02d", i), name: fmt.Sprintf("Pat %02d", i), email: fmt.Sprintf("pat%02d@example.test", i), typ: "user", password: "pw", active: true, updated: now})
	}
	target := user{ecode: "E6999", name: "Pat", email: "pat@example.test", typ: "user", password: "pw", active: true, updated: now}
	early := user{ecode: "E6000", name: "Pat", email: "pat.early@example.test", typ: "user", password: "pw", active: true, updated: now}

	newServer := func(pageSize int) *fakeRedis {
		f := newFakeRedis(t)
		f.setPageSize(pageSize)
		f.add(users...)
		f.add(target)
		return f
	}

	t.Run("pages and duplicates", func(t *testing.T) {
		f := newServer(3)
		s := openStore(t, Options{URL: f.url()})
		rec, err := s.Lookup(ctx, "Pat") // the target sorts last, on the fourth page
		if err != nil || rec.ECode != "E6999" {
			t.Fatalf("got %v, %v", rec, err)
		}
		if n := f.count("SCAN"); n != 4 {
			t.Errorf("%d SCAN calls, want 4: %v", n, f.matching("SCAN"))
		}
		if n := f.count("HGETALL"); n != 12 {
			t.Errorf("%d HGETALL calls, want 12 (a key listed twice is read once)", n)
		}
		for _, c := range f.matching("SCAN") {
			if !strings.HasSuffix(strings.ToLower(c), " count 1000") {
				t.Errorf("SCAN without the COUNT hint: %s", c)
			}
		}
	})
	t.Run("no HELLO 3, CLIENT, AUTH or KEYS", func(t *testing.T) {
		f := newServer(0)
		s := openStore(t, Options{URL: f.url()})
		if err := s.Ping(ctx); err != nil {
			t.Fatalf("Ping: %v", err)
		}
		if _, err := s.Lookup(ctx, "Pat"); err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		if first := f.matching("HELLO"); len(first) == 0 || first[0] != "HELLO 2" {
			t.Errorf("expected a RESP2 HELLO first, got %v", first)
		}
		for _, banned := range []string{"CLIENT", "AUTH", "KEYS", "SELECT", "HELLO 3"} {
			if n := f.count(banned); n != 0 {
				t.Errorf("%d %s commands: %v", n, banned, f.matching(banned))
			}
		}
	})
	t.Run("MaxScan stops the scan", func(t *testing.T) {
		f := newServer(3)
		f.add(early)
		s := openStore(t, Options{URL: f.url(), MaxScan: 7})
		// A login that matches more keys than MaxScan is not answered from some of them: that
		// would hide a second employee behind the first.
		if rec, err := s.Lookup(ctx, "Pat"); !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("got %v, %v", rec, err)
		}
		if n := f.count("SCAN"); n != 3 {
			t.Errorf("%d SCAN calls, want 3: %v", n, f.matching("SCAN"))
		}
		if n := f.count("HGETALL"); n != 0 {
			t.Errorf("%d HGETALL calls, want none once the scan is too broad", n)
		}
		f.setPageSize(0)
		if _, err := s.Lookup(ctx, "Pat 11"); err != nil {
			t.Errorf("a login that matches few keys is not limited: %v", err)
		}
	})
	t.Run("a login that matches too many keys is ambiguous", func(t *testing.T) {
		f := newServer(3)
		s := openStore(t, Options{URL: f.url(), MaxScan: 7})
		if _, err := s.Lookup(ctx, "Pat"); !errors.Is(err, ErrAmbiguous) {
			t.Errorf("got %v", err)
		}
	})
}
