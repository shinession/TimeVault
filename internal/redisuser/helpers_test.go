package redisuser

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// inert stands for an object of a class that the decoder does not interpret.
type inert struct{ module, name string }

// same compares a decoded value with the expected one: NaN equals NaN,
// integers and times compare by value, inert matches the class of an object.
func same(got, want any) bool {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		return ok && (g == w || math.IsNaN(g) && math.IsNaN(w))
	case *big.Int:
		g, ok := got.(*big.Int)
		return ok && g.Cmp(w) == 0
	case time.Time:
		g, ok := got.(time.Time)
		return ok && g.Equal(w)
	case inert:
		o, ok := got.(*pyObject)
		return ok && o.Class == pyGlobal{w.module, w.name}
	}
	return reflect.DeepEqual(got, want)
}

// zl compresses like Python's zlib.compress.
func zl(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func fromHex(t testing.TB, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

// vector returns the bytes dash stored for the named Python value.
func vector(t testing.TB, name string) []byte {
	t.Helper()
	h, ok := vectors[name]
	if !ok {
		t.Fatalf("no vector %q", name)
	}
	return fromHex(t, h)
}

// inflated returns the pickle inside a vector.
func inflated(t testing.TB, name string) []byte {
	t.Helper()
	b, err := inflate(vector(t, name))
	if err != nil {
		t.Fatalf("inflate %s: %v", name, err)
	}
	return b
}

func utc(year int, month time.Month, day, hour, minute, sec, nsec int) time.Time {
	return time.Date(year, month, day, hour, minute, sec, nsec, time.UTC)
}

func mustBig(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad integer " + s)
	}
	return n
}

// timestamp marks a time that pyPickle writes as a pandas.Timestamp with nanosecond resolution.
type timestamp time.Time

// pyPickle writes what pickle.dumps(v) writes with protocol 4 for the types of a
// dash user hash: None, bool, str, naive datetime.datetime and pandas.Timestamp.
// vectors_test.go compares it with the output of Python.
func pyPickle(v any) []byte {
	var body []byte
	switch x := v.(type) {
	case nil:
		body = []byte{'N'}
	case bool:
		body = []byte{0x89}
		if x {
			body = []byte{0x88}
		}
	case string:
		body = pyStr(x)
	case time.Time:
		state := []byte{byte(x.Year() >> 8), byte(x.Year()), byte(x.Month()), byte(x.Day()), byte(x.Hour()), byte(x.Minute()), byte(x.Second())}
		us := x.Nanosecond() / 1000
		state = append(state, byte(us>>16), byte(us>>8), byte(us))
		body = append(pyStr("datetime"), pyStr("datetime")...)
		body = append(body, 0x93, 0x94, 'C', 10)
		body = append(body, state...)
		body = append(body, 0x94, 0x85, 0x94, 'R', 0x94)
	case timestamp:
		ns := time.Time(x).UnixNano()
		if ns < 1<<56 {
			panic("pyPickle: timestamp too early for LONG1")
		}
		body = append(pyStr("pandas._libs.tslibs.timestamps"), pyStr("_unpickle_timestamp")...)
		body = append(body, 0x93, 0x94, '(', 0x8a, 8)
		body = binary.LittleEndian.AppendUint64(body, uint64(ns))
		body = append(body, 'N', 'N', 'K', 10, 't', 0x94, 'R', 0x94)
	default:
		panic(fmt.Sprintf("pyPickle: unsupported %T", v))
	}
	body = append(body, '.')
	out := []byte{0x80, 4}
	if len(body) >= 4 { // CPython frames pickles of 4 bytes or more
		out = append(out, 0x95)
		out = binary.LittleEndian.AppendUint64(out, uint64(len(body)))
	}
	return append(out, body...)
}

func pyStr(s string) []byte {
	var b []byte
	if len(s) < 256 {
		b = append(b, 0x8c, byte(len(s)))
	} else {
		b = append(b, 'X')
		b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	}
	b = append(b, s...)
	return append(b, 0x94)
}

// user is a fake directory entry.
type user struct {
	ecode, name, email, typ string
	password                string // empty: stored as None
	active                  bool
	updated                 time.Time // zero: stored as None
}

var now = utc(2026, 11, 15, 14, 30, 1, 0)

func alice() user {
	return user{ecode: "E1001", name: "Alice Tan", email: "alice.tan@example.test", typ: "user", password: "fake-pw-1", active: true, updated: now}
}

// key is the Redis key dash gives the user.
func (u user) key() string {
	return "app_user_" + strings.ToLower(u.ecode) + "_" + strings.ToLower(u.name) + "_" + strings.ToLower(u.email)
}

// fields are the hash fields dash writes, as Python values.
func (u user) fields() map[string]any {
	var password, updated any
	if u.password != "" {
		password = u.password
	}
	if !u.updated.IsZero() {
		updated = timestamp(u.updated)
	}
	return map[string]any{
		"ee_code": u.ecode, "HashID": "h-" + strings.ToLower(u.ecode), "Name": u.name, "Email": u.email,
		"HashPassword": password, "Type": u.typ, "UpdateDate": updated, "phone_num": nil,
		"activated": u.active, "is_company_email": true,
	}
}

// hash is the hash dash writes for u: every field is zlib.compress(pickle.dumps(value)).
func (u user) hash() map[string]string {
	hash := map[string]string{}
	for name, val := range u.fields() {
		hash[name] = string(zl(pyPickle(val)))
	}
	return hash
}

// seed stores users in Redis the way dash does.
func seed(t testing.TB, mr *miniredis.Miniredis, users ...user) {
	t.Helper()
	for _, u := range users {
		seedAs(mr, u.key(), u.hash())
	}
}

func seedAs(mr *miniredis.Miniredis, key string, hash map[string]string) {
	for name, raw := range hash {
		mr.HSet(key, name, raw)
	}
}

// stored returns the hash of u with some fields replaced by raw bytes; a nil value removes the field.
func stored(u user, raw map[string][]byte) map[string]string {
	hash := u.hash()
	for name, b := range raw {
		if b == nil {
			delete(hash, name)
		} else {
			hash[name] = string(b)
		}
	}
	return hash
}

// newDirectory starts an in-process Redis and a Store on it.
func newDirectory(t *testing.T, users ...user) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	seed(t, mr, users...)
	return openStore(t, Options{URL: "redis://" + mr.Addr()}), mr
}

func openStore(t testing.TB, o Options) *Store {
	t.Helper()
	s, err := Open(o)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
