package redisuser

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Record is one user of the directory, as the dash application keeps it in
// the Redis hash "app_user_<ee_code>_<name>_<email>".
type Record struct {
	Ref       string // the Redis key it was read from
	ECode     string // upper-cased, trimmed
	Name      string
	Email     string // lower-cased, trimmed
	Type      string // as stored, trimmed
	Activated bool
	Updated   time.Time // zero when unreadable; times without a zone are read as UTC

	// The password is kept as a keyed digest, not as text, so printing,
	// logging or marshalling a Record can never reveal it, reflection included.
	hasPassword bool
	password    [sha256.Size]byte
}

// passwordKey is random per process. A digest made with it is useless anywhere else.
// crypto/rand.Read cannot fail since Go 1.24.
var passwordKey = func() []byte {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
}()

func passwordDigest(s string) [sha256.Size]byte {
	h := hmac.New(sha256.New, passwordKey)
	h.Write([]byte(s))
	var d [sha256.Size]byte
	h.Sum(d[:0])
	return d
}

// IsAdmin reports whether Type is "admin", whatever the case.
func (r Record) IsAdmin() bool { return strings.EqualFold(strings.TrimSpace(r.Type), "admin") }

// HasPassword reports whether a password is stored. dash stores it as plain
// text; a missing one is never a valid password.
func (r Record) HasPassword() bool { return r.hasPassword }

// CheckPassword compares candidate with the stored password in constant time.
// It is false when no password is stored, whatever the candidate is.
func (r Record) CheckPassword(candidate string) bool {
	d := passwordDigest(candidate)
	same := subtle.ConstantTimeCompare(d[:], r.password[:]) == 1
	return same && r.hasPassword
}

// String and GoString show the identity and the state of the account only.
// Name, e-mail address and Ref are personal data and stay out of logs.
func (r Record) String() string {
	return fmt.Sprintf("Record{ee_code: %q, type: %q, activated: %t}", r.ECode, r.Type, r.Activated)
}

func (r Record) GoString() string {
	return fmt.Sprintf("redisuser.Record{ECode: %q, Type: %q, Activated: %t}", r.ECode, r.Type, r.Activated)
}

// LogValue implements slog.LogValuer with the same restraint as String.
func (r Record) LogValue() slog.Value {
	return slog.GroupValue(slog.String("ee_code", r.ECode), slog.String("type", r.Type), slog.Bool("activated", r.Activated))
}

type fieldState uint8

const (
	fieldAbsent fieldState = iota
	fieldOK
	fieldBad
)

// fieldSpec describes one field of the hash. A nil set means the field is
// decoded for the health check but not used.
type fieldSpec struct {
	name     string
	required bool // a record without it is unreadable
	set      func(*Record, value) bool
}

// fieldSpecs lists the fields dash writes. UpdateDate only orders records, so
// a record whose date cannot be read is still usable.
var fieldSpecs = [...]fieldSpec{
	{"ee_code", true, func(r *Record, v value) bool {
		s, ok := asString(v)
		r.ECode = strings.ToUpper(strings.TrimSpace(s))
		return ok
	}},
	{"HashID", false, nil},
	{"Name", true, func(r *Record, v value) bool {
		s, ok := asString(v)
		r.Name = strings.TrimSpace(s)
		return ok
	}},
	{"Email", true, func(r *Record, v value) bool {
		s, ok := asString(v)
		r.Email = strings.ToLower(strings.TrimSpace(s))
		return ok
	}},
	{"HashPassword", true, func(r *Record, v value) bool {
		p, ok := asPassword(v)
		if ok && p != "" {
			r.hasPassword, r.password = true, passwordDigest(p)
		}
		return ok
	}},
	{"Type", true, func(r *Record, v value) bool {
		s, ok := asOptionalString(v)
		r.Type = strings.TrimSpace(s)
		return ok
	}},
	{"UpdateDate", false, func(r *Record, v value) bool {
		t, ok := asTime(v)
		r.Updated = t
		return ok || isMissingTime(v)
	}},
	{"phone_num", false, nil},
	{"activated", true, func(r *Record, v value) bool {
		b, ok := asBool(v)
		r.Activated = b
		return ok
	}},
	{"is_company_email", false, nil},
}

// hashInfo is the outcome of decoding one hash.
type hashInfo struct {
	rec    Record
	usable bool // every required field was decoded
	state  [len(fieldSpecs)]fieldState
	reason [len(fieldSpecs)]string // why a field is bad or missing; never contains data of the record
}

// decodeHash decodes the fields of hash. Unless all is set, only the fields
// that make up a Record are decoded.
func decodeHash(ref string, hash map[string]string, all bool) hashInfo {
	h := hashInfo{rec: Record{Ref: ref}, usable: true}
	for i, f := range fieldSpecs {
		if f.set == nil && !all {
			continue
		}
		raw, present := hash[f.name]
		if !present {
			if f.required {
				h.usable, h.reason[i] = false, "missing"
			}
			continue
		}
		v, err := decodeField([]byte(raw))
		if err == nil && f.set != nil && !f.set(&h.rec, v) {
			err = errUnexpected
		}
		if err != nil {
			h.state[i], h.reason[i] = fieldBad, err.Error()
			h.usable = h.usable && !f.required
			continue
		}
		h.state[i] = fieldOK
	}
	return h
}
