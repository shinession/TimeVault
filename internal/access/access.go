// Package access decides what a visitor may do with one monitored folder.
//
// Every monitored folder carries a list of grants. A grant says who it is for
// (guests, everybody who is signed in, or one account) and which level it
// gives. The levels build on each other: Browse, then Backup, then Full. An
// administrator has Full on every folder without being listed.
//
// The package knows nothing about HTTP, the database or the user directory; it
// only turns a list and a visitor into a level, and checks a list that a person
// typed before it is stored.
package access

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Level is how much a visitor may do with one monitored folder. Each level
// includes everything the levels below it allow.
type Level int

const (
	// None cannot see the folder at all: it is left out of every list and every request for it is answered
	// as if it did not exist.
	None Level = iota
	// Browse looks at the folder (files, history, versions, search, the state of its backups and jobs) and
	// restores from it. A restore needs an account; a guest who may browse is asked to sign in for it.
	Browse
	// Backup is Browse plus backing up now (also with a full re-read), verifying recovery points and
	// cancelling the jobs of the folder.
	Backup
	// Full is Backup plus changing the settings of the folder and who may do what with it. The folder on the
	// server's disk and the list of monitored folders stay with the administrators.
	Full
)

var levelNames = [...]string{None: "none", Browse: "browse", Backup: "backup", Full: "full"}

// String is the name used in the database, the API and the log: none, browse, backup or full.
func (l Level) String() string {
	if l < None || int(l) >= len(levelNames) {
		return "none"
	}
	return levelNames[l]
}

// ParseLevel reads the name of a level that can be granted (browse, backup or full).
func ParseLevel(s string) (Level, bool) {
	switch s {
	case "browse":
		return Browse, true
	case "backup":
		return Backup, true
	case "full":
		return Full, true
	}
	return None, false
}

// AtLeast reports whether l gives everything that min gives.
func (l Level) AtLeast(min Level) bool { return l >= min }

// Kind says who a grant is for.
type Kind string

const (
	// Guest is every visitor, signed in or not. It can only give Browse: somebody who has not signed in has no
	// session to carry a CSRF token and nobody to audit.
	Guest Kind = "guest"
	// Everyone is every signed-in account.
	Everyone Kind = "everyone"
	// User is one account, by employee ID.
	User Kind = "user"
)

// The two names that stand for a group of visitors in a Spec. A plain name is a group when it is one of these and an
// employee ID otherwise; an employee ID that is spelled like a group (a legal one, as far as its characters go) is
// written with UserPrefix, which is how the settings form always writes them.
const (
	GuestName    = "guest"
	EveryoneName = "everyone"
	// UserPrefix marks a name of a Spec as an employee ID, whatever it is spelled like: "user:E1001".
	UserPrefix = "user:"
)

// Limits of a list.
const (
	// MaxAccounts is how many accounts one folder may name.
	MaxAccounts = 500
	// MaxAccountLen is the longest employee ID, in characters.
	MaxAccountLen = 64
)

// Entry is one grant.
type Entry struct {
	Kind Kind
	// Account is the employee ID in upper case; empty unless Kind is User.
	Account string
	Level   Level
}

// List is the grants of one folder. A list that came out of Spec.List or the database holds each principal once,
// with the highest level it was given, ordered guest, everyone, then the accounts by employee ID.
type List []Entry

// Who is the visitor a level is worked out for.
type Who struct {
	SignedIn bool
	// Account is the employee ID of the signed-in account.
	Account string
	// Admin is true for an administrator, who has Full on every folder.
	Admin bool
}

// LevelFor is the level the visitor has: the highest of what an administrator, guests, everybody who is signed in
// and the visitor's own entry give.
func (l List) LevelFor(w Who) Level {
	if w.SignedIn && w.Admin {
		return Full
	}
	best := None
	for _, e := range l {
		switch e.Kind {
		case Guest:
		case Everyone:
			if !w.SignedIn {
				continue
			}
		case User:
			if !w.SignedIn || !strings.EqualFold(e.Account, w.Account) {
				continue
			}
		default:
			continue
		}
		best = max(best, e.Level)
	}
	return best
}

// OffersAccounts reports whether signing in could give at least min on this folder: an entry for everybody who
// is signed in or for some account gives it. It tells a guest whether to be offered the sign-in.
func (l List) OffersAccounts(min Level) bool {
	for _, e := range l {
		if (e.Kind == Everyone || e.Kind == User) && e.Level >= min {
			return true
		}
	}
	return false
}

// Equal reports whether two lists give the same grants.
func (l List) Equal(o List) bool { return slices.Equal(l, o) }

// Digest names the exact state of a list: the same grants always give the same text, whatever order they come in,
// and any change of a grant gives another. A form or a request carries the digest of the list it was built from, so
// that saving can tell whether somebody else has changed the grants since.
func (l List) Digest() string {
	sorted := slices.Clone(l)
	slices.SortFunc(sorted, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)), strings.Compare(a.Account, b.Account), cmp.Compare(a.Level, b.Level))
	})
	h := sha256.New()
	for _, e := range sorted {
		fmt.Fprintf(h, "%s\x00%s\x00%d\n", e.Kind, e.Account, e.Level)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// Upgrade is the list that the upgrade to per-folder levels gave every folder that existed before: guests browse and
// every signed-in account may also back up, restore and verify. That is what everybody could do until then.
func Upgrade() List {
	return List{{Kind: Guest, Level: Browse}, {Kind: Everyone, Level: Backup}}
}

// Spec is a list the way it is entered in the settings form and in the API: for each level, who has it. A name is
// "guest", "everyone" or an employee ID (written "user:<ID>" when it is spelled like a group). A name in a higher
// list wins over the same name in a lower one.
type Spec struct {
	Browse []string `json:"browse"`
	Backup []string `json:"backup"`
	Full   []string `json:"full"`
}

// IsZero reports whether nobody is named.
func (s Spec) IsZero() bool { return len(s.Browse)+len(s.Backup)+len(s.Full) == 0 }

// Spec is the list as it is entered: each principal under the level it has. The three lists are never nil.
func (l List) Spec() Spec {
	s := Spec{Browse: []string{}, Backup: []string{}, Full: []string{}}
	for _, e := range l {
		name := e.Account
		switch e.Kind {
		case Guest:
			name = GuestName
		case Everyone:
			name = EveryoneName
		case User:
			if strings.EqualFold(name, GuestName) || strings.EqualFold(name, EveryoneName) {
				name = UserPrefix + name
			}
		}
		switch e.Level {
		case Browse:
			s.Browse = append(s.Browse, name)
		case Backup:
			s.Backup = append(s.Backup, name)
		case Full:
			s.Full = append(s.Full, name)
		}
	}
	return s
}

// Accounts lists the employee IDs a list names, once each, in order.
func (l List) Accounts() []string {
	var out []string
	for _, e := range l {
		if e.Kind == User {
			out = append(out, e.Account)
		}
	}
	return out
}

// ErrInvalid wraps every refusal of a Spec.
var ErrInvalid = errors.New("invalid permissions")

// SpecError is a refusal that the web UI shows in the language of the visitor: Key names the message and Args fill
// it in (a Level in Args is shown by its translated name); Msg is the same sentence in English for the API and the
// log. It wraps ErrInvalid.
type SpecError struct {
	Key  string
	Args []any
	Msg  string
}

func (e *SpecError) Error() string { return "invalid permissions: " + e.Msg }

func (e *SpecError) Unwrap() error { return ErrInvalid }

func refuse(key, msg string, args ...any) error {
	return &SpecError{Key: key, Args: args, Msg: fmt.Sprintf(msg, args...)}
}

// shorten cuts text that came from the network before it is put into a message.
func shorten(s string) string {
	if r := []rune(s); len(r) > MaxAccountLen {
		return string(r[:MaxAccountLen]) + "..."
	}
	return s
}

// ValidAccount cleans an employee ID and says whether it can be one: 1 to MaxAccountLen letters, digits and the
// characters . _ - @, with no spaces. The ID is returned in upper case, which is how the user directory spells it.
func ValidAccount(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" || utf8.RuneCountInString(id) > MaxAccountLen {
		return "", false
	}
	for _, r := range id {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-@", r) {
			return "", false
		}
	}
	return strings.ToUpper(id), true
}

type principal struct {
	kind    Kind
	account string
}

// ParseToken reads one name of a Spec: Guest and Everyone for those two names, User and the upper-case employee ID
// for an ID (with or without UserPrefix; only with it may the ID be spelled like a group), and an empty Kind for a
// blank one. Anything else is refused with the same error Spec.List gives.
func ParseToken(raw string) (Kind, string, error) {
	token := strings.TrimSpace(raw)
	switch {
	case token == "":
		return "", "", nil
	case len(token) >= len(UserPrefix) && strings.EqualFold(token[:len(UserPrefix)], UserPrefix):
		id, ok := ValidAccount(token[len(UserPrefix):])
		if !ok {
			return "", "", refuse("errors.access_bad_account", "%q is not a valid employee ID", shorten(token))
		}
		return User, id, nil
	case strings.EqualFold(token, GuestName):
		return Guest, "", nil
	case strings.EqualFold(token, EveryoneName):
		return Everyone, "", nil
	}
	id, ok := ValidAccount(token)
	if !ok {
		return "", "", refuse("errors.access_bad_account", "%q is not a valid employee ID", shorten(token))
	}
	return User, id, nil
}

// List checks what was entered and turns it into a list. Inside one list, guest and everyone cannot be combined
// with each other or with accounts (they already include the accounts); guest can only be given Browse; every
// employee ID has to be valid; and at most MaxAccounts accounts are named. A name that is in more than one list
// keeps the highest level. Nothing is looked up in the user directory here.
func (s Spec) List() (List, error) {
	best := map[principal]Level{}
	for _, in := range []struct {
		level  Level
		tokens []string
	}{{Browse, s.Browse}, {Backup, s.Backup}, {Full, s.Full}} {
		guest, everyone := false, false
		var accounts []string
		for _, raw := range in.tokens {
			kind, id, err := ParseToken(raw)
			if err != nil {
				return nil, err
			}
			switch kind {
			case Guest:
				guest = true
			case Everyone:
				everyone = true
			case User:
				accounts = append(accounts, id)
			}
		}
		groups := 0
		for _, g := range []bool{guest, everyone} {
			if g {
				groups++
			}
		}
		switch {
		case guest && in.level != Browse:
			return nil, refuse("errors.access_guest_level", "guests can only be given browse")
		case groups > 1 || (groups == 1 && len(accounts) > 0):
			return nil, refuse("errors.access_exclusive", "in the %s list, guest and everyone cannot be combined with each other or with accounts", in.level)
		}
		if guest {
			best[principal{kind: Guest}] = max(best[principal{kind: Guest}], in.level)
		}
		if everyone {
			best[principal{kind: Everyone}] = max(best[principal{kind: Everyone}], in.level)
		}
		for _, id := range accounts {
			p := principal{kind: User, account: id}
			best[p] = max(best[p], in.level)
		}
	}
	out := make(List, 0, len(best))
	for p, level := range best {
		out = append(out, Entry{Kind: p.kind, Account: p.account, Level: level})
	}
	slices.SortFunc(out, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)), strings.Compare(a.Account, b.Account))
	})
	if len(out.Accounts()) > MaxAccounts {
		return nil, refuse("errors.access_too_many", "at most %d accounts can be named for one folder", MaxAccounts)
	}
	return out, nil
}

func kindRank(k Kind) int {
	switch k {
	case Guest:
		return 0
	case Everyone:
		return 1
	}
	return 2
}

// Valid reports whether a list is well formed: known kinds and levels, guests only at Browse, an account only for
// the User kind, no principal twice. A list read back from the database always is; this is the check for one that
// was built in code.
func (l List) Valid() error {
	seen := map[principal]bool{}
	for _, e := range l {
		p := principal{kind: e.Kind, account: e.Account}
		switch {
		case e.Kind != Guest && e.Kind != Everyone && e.Kind != User:
			return fmt.Errorf("access: unknown kind %q", e.Kind)
		case e.Level < Browse || e.Level > Full:
			return fmt.Errorf("access: level %d cannot be granted", e.Level)
		case e.Kind == Guest && e.Level != Browse:
			return errors.New("access: guests can only be given browse")
		case (e.Kind == User) != (e.Account != ""):
			return errors.New("access: an account belongs to the user kind only")
		case seen[p]:
			return errors.New("access: a principal is listed twice")
		}
		seen[p] = true
	}
	return nil
}
