package redisuser

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestCheckReport(t *testing.T) {
	mr := miniredis.RunT(t)
	manager := user{ecode: "E1004", name: "Mia Ng", email: "mia@example.test", typ: "manager", password: "fake-pw-4", active: true, updated: now}
	oldAlice := alice()
	oldAlice.email, oldAlice.updated = "alice.old@example.test", now.Add(-time.Hour)
	nopw := user{ecode: "E1007", name: "Nina Pw", email: "nina@example.test", typ: "user", active: true, updated: now}
	broken := user{ecode: "E1005", name: "Zed Broken", email: "zed@example.test", typ: "user", password: "fake-pw-5", active: true, updated: now}
	noType := user{ecode: "E1006", name: "Nia Notype", email: "nia@example.test", typ: "user", password: "fake-pw-6", active: true, updated: now}
	users := []user{alice(), bob(), chen(), manager, oldAlice, nopw}
	seed(t, mr, users...)
	seedAs(mr, broken.key(), stored(broken, map[string][]byte{"activated": zl(pyPickle("yes")), "UpdateDate": []byte("junk"), "phone_num": []byte("junk")}))
	seedAs(mr, noType.key(), stored(noType, map[string][]byte{"Type": nil}))
	const note = "app_user_zzz_note"
	if err := mr.Set(note, "a string"); err != nil {
		t.Fatal(err)
	}
	s := openStore(t, Options{URL: "redis://" + mr.Addr(), MaxScan: 3}) // MaxScan does not limit Check

	rep, err := s.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := mr.Keys()
	sort.Strings(keys)
	position := func(key string) int { return sort.SearchStrings(keys, key) + 1 }

	counts := map[string][2]int{
		"Keys": {rep.Keys, 9}, "Records": {rep.Records, 6}, "Unreadable": {rep.Unreadable, 2}, "NotHash": {rep.NotHash, 1}, "Vanished": {rep.Vanished, 0},
		"Active": {rep.Active, 5}, "Inactive": {rep.Inactive, 1}, "Admin": {rep.Admin, 1}, "User": {rep.User, 4}, "OtherType": {rep.OtherType, 1},
		"NoPassword": {rep.NoPassword, 1}, "DuplicateECodes": {rep.DuplicateECodes, 1}, "ProblemCount": {rep.ProblemCount, 5},
	}
	for name, c := range counts {
		if c[0] != c[1] {
			t.Errorf("%s = %d, want %d", name, c[0], c[1])
		}
	}
	fields := map[string]FieldStat{
		"ee_code":      {8, 8, 0},
		"Type":         {7, 7, 0}, // missing from one hash
		"activated":    {8, 7, 1},
		"UpdateDate":   {8, 7, 1},
		"phone_num":    {8, 7, 1},
		"HashPassword": {8, 8, 0}, // None is a valid, empty password
	}
	for name, want := range fields {
		if got := rep.Fields[name]; got != want {
			t.Errorf("field %s = %+v, want %+v", name, got, want)
		}
	}
	if len(rep.Fields) != len(fieldSpecs) {
		t.Errorf("%d fields in the report, want %d", len(rep.Fields), len(fieldSpecs))
	}
	got := map[Problem]bool{}
	for _, p := range rep.Problems {
		got[p] = true
	}
	for _, want := range []Problem{
		{position(broken.key()), "UpdateDate", errInflate.Error()},
		{position(broken.key()), "phone_num", errInflate.Error()},
		{position(broken.key()), "activated", errUnexpected.Error()},
		{position(noType.key()), "Type", "missing"},
		{position(note), "", "not a hash"},
	} {
		if !got[want] {
			t.Errorf("missing problem %+v in %+v", want, rep.Problems)
		}
	}
	if rep.Elapsed < 0 || rep.Elapsed > time.Minute {
		t.Errorf("Elapsed = %s", rep.Elapsed)
	}
}

func TestReportContainsNoPersonalData(t *testing.T) {
	mr := miniredis.RunT(t)
	damaged := bob()
	damaged.name, damaged.email = "Damaged Dan", "damaged.dan@example.test"
	users := []user{alice(), bob(), chen(), damaged}
	seed(t, mr, users[:3]...)
	seedAs(mr, damaged.key(), stored(damaged, map[string][]byte{"Email": []byte("junk")}))
	if err := mr.Set("app_user_zzz_note", "a string"); err != nil {
		t.Fatal(err)
	}
	s := openStore(t, Options{URL: "redis://" + mr.Addr()})
	rep, err := s.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProblemCount != 2 {
		t.Fatalf("the damaged record and the key that is not a hash should be reported, got %d problems", rep.ProblemCount)
	}
	var personal []string
	for _, u := range users {
		personal = append(personal, u.name, u.email, u.password, u.ecode, strings.ToLower(u.ecode), u.key(), strings.ToLower(u.name))
	}
	personal = append(personal, "app_user", "example.test", "pwned")
	for name, out := range map[string]string{"Format": rep.Format(), "%+v": fmt.Sprintf("%+v", rep), "%#v": fmt.Sprintf("%#v", rep)} {
		for _, p := range personal {
			if strings.Contains(out, p) {
				t.Errorf("%s contains %q:\n%s", name, p, out)
			}
		}
	}
	text := rep.Format()
	for _, want := range []string{"5 keys", "readable records: 3", "unreadable keys:  1", "not a hash:       1", "fields (present / decoded / failed):", "problems (2 of 2", "Email: pickle: invalid zlib stream", "key: not a hash"} {
		if !strings.Contains(text, want) {
			t.Errorf("Format lacks %q:\n%s", want, text)
		}
	}
}

func TestCheckScansEverything(t *testing.T) {
	f := newFakeRedis(t)
	f.setPageSize(7)
	for i := 0; i < 120; i++ {
		f.add(user{ecode: fmt.Sprintf("E7%03d", i), name: fmt.Sprintf("User %03d", i), email: fmt.Sprintf("user%03d@example.test", i), typ: "user", password: "pw", active: i%2 == 0, updated: now})
	}
	f.ghost("app_user_e9_ghost")
	f.wrongType("app_user_e9_note")
	s := openStore(t, Options{URL: f.url(), MaxScan: 5})
	rep, err := s.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Keys != 122 || rep.Records != 120 || rep.Vanished != 1 || rep.NotHash != 1 || rep.Active != 60 || rep.Inactive != 60 || rep.ProblemCount != 1 {
		t.Errorf("unexpected report:\n%s", rep.Format())
	}
	if n := f.count("HGETALL"); n != 122 {
		t.Errorf("%d HGETALL calls, want 122", n)
	}
	if n := f.count("KEYS"); n != 0 {
		t.Errorf("KEYS was used %d times", n)
	}
}

func TestCheckOfAnEmptyDirectory(t *testing.T) {
	s, _ := newDirectory(t)
	rep, err := s.Check(ctx)
	if err != nil || rep.Keys != 0 || rep.ProblemCount != 0 {
		t.Fatalf("got %+v, %v", rep, err)
	}
	if text := rep.Format(); !strings.Contains(text, "0 keys") || strings.Contains(text, "problems") {
		t.Errorf("unexpected text:\n%s", text)
	}
	if text := (Report{}).Format(); !strings.Contains(text, "0 keys") {
		t.Errorf("a zero Report must format: %s", text)
	}
}

func TestReportListsAtMostTwentyProblems(t *testing.T) {
	mr := miniredis.RunT(t)
	for i := 0; i < 30; i++ {
		u := user{ecode: fmt.Sprintf("E8%03d", i), name: fmt.Sprintf("Broken %03d", i), email: fmt.Sprintf("b%03d@example.test", i), typ: "user", password: "pw", active: true, updated: now}
		seedAs(mr, u.key(), stored(u, map[string][]byte{"Name": []byte("junk")}))
	}
	s := openStore(t, Options{URL: "redis://" + mr.Addr()})
	rep, err := s.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 20 || rep.ProblemCount != 30 || rep.Unreadable != 30 || rep.Fields["Name"].Failed != 30 {
		t.Errorf("%d problems listed, %d found, %d unreadable", len(rep.Problems), rep.ProblemCount, rep.Unreadable)
	}
	for i, p := range rep.Problems {
		if p.N != i+1 {
			t.Errorf("problem %d has ordinal %d", i, p.N)
		}
	}
	if !strings.Contains(rep.Format(), "problems (20 of 30") {
		t.Errorf("Format should say that the list is cut:\n%s", rep.Format())
	}
}
