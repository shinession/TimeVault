package redisuser

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

// parse runs a pickle program given as text; Go escapes spell the raw bytes.
func parse(program string) (value, error) { return parsePickle([]byte(program)) }

func TestOpcodes(t *testing.T) {
	obj := func(module, name string, args pyTuple, state value) *pyObject {
		return &pyObject{Class: pyGlobal{module, name}, Args: args, State: state}
	}
	cases := []struct {
		name, program string
		want          any
	}{
		{"STRING", "S'abc'\n.", "abc"},
		{"STRING double quotes", "S\"abc\"\n.", "abc"},
		{"STRING escapes", `S'a\x41\n\101\\\''` + "\n.", "aA\nA\\'"},
		{"SHORT_BINSTRING", "U\x03abc.", "abc"},
		{"BINSTRING", "T\x03\x00\x00\x00abc.", "abc"},
		{"BINUNICODE8", "\x8d\x03\x00\x00\x00\x00\x00\x00\x00abc.", "abc"},
		{"BINBYTES8", "\x8e\x03\x00\x00\x00\x00\x00\x00\x00abc.", []byte("abc")},
		{"BYTEARRAY8", "\x96\x03\x00\x00\x00\x00\x00\x00\x00abc.", []byte("abc")},
		{"INT", "I12345\n.", int64(12345)},
		{"INT negative", "I-5\n.", int64(-5)},
		{"INT True", "I01\n.", true},
		{"INT False", "I00\n.", false},
		{"INT beyond 64 bits", "I99999999999999999999\n.", mustBig("99999999999999999999")},
		{"LONG", "L123L\n.", int64(123)},
		{"LONG big", "L100000000000000000000L\n.", mustBig("100000000000000000000")},
		{"LONG1 empty", "\x8a\x00.", int64(0)},
		{"LONG1 two bytes", "\x8a\x02\xff\x7f.", int64(32767)},
		{"LONG1 sign byte", "\x8a\x01\xff.", int64(-1)},
		{"FLOAT", "F1.5\n.", 1.5},
		{"FLOAT nan", "Fnan\n.", math.NaN()},
		{"EMPTY_LIST APPEND", "]K\x01a.", &pyList{items: []value{int64(1)}}},
		{"LIST", "(K\x01K\x02l.", &pyList{items: []value{int64(1), int64(2)}}},
		{"APPENDS", "](K\x01K\x02e.", &pyList{items: []value{int64(1), int64(2)}}},
		{"DICT", "(K\x01K\x02d.", &pyDict{keys: []value{int64(1)}, vals: []value{int64(2)}}},
		{"SETITEM", "}K\x01K\x02s.", &pyDict{keys: []value{int64(1)}, vals: []value{int64(2)}}},
		{"SETITEMS", "}(K\x01K\x02K\x03K\x04u.", &pyDict{keys: []value{int64(1), int64(3)}, vals: []value{int64(2), int64(4)}}},
		{"EMPTY_SET ADDITEMS", "\x8f(K\x01K\x02\x90.", &pySet{items: []value{int64(1), int64(2)}}},
		{"FROZENSET", "(K\x01\x91.", &pySet{items: []value{int64(1)}}},
		{"DUP", "K\x012\x86.", pyTuple{int64(1), int64(1)}},
		{"POP", "K\x01K\x020.", int64(1)},
		{"POP_MARK", "K\x01(K\x02K\x031.", int64(1)},
		{"POP of a mark", "K\x01(0.", int64(1)},
		{"PUT GET", "K\x07p5\ng5\n\x86.", pyTuple{int64(7), int64(7)}},
		{"LONG_BINPUT LONG_BINGET", "K\x07r\x01\x00\x00\x00j\x01\x00\x00\x00\x86.", pyTuple{int64(7), int64(7)}},
		{"MEMOIZE BINGET", "K\x07\x94h\x00\x86.", pyTuple{int64(7), int64(7)}},
		{"GLOBAL", "cos\nsystem\n.", pyGlobal{"os", "system"}},
		{"REDUCE", "cfoo\nbar\n(K\x01tR.", obj("foo", "bar", pyTuple{int64(1)}, nil)},
		{"REDUCE of an earlier call", "cfoo\nbar\n(S'x'\ntR(K\x01tR.", obj("", "", pyTuple{int64(1)}, nil)},
		{"NEWOBJ", "cfoo\nbar\n)\x81.", obj("foo", "bar", pyTuple{}, nil)},
		{"NEWOBJ_EX", "cfoo\nbar\n)}\x92.", &pyObject{Class: pyGlobal{"foo", "bar"}, Args: pyTuple{}, Kwargs: &pyDict{}}},
		{"BUILD", "cfoo\nbar\n)\x81K\x05b.", obj("foo", "bar", pyTuple{}, int64(5))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parse(c.program)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !same(got, c.want) {
				t.Errorf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestMalformedPickles(t *testing.T) {
	cases := []struct {
		name, program string
		want          error
	}{
		{"empty", "", errTruncated},
		{"no STOP", "N", errTruncated},
		{"STOP on an empty stack", ".", errUnderflow},
		{"unknown protocol", "\x80\x09N.", errMalformed},
		{"truncated BINUNICODE", "X\x05\x00\x00\x00ab", errTruncated},
		{"truncated BININT", "J\x01\x02", errTruncated},
		{"declared length of 4 GiB", "X\xff\xff\xff\xff", errLimit},
		{"declared length of 2^64-1", "\x8d" + strings.Repeat("\xff", 8), errLimit},
		{"declared length over the limit", "X\x01\x00\x01\x00", errLimit},
		{"line without newline", "I123", errTruncated},
		{"integer text too long", "I" + strings.Repeat("9", 5000) + "\n.", errLimit},
		{"GLOBAL line too long", "c" + strings.Repeat("a", 70000) + "\nx\n.", errLimit},
		{"stack depth", strings.Repeat("N", 1001) + ".", errLimit},
		{"open marks", strings.Repeat("(", 1001) + ".", errLimit},
		{"opcode budget", strings.Repeat("N0", 100_001) + ".", errLimit},
		{"memo entries", strings.Repeat("N\x940", 10_001) + "N.", errLimit},
		{"list size", "]" + strings.Repeat("K\x00a", 10_001) + ".", errLimit},
		{"unknown memo entry", "h\x00.", errMalformed},
		{"negative memo index", "Np-1\n.", errMalformed},
		{"unknown opcode", "\x80\x04\xff.", errOpcode},
		{"letter that is no opcode", "A.", errOpcode},
		{"INST", "ifoo\nbar\n.", errOpcode},
		{"OBJ", "(cfoo\nbar\no.", errOpcode},
		{"PERSID", "Pabc\n.", errOpcode},
		{"BINPERSID", "NQ.", errOpcode},
		{"EXT1", "\x82\x01.", errOpcode},
		{"EXT2", "\x83\x01\x00.", errOpcode},
		{"EXT4", "\x84\x01\x00\x00\x00.", errOpcode},
		{"NEXT_BUFFER", "\x97.", errOpcode},
		{"READONLY_BUFFER", "N\x98.", errOpcode},
		{"TUPLE without MARK", "t.", errMalformed},
		{"TUPLE2 without two values", "K\x01\x86.", errUnderflow},
		{"values below a mark are out of reach", "K\x01(\x85.", errUnderflow},
		{"APPEND to a non-list", "NNa.", errMalformed},
		{"SETITEMS with an odd count", "}(K\x01u.", errMalformed},
		{"BUILD on a non-object", "NNb.", errMalformed},
		{"REDUCE of a non-global", "N)R.", errMalformed},
		{"REDUCE without a tuple", "cos\nsystem\nNR.", errMalformed},
		{"invalid UTF-8", "X\x01\x00\x00\x00\xff.", errMalformed},
		{"8-bit string that is not ASCII", "U\x01\xe9.", errMalformed},
		{"invalid float", "Fabc\n.", errMalformed},
		{"invalid integer", "Iabc\n.", errMalformed},
		{"unterminated unicode escape", "V\\u12\n.", errMalformed},
		{"surrogate in a unicode escape", "V\\ud800\n.", errMalformed},
		{"unquoted STRING", "Sabc\n.", errMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v, err := parse(c.program); !errors.Is(err, c.want) {
				t.Errorf("got %#v, %v; want %v", v, err, c.want)
			}
		})
	}
}

// Every proper prefix of a pickle lacks its STOP, so it must be an error at whatever opcode it ends.
func TestEveryTruncationIsAnError(t *testing.T) {
	for name := range vectors {
		data := inflated(t, name)
		if _, err := parsePickle(data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for n := 0; n < len(data); n++ {
			if v, err := parsePickle(data[:n]); err == nil {
				t.Fatalf("%s cut to %d of %d bytes decoded to %#v", name, n, len(data), v)
			}
		}
	}
}

// Damaged pickles may decode to something else or fail, but never panic or hang.
func TestDamagedPicklesAreHandled(t *testing.T) {
	for name := range vectors {
		data := inflated(t, name)
		for i := range data {
			for _, b := range []byte{0x00, 0x01, 0x28, 0x2e, 0x52, 0x62, 0x80, 0xff} {
				damaged := append([]byte(nil), data...)
				damaged[i] = b
				if v, err := parsePickle(damaged); err == nil {
					native(v)
					asPassword(v)
					asBool(v)
					asTime(v)
				}
			}
		}
	}
}

func TestNestingIsNotRecursive(t *testing.T) {
	nested := func(depth int) string { return ")" + strings.Repeat("\x85", depth) + "." }
	v, err := parse(nested(150_000)) // each level costs one opcode, so this stays within the budget
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := native(v).(pyTuple); !ok {
		t.Errorf("got %T", v)
	}
	if _, err := parse(nested(250_000)); !errors.Is(err, errLimit) {
		t.Errorf("past the opcode budget: %v", err)
	}
}

func TestSharedAndCyclicValues(t *testing.T) {
	shared, err := parse("](K\x01e\x94h\x00\x86.") // x = [1]; return (x, x)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pair := shared.(pyTuple)
	if pair[0] != pair[1] {
		t.Error("a memoized list must be the same value when fetched again")
	}
	cyclic, err := parse("]\x94h\x00a.") // x = []; x.append(x)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if l := cyclic.(*pyList); len(l.items) != 1 || l.items[0] != cyclic {
		t.Error("the list must contain itself")
	}
}

func TestInflateLimits(t *testing.T) {
	valid := zl([]byte("\x80\x04N."))
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-1] ^= 0xff // the Adler-32 checksum
	atLimit := bytes.Repeat([]byte{'N', '0'}, maxInflated/2)
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"empty", nil, errInflate},
		{"not zlib", []byte("this is definitely not zlib"), errInflate},
		{"truncated", valid[:len(valid)-3], errInflate},
		{"bad checksum", corrupt, errInflate},
		{"zip bomb", zl(bytes.Repeat([]byte{'N'}, 4<<20)), errTooLarge},
		{"one byte over the limit", zl(append(atLimit, '.')), errTooLarge},
		{"compressed input over the limit", make([]byte, maxInflated+1), errTooLarge},
		{"exactly the limit is inflated, then runs out of opcodes", zl(atLimit), errLimit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v, err := decodeField(c.raw); !errors.Is(err, c.want) {
				t.Errorf("got %#v, %v; want %v", v, err, c.want)
			}
		})
	}
	if v, err := decodeField(valid); err != nil || v != nil {
		t.Errorf("a valid value: %v, %v", v, err)
	}
}

// Pickles that call os.system or eval must decode to data and be rejected as values.
func TestCallsStayInert(t *testing.T) {
	payloads := map[string]string{
		"os.system, protocol 0":       "cos\nsystem\n(S'echo pwned'\ntR.",
		"builtins.eval, STACK_GLOBAL": "\x80\x04\x8c\x08builtins\x8c\x04eval\x93\x8c\x03abc\x85R.",
		"subprocess.Popen, NEWOBJ_EX": "\x80\x04\x8c\x0asubprocess\x8c\x05Popen\x93)}\x92.",
		"object with a state":         "cfoo\nbar\n)\x81}S'x'\nI1\nsb.",
	}
	for name, program := range payloads {
		t.Run(name, func(t *testing.T) {
			v, err := decodeField(zl([]byte(program)))
			if err != nil {
				t.Fatalf("decodeField: %v", err)
			}
			o, ok := v.(*pyObject)
			if !ok {
				t.Fatalf("got %T, want an inert object", v)
			}
			if native(v) != value(o) {
				t.Error("native must leave an unknown class alone")
			}
			if s, ok := asString(v); ok {
				t.Errorf("asString accepted it: %q", s)
			}
			if _, ok := asOptionalString(v); ok {
				t.Error("asOptionalString accepted it")
			}
			if _, ok := asPassword(v); ok {
				t.Error("asPassword accepted it")
			}
			if _, ok := asBool(v); ok {
				t.Error("asBool accepted it")
			}
			if _, ok := asTime(v); ok {
				t.Error("asTime accepted it")
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	for name := range vectors {
		f.Add(inflated(f, name))
	}
	for _, seed := range []string{
		"cos\nsystem\n(S'echo pwned'\ntR.", "\x80\x04\x8c\x02os\x8c\x06system\x93\x8c\x01x\x85R.", "(K\x01K\x02d.",
		"]\x94h\x00a.", "X\xff\xff\xff\xff", "\x8d" + strings.Repeat("\xff", 8), "(((((((((((", "i", "I99\n", "V\\u12\n.",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if v, err := parsePickle(data); err == nil {
			native(v)
			asString(v)
			asOptionalString(v)
			asPassword(v)
			asBool(v)
			asTime(v)
		}
		decodeField(data)
		decodeField(zl(data))
	})
}
