package access

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func TestLevelsBuildOnEachOther(t *testing.T) {
	if !(None < Browse && Browse < Backup && Backup < Full) {
		t.Fatal("the levels must be ordered none < browse < backup < full")
	}
	for l, name := range map[Level]string{None: "none", Browse: "browse", Backup: "backup", Full: "full", Level(99): "none", Level(-1): "none"} {
		if l.String() != name {
			t.Errorf("Level(%d).String() = %q, want %q", int(l), l.String(), name)
		}
	}
	for name, want := range map[string]Level{"browse": Browse, "backup": Backup, "full": Full} {
		if got, ok := ParseLevel(name); !ok || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", name, got, ok)
		}
	}
	for _, name := range []string{"", "none", "Browse", "admin", "guest"} {
		if got, ok := ParseLevel(name); ok || got != None {
			t.Errorf("ParseLevel(%q) = %v, %v; only browse, backup and full can be granted", name, got, ok)
		}
	}
	if !Full.AtLeast(Backup) || !Backup.AtLeast(Browse) || Browse.AtLeast(Backup) || None.AtLeast(Browse) {
		t.Error("AtLeast does not follow the order of the levels")
	}
}

func TestLevelFor(t *testing.T) {
	list := List{
		{Kind: Guest, Level: Browse},
		{Kind: Everyone, Level: Backup},
		{Kind: User, Account: "E1001", Level: Full},
		{Kind: User, Account: "E1002", Level: Browse},
	}
	anon := Who{}
	alice := Who{SignedIn: true, Account: "E3003"}
	owner := Who{SignedIn: true, Account: "e1001"} // the case of an ID never matters
	other := Who{SignedIn: true, Account: "E1002"}
	admin := Who{SignedIn: true, Account: "E9999", Admin: true}

	cases := []struct {
		name string
		list List
		who  Who
		want Level
	}{
		{"guests browse", list, anon, Browse},
		{"everyone is more than guests", list, alice, Backup},
		{"a named account is more than everyone", list, owner, Full},
		{"a named account never lowers what everyone has", list, other, Backup},
		{"an administrator has Full without being listed", nil, admin, Full},
		{"an administrator has Full whatever the list says", List{{Kind: Guest, Level: Browse}}, admin, Full},
		{"an empty list shows nothing to guests", nil, anon, None},
		{"an empty list shows nothing to accounts", nil, alice, None},
		{"everyone does not apply to guests", List{{Kind: Everyone, Level: Full}}, anon, None},
		{"a named account does not apply to guests", List{{Kind: User, Account: "E1001", Level: Full}}, Who{Account: "E1001"}, None},
		{"a named account does not apply to another account", List{{Kind: User, Account: "E1001", Level: Full}}, alice, None},
		{"an account that is only named can see the folder", List{{Kind: User, Account: "E1001", Level: Browse}}, owner, Browse},
		{"Admin without a sign-in is nobody", nil, Who{Admin: true}, None},
		{"an unknown kind gives nothing", List{{Kind: "group", Level: Full}}, alice, None},
	}
	for _, c := range cases {
		if got := c.list.LevelFor(c.who); got != c.want {
			t.Errorf("%s: level = %v, want %v", c.name, got, c.want)
		}
	}
}

// Adding a grant can only give a visitor more, never less.
func TestAGrantNeverLowersALevel(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	accounts := []string{"E1", "E2", "E3"}
	randomEntry := func() Entry {
		switch rng.IntN(3) {
		case 0:
			return Entry{Kind: Guest, Level: Browse}
		case 1:
			return Entry{Kind: Everyone, Level: Level(1 + rng.IntN(3))}
		}
		return Entry{Kind: User, Account: accounts[rng.IntN(len(accounts))], Level: Level(1 + rng.IntN(3))}
	}
	visitors := []Who{{}, {SignedIn: true, Account: "E1"}, {SignedIn: true, Account: "E2"}, {SignedIn: true, Account: "E4"}, {SignedIn: true, Admin: true}}
	for i := 0; i < 2000; i++ {
		var list List
		for n := rng.IntN(6); n > 0; n-- {
			list = append(list, randomEntry())
		}
		more := append(slices.Clone(list), randomEntry())
		for _, w := range visitors {
			if before, after := list.LevelFor(w), more.LevelFor(w); after < before {
				t.Fatalf("%v then %v: %+v went from %v to %v", list, more[len(more)-1], w, before, after)
			}
			// Somebody who is signed in never has less than a guest.
			if w.SignedIn {
				if g, s := list.LevelFor(Who{}), list.LevelFor(w); s < g {
					t.Fatalf("%v: a signed-in account has %v, less than a guest's %v", list, s, g)
				}
			}
		}
	}
}

func spec(browse, backup, full string) Spec {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, ",")
	}
	return Spec{Browse: split(browse), Backup: split(backup), Full: split(full)}
}

func TestSpecBecomesAnOrderedList(t *testing.T) {
	got, err := spec(" Guest ", "", "e2002, E1001 ,").List()
	if err != nil {
		t.Fatal(err)
	}
	want := List{
		{Kind: Guest, Level: Browse},
		{Kind: User, Account: "E1001", Level: Full},
		{Kind: User, Account: "E2002", Level: Full},
	}
	if !got.Equal(want) {
		t.Errorf("list = %+v, want %+v", got, want)
	}
	if err := got.Valid(); err != nil {
		t.Errorf("a list from a Spec is not valid: %v", err)
	}

	// Everybody and the named accounts in other levels; the order is guest, everyone, accounts by ID.
	got, err = spec("everyone", "E3003,E1001", "").List()
	if err != nil {
		t.Fatal(err)
	}
	want = List{
		{Kind: Everyone, Level: Browse},
		{Kind: User, Account: "E1001", Level: Backup},
		{Kind: User, Account: "E3003", Level: Backup},
	}
	if !got.Equal(want) {
		t.Errorf("list = %+v, want %+v", got, want)
	}

	if got, err := spec("", "", "").List(); err != nil || len(got) != 0 {
		t.Errorf("nobody named = %+v, %v; want an empty list", got, err)
	}
}

func TestAPrincipalKeepsItsHighestLevel(t *testing.T) {
	got, err := spec("E1001,E2002", "e1001", "everyone").List()
	if err != nil {
		t.Fatal(err)
	}
	want := List{
		{Kind: Everyone, Level: Full},
		{Kind: User, Account: "E1001", Level: Backup},
		{Kind: User, Account: "E2002", Level: Browse},
	}
	if !got.Equal(want) {
		t.Errorf("list = %+v, want %+v", got, want)
	}
	// An account in the top list and in a lower one has the top level, and a group likewise.
	got, err = spec("everyone", "E1001", "e1001").List()
	if err != nil {
		t.Fatal(err)
	}
	want = List{{Kind: Everyone, Level: Browse}, {Kind: User, Account: "E1001", Level: Full}}
	if !got.Equal(want) {
		t.Errorf("list = %+v, want %+v", got, want)
	}
	// The same name twice in one list is one entry.
	got, err = spec("E1001,E1001,e1001", "", "").List()
	if err != nil || len(got) != 1 {
		t.Errorf("a repeated name = %+v, %v; want one entry", got, err)
	}
}

func TestSpecRefusals(t *testing.T) {
	long := strings.Repeat("E", MaxAccountLen+1)
	var many []string
	for i := 0; i <= MaxAccounts; i++ {
		many = append(many, "E"+strings.Repeat("0", 5)+string(rune('A'+i%26))+strings.Repeat("1", i/26))
	}
	cases := []struct {
		name string
		spec Spec
		key  string
	}{
		{"guests cannot back up", spec("", "guest", ""), "errors.access_guest_level"},
		{"guests cannot have Full", spec("", "", "GUEST"), "errors.access_guest_level"},
		{"guest and everyone together", spec("guest,everyone", "", ""), "errors.access_exclusive"},
		{"guest with an account", spec("guest,E1001", "", ""), "errors.access_exclusive"},
		{"everyone with an account", spec("", "E1001,Everyone", ""), "errors.access_exclusive"},
		{"an ID with a space", spec("E 1001", "", ""), "errors.access_bad_account"},
		{"an ID with a quote", spec("", "E1001'", ""), "errors.access_bad_account"},
		{"an ID with a slash", spec("", "", "E/1"), "errors.access_bad_account"},
		{"an ID that is too long", spec(long, "", ""), "errors.access_bad_account"},
		{"too many accounts", Spec{Browse: many}, "errors.access_too_many"},
	}
	for _, c := range cases {
		_, err := c.spec.List()
		var se *SpecError
		if !errors.As(err, &se) || !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want a *SpecError that wraps ErrInvalid", c.name, err)
			continue
		}
		if se.Key != c.key {
			t.Errorf("%s: key = %q, want %q", c.name, se.Key, c.key)
		}
		if se.Msg == "" || len(se.Msg) > 300 {
			t.Errorf("%s: message %q is empty or echoes too much", c.name, se.Msg)
		}
	}

	// The level that is shown to the visitor is a Level, so the page can translate it.
	_, err := spec("guest,everyone", "", "").List()
	var se *SpecError
	if !errors.As(err, &se) || len(se.Args) != 1 || se.Args[0] != Browse {
		t.Errorf("an exclusive-list refusal carries %v, want the level Browse", se)
	}
}

func TestSpecRoundTripIsStable(t *testing.T) {
	for _, in := range []Spec{
		spec("guest", "everyone", "E1001,E2002"),
		spec("E3003", "E1001", ""),
		spec("", "", ""),
		spec("everyone", "", "E9"),
		spec("", "", "user:everyone"),
		spec("user:Guest", "", "user:EVERYONE"),
	} {
		list, err := in.List()
		if err != nil {
			t.Fatal(err)
		}
		out := list.Spec()
		again, err := out.List()
		if err != nil || !again.Equal(list) {
			t.Errorf("%+v -> %+v -> %+v (%v)", in, list, again, err)
		}
		if out.Browse == nil || out.Backup == nil || out.Full == nil {
			t.Errorf("the three lists of %+v must never be nil, so the API shows [] and not null", out)
		}
	}
	if !(List{}).Spec().IsZero() {
		t.Error("an empty list is a zero Spec")
	}
}

// An employee ID can be spelled like a group. Ticked as a person it must stay that person: a list that gave Full to
// the employee "everyone" and thereby to everybody who is signed in would be a hole.
func TestAnEmployeeIDSpelledLikeAGroupIsStillAPerson(t *testing.T) {
	list, err := spec("user:Guest", "", "user:everyone").List()
	if err != nil {
		t.Fatal(err)
	}
	want := List{{Kind: User, Account: "EVERYONE", Level: Full}, {Kind: User, Account: "GUEST", Level: Browse}}
	slices.SortFunc(want, func(a, b Entry) int { return strings.Compare(a.Account, b.Account) })
	if !list.Equal(want) {
		t.Fatalf("list = %+v, want %+v", list, want)
	}
	if got := list.LevelFor(Who{SignedIn: true, Account: "E1001"}); got != None {
		t.Errorf("somebody else has %v on a list that names two odd employee IDs", got)
	}
	if got := list.LevelFor(Who{}); got != None {
		t.Errorf("a guest has %v on a list that names an employee called guest", got)
	}
	if got := list.LevelFor(Who{SignedIn: true, Account: "everyone"}); got != Full {
		t.Errorf("the employee called everyone has %v, want full", got)
	}
	// Written back, the two are told apart from the groups; a plain name is still a group.
	out := list.Spec()
	if strings.Join(out.Full, ",") != "user:EVERYONE" || strings.Join(out.Browse, ",") != "user:GUEST" {
		t.Errorf("spec = %+v", out)
	}
	if plain, err := spec("everyone", "", "").List(); err != nil || plain[0].Kind != Everyone {
		t.Errorf("a plain everyone = %+v, %v; want the group", plain, err)
	}
}

func TestParseToken(t *testing.T) {
	cases := []struct {
		in      string
		kind    Kind
		account string
		bad     bool
	}{
		{"", "", "", false},
		{"  ", "", "", false},
		{"guest", Guest, "", false},
		{" GUEST ", Guest, "", false},
		{"Everyone", Everyone, "", false},
		{"e1001", User, "E1001", false},
		{"user:e1001", User, "E1001", false},
		{"USER:E1001", User, "E1001", false},
		{"user:guest", User, "GUEST", false},
		{"user:", "", "", true},
		{"user:bad id", "", "", true},
		{"user:user:E1", "", "", true},
		{"bad id", "", "", true},
	}
	for _, c := range cases {
		kind, account, err := ParseToken(c.in)
		if (err != nil) != c.bad || kind != c.kind || account != c.account {
			t.Errorf("ParseToken(%q) = %q, %q, %v; want %q, %q, bad=%v", c.in, kind, account, err, c.kind, c.account, c.bad)
		}
	}
}

func TestUpgradeIsWhatEverybodyCouldDoBefore(t *testing.T) {
	up := Upgrade()
	if err := up.Valid(); err != nil {
		t.Fatal(err)
	}
	if got := up.LevelFor(Who{}); got != Browse {
		t.Errorf("a guest gets %v, want browse", got)
	}
	if got := up.LevelFor(Who{SignedIn: true, Account: "E1"}); got != Backup {
		t.Errorf("an account gets %v, want backup", got)
	}
	if got := up.LevelFor(Who{SignedIn: true, Account: "E1", Admin: true}); got != Full {
		t.Errorf("an administrator gets %v, want full", got)
	}
}

// The digest is what a form carries to say which grants it was made from: the same grants always give the same text,
// whatever order they come in, and any change of a grant gives another.
func TestDigestNamesTheExactGrants(t *testing.T) {
	base := List{
		{Kind: Guest, Level: Browse},
		{Kind: Everyone, Level: Backup},
		{Kind: User, Account: "E1001", Level: Full},
		{Kind: User, Account: "E2002", Level: Full},
	}
	want := base.Digest()
	if len(want) != 32 || strings.Trim(want, "0123456789abcdef") != "" {
		t.Fatalf("Digest = %q, want 32 hexadecimal characters", want)
	}
	if got := (List{base[3], base[1], base[2], base[0]}).Digest(); got != want {
		t.Errorf("the same grants in another order give %q, want %q", got, want)
	}
	if got := slices.Clone(base).Digest(); got != want {
		t.Errorf("a copy gives %q, want %q", got, want)
	}
	if base[0].Kind != Guest || base[3].Account != "E2002" {
		t.Fatal("Digest changed the list it was called on")
	}

	differ := map[string]List{
		"a level":              {base[0], base[1], {Kind: User, Account: "E1001", Level: Backup}, base[3]},
		"an account":           {base[0], base[1], base[2], {Kind: User, Account: "E2003", Level: Full}},
		"a missing entry":      base[:3],
		"an added entry":       append(slices.Clone(base), Entry{Kind: User, Account: "E3003", Level: Full}),
		"a group for a person": {base[0], {Kind: User, Account: "EVERYONE", Level: Backup}, base[2], base[3]},
		"nothing":              nil,
	}
	seen := map[string]string{want: "the original"}
	for name, l := range differ {
		got := l.Digest()
		if other, dup := seen[got]; dup {
			t.Errorf("%s gives the digest of %s: %q", name, other, got)
		}
		seen[got] = name
	}
	if (List{}).Digest() != List(nil).Digest() {
		t.Error("an empty list and no list give different digests")
	}
	// What was entered and what was stored say the same.
	spec := base.Spec()
	again, err := spec.List()
	if err != nil || again.Digest() != want {
		t.Errorf("the list of its own Spec has the digest %q, %v; want %q", again.Digest(), err, want)
	}
}

func TestOffersAccounts(t *testing.T) {
	cases := []struct {
		list List
		min  Level
		want bool
	}{
		{Upgrade(), Backup, true},
		{Upgrade(), Full, false},
		{List{{Kind: Guest, Level: Browse}}, Browse, false},
		{List{{Kind: User, Account: "E1", Level: Full}}, Backup, true},
		{nil, Browse, false},
	}
	for _, c := range cases {
		if got := c.list.OffersAccounts(c.min); got != c.want {
			t.Errorf("%+v.OffersAccounts(%v) = %v, want %v", c.list, c.min, got, c.want)
		}
	}
}

func TestValidChecksAListBuiltInCode(t *testing.T) {
	good := List{{Kind: Guest, Level: Browse}, {Kind: Everyone, Level: Full}, {Kind: User, Account: "E1", Level: Backup}}
	if err := good.Valid(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]List{
		"unknown kind":         {{Kind: "group", Level: Browse}},
		"level none":           {{Kind: Everyone, Level: None}},
		"level above full":     {{Kind: Everyone, Level: Level(4)}},
		"guest above browse":   {{Kind: Guest, Level: Backup}},
		"user without account": {{Kind: User, Level: Browse}},
		"account on everyone":  {{Kind: Everyone, Account: "E1", Level: Browse}},
		"principal twice":      {{Kind: User, Account: "E1", Level: Browse}, {Kind: User, Account: "E1", Level: Full}},
	} {
		if bad.Valid() == nil {
			t.Errorf("%s: list %+v passed", name, bad)
		}
	}
}

func TestValidAccount(t *testing.T) {
	for in, want := range map[string]string{"e1001": "E1001", " E1001 ": "E1001", "a.b-c_d@e": "A.B-C_D@E", "12345": "12345", "岳不群": "岳不群"} {
		if got, ok := ValidAccount(in); !ok || got != want {
			t.Errorf("ValidAccount(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "  ", "E 1", "E,1", "E;1", "E\n1", "E1\x00", "<script>", strings.Repeat("A", MaxAccountLen+1)} {
		if got, ok := ValidAccount(in); ok {
			t.Errorf("ValidAccount(%q) = %q, true; want false", in, got)
		}
	}
}
