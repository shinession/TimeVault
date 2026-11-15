package redisuser

import (
	"math"
	"math/big"
	"testing"
	"time"
)

func call(module, name string, args ...value) *pyObject {
	return &pyObject{Class: pyGlobal{module, name}, Args: pyTuple(args)}
}

func TestAsPassword(t *testing.T) {
	cases := []struct {
		name string
		in   value
		want string
		ok   bool
	}{
		{"None is no password", nil, "", true},
		{"str", "pw", "pw", true},
		{"empty str", "", "", true},
		{"int", int64(0), "0", true},
		{"negative int", int64(-5), "-5", true},
		{"big int", mustBig("100000000000000000000"), "100000000000000000000", true},
		{"huge int", new(big.Int).Lsh(big.NewInt(1), 2000), "", false},
		{"whole float, as str() prints it", 123456.0, "123456.0", true},
		{"negative zero", math.Copysign(0, -1), "-0.0", true},
		{"fraction", 1.5, "", false},
		{"NaN", math.NaN(), "", false},
		{"infinity", math.Inf(1), "", false},
		{"float too big to print like an int", 1e16, "", false},
		{"bool", true, "", false},
		{"bytes", []byte("pw"), "", false},
		{"tuple", pyTuple{"pw"}, "", false},
		{"time", utc(2026, 1, 1, 0, 0, 0, 0), "", false},
		{"object", &pyObject{}, "", false},
	}
	for _, c := range cases {
		got, ok := asPassword(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: asPassword(%#v) = %q, %v; want %q, %v", c.name, c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestAsBool(t *testing.T) {
	cases := []struct {
		name     string
		in       value
		got, set bool
	}{
		{"True", true, true, true},
		{"False", false, false, true},
		{"None", nil, false, true},
		{"0", int64(0), false, true},
		{"1", int64(1), true, true},
		{"2", int64(2), false, false},
		{"-1", int64(-1), false, false},
		{"the text True", "True", false, false},
		{"the text False", "False", false, false},
		{"the empty text", "", false, false},
		{"the text 0", "0", false, false},
		{"1.0", 1.0, false, false},
		{"NaN", math.NaN(), false, false},
		{"big int", mustBig("100000000000000000000"), false, false},
		{"bytes", []byte{1}, false, false},
		{"object", &pyObject{}, false, false},
	}
	for _, c := range cases {
		if got, ok := asBool(c.in); got != c.got || ok != c.set {
			t.Errorf("%s: asBool(%#v) = %v, %v; want %v, %v", c.name, c.in, got, ok, c.got, c.set)
		}
	}
}

func TestMissingTime(t *testing.T) {
	nat := call("pandas._libs.tslibs.nattype", "__nat_unpickle", nil)
	for _, v := range []value{nil, nat} {
		if !isMissingTime(v) {
			t.Errorf("%#v is a missing date", v)
		}
		if _, ok := asTime(v); ok {
			t.Errorf("%#v is not a time", v)
		}
	}
	for _, v := range []value{"", int64(0), false, call("os", "system"), utc(2026, 1, 1, 0, 0, 0, 0), []byte{}} {
		if isMissingTime(v) {
			t.Errorf("%#v is not a missing date", v)
		}
	}
}

func TestStringKinds(t *testing.T) {
	if s, ok := asString("x"); !ok || s != "x" {
		t.Error("a str is a string")
	}
	for _, v := range []value{nil, int64(1), []byte("x"), true} {
		if _, ok := asString(v); ok {
			t.Errorf("asString(%#v) must fail", v)
		}
	}
	if s, ok := asOptionalString(nil); !ok || s != "" {
		t.Error("None reads as the empty string")
	}
	if _, ok := asOptionalString(int64(1)); ok {
		t.Error("an int is not a string")
	}
}

func TestAsBytes(t *testing.T) {
	cases := []struct {
		name string
		in   value
		want string
		ok   bool
	}{
		{"bytes", []byte("abc"), "abc", true},
		{"Latin-1 str", "caf\u00e9", "caf\xe9", true},
		{"str beyond Latin-1", "\u9648", "", false},
		{"_codecs.encode latin1", call("_codecs", "encode", "caf\u00e9", "latin1"), "caf\xe9", true},
		{"_codecs.encode latin-1", call("_codecs", "encode", "abc", "latin-1"), "abc", true},
		{"_codecs.encode utf-8", call("_codecs", "encode", "abc", "utf-8"), "", false},
		{"_codecs.encode, one argument", call("_codecs", "encode", "abc"), "", false},
		{"another call", call("os", "system", "abc", "latin1"), "", false},
		{"int", int64(1), "", false},
	}
	for _, c := range cases {
		if got, ok := asBytes(c.in); string(got) != c.want || ok != c.ok {
			t.Errorf("%s: asBytes = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func dtype(code string, state pyTuple) *pyObject {
	o := call("numpy", "dtype", code, false, true)
	o.State = state
	return o
}

func scalar(d value, data []byte) *pyObject { return call("numpy._core.multiarray", "scalar", d, data) }

// datetime64 is an np.datetime64 as numpy 2 pickles it: the unit and count are in the last item of the dtype state.
func datetime64(unit string, n int64) *pyObject {
	data := make([]byte, 8)
	for i := range data {
		data[i] = byte(uint64(n) >> (8 * i))
	}
	state := pyTuple{int64(4), "<", nil, nil, nil, int64(-1), int64(-1), int64(0), pyTuple{&pyDict{}, pyTuple{[]byte(unit), int64(1), int64(1), int64(1)}}}
	return scalar(dtype("M8", state), data)
}

// Objects that look like a known shape but are not one stay objects, and a time is never invented.
func TestUnsupportedShapesAreRejected(t *testing.T) {
	state := []byte{0x07, 0xea, 0x0b, 0x0f, 0x0e, 0x1e, 0x01, 0x01, 0xe2, 0x40} // 2026-11-15 14:30:01.123456
	with := func(i int, b byte) []byte {
		s := append([]byte(nil), state...)
		s[i] = b
		return s
	}
	datetime := func(args ...value) *pyObject { return call("datetime", "datetime", args...) }
	timestamp := func(args ...value) *pyObject {
		return call("pandas._libs.tslibs.timestamps", "_unpickle_timestamp", args...)
	}
	le := pyTuple{int64(3), "<"}

	cases := map[string]value{
		"datetime, 11 byte state":          datetime(append(state, 0)),
		"datetime, no state":               datetime(),
		"datetime, three arguments":        datetime(state, nil, nil),
		"datetime, state is an int":        datetime(int64(5)),
		"datetime, month 13":               datetime(with(2, 13)),
		"datetime, month 0":                datetime(with(2, 0)),
		"datetime, February 30":            datetime([]byte{0x07, 0xea, 2, 30, 0, 0, 0, 0, 0, 0}),
		"datetime, day 0":                  datetime(with(3, 0)),
		"datetime, hour 24":                datetime(with(4, 24)),
		"datetime, minute 60":              datetime(with(5, 60)),
		"datetime, second 60":              datetime(with(6, 60)),
		"datetime, microsecond 1e6":        datetime([]byte{0x07, 0xea, 11, 15, 14, 30, 1, 0x0f, 0x42, 0x40}),
		"datetime, year 0":                 datetime([]byte{0, 0, 1, 1, 0, 0, 0, 0, 0, 0}, nil),
		"datetime, year 10000":             datetime([]byte{0x27, 0x10, 1, 1, 0, 0, 0, 0, 0, 0}),
		"Timestamp, text value":            timestamp("1794753001123456", nil, nil, int64(9)),
		"Timestamp, unknown unit":          timestamp(int64(1), nil, nil, int64(99)),
		"Timestamp, unit not an int":       timestamp(int64(1), nil, nil, "ns"),
		"Timestamp, two arguments":         timestamp(int64(1), nil),
		"Timestamp, year 10000":            timestamp(int64(253402300800), nil, nil, int64(7)),
		"Timestamp, before year 1":         timestamp(int64(-62135596801), nil, nil, int64(7)),
		"numpy, foreign dtype":             scalar(call("os", "system", "x"), []byte{1}),
		"numpy, dtype is a string":         scalar("i8", []byte{1}),
		"numpy, data is an int":            call("numpy._core.multiarray", "scalar", dtype("i8", le), int64(1)),
		"numpy, one argument":              call("numpy._core.multiarray", "scalar", dtype("i8", le)),
		"numpy, size and data differ":      scalar(dtype("i8", le), []byte{1, 2, 3, 4}),
		"numpy, size 0":                    scalar(dtype("b0", le), nil),
		"numpy, size is not a number":      scalar(dtype("bx", le), []byte{1}),
		"numpy, code of one character":     scalar(dtype("b", le), []byte{1}),
		"numpy, int of 3 bytes":            scalar(dtype("i3", le), []byte{1, 2, 3}),
		"numpy, half float":                scalar(dtype("f2", le), []byte{1, 2}),
		"numpy, complex":                   scalar(dtype("c8", le), make([]byte, 8)),
		"numpy, bool that is 2":            scalar(dtype("b1", le), []byte{2}),
		"numpy, bool of 2 bytes":           scalar(dtype("b2", le), []byte{1, 0}),
		"numpy, datetime without unit":     scalar(dtype("M8", le), make([]byte, 8)),
		"numpy, datetime in years":         datetime64("Y", 5),
		"numpy, datetime in weeks":         datetime64("W", 5),
		"numpy, datetime NaT":              datetime64("s", math.MinInt64),
		"numpy, datetime in days, too far": datetime64("D", 1<<40),
		"numpy, datetime, odd unit count":  scalar(dtype("M8", pyTuple{int64(4), "<", nil, nil, nil, int64(-1), int64(-1), int64(0), pyTuple{&pyDict{}, pyTuple{[]byte("s"), int64(5), int64(1), int64(1)}}}), make([]byte, 8)),
	}
	for name, v := range cases {
		if got := native(v); got != v {
			t.Errorf("%s: native returned %#v", name, got)
		}
		if _, ok := asTime(v); ok {
			t.Errorf("%s: asTime accepted it", name)
		}
	}
}

func TestSupportedShapesOutsideTheVectors(t *testing.T) {
	numpyValue := func(code string, order string, data []byte) value {
		d := dtype(code, pyTuple{int64(3), order})
		return native(call("numpy.core.multiarray", "scalar", d, data)) // numpy 1.x spells the module this way
	}
	cases := []struct {
		name string
		got  value
		want any
	}{
		{"big-endian int", numpyValue("i4", ">", []byte{0, 0, 1, 0}), int64(256)},
		{"native byte order", numpyValue("i2", "=", []byte{0, 1}), int64(256)},
		{"unsigned beyond int64", numpyValue("u8", "<", []byte{255, 255, 255, 255, 255, 255, 255, 255}), mustBig("18446744073709551615")},
		{"signed byte", numpyValue("i1", "|", []byte{0xff}), int64(-1)},
		{"big-endian float", numpyValue("f8", ">", []byte{0x3f, 0xf8, 0, 0, 0, 0, 0, 0}), 1.5},
		{"fold bit of datetime", native(call("datetime", "datetime", []byte{0x07, 0xea, 0x0b | 0x80, 0x0f, 0x0e, 0x1e, 0x01, 0, 0, 0})), utc(2026, 11, 15, 14, 30, 1, 0)},
		{"datetime as Latin-1 text", native(call("datetime", "datetime", "\x07\u00ea\x0b\x0f\x0e\x1e\x01\x00\x00\x00")), utc(2026, 11, 15, 14, 30, 1, 0)},
		{"datetime at -05:00", native(call("datetime", "datetime", []byte{0x07, 0xea, 0x0b, 0x0f, 0x0e, 0x1e, 0x01, 0, 0, 0},
			call("datetime", "timezone", call("datetime", "timedelta", int64(-1), int64(68400), int64(0))))), utc(2026, 11, 15, 19, 30, 1, 0)},
		{"Timestamp from older pandas, nanoseconds", native(call("pandas._libs.tslibs.timestamps", "_unpickle_timestamp", int64(1794753001123456789), nil, nil)), utc(2026, 11, 15, 14, 30, 1, 123456789)},
		{"datetime64 in days", native(datetime64("D", 1)), utc(1970, 1, 2, 0, 0, 0, 0)},
		{"datetime64 in hours", native(datetime64("h", 25)), utc(1970, 1, 2, 1, 0, 0, 0)},
		{"datetime64 in minutes", native(datetime64("m", 61)), utc(1970, 1, 1, 1, 1, 0, 0)},
		{"datetime64 in milliseconds", native(datetime64("ms", 1500)), utc(1970, 1, 1, 0, 0, 1, 500000000)},
		{"datetime64 in microseconds", native(datetime64("us", -1)), utc(1969, 12, 31, 23, 59, 59, 999999000)},
	}
	for _, c := range cases {
		if !same(c.got, c.want) {
			t.Errorf("%s: got %#v, want %#v", c.name, c.got, c.want)
		}
	}
	if tm, ok := asTime(call("datetime", "datetime", []byte{0x07, 0xea, 0x0b, 0x0f, 0, 0, 0, 0, 0, 0})); !ok || tm.Location() != time.UTC {
		t.Errorf("times are in UTC: %v, %v", tm, ok)
	}
}

// A time zone that is not a fixed offset, or is not readable, leaves the wall clock time as it is.
func TestUnknownTimeZonesLeaveTheWallClock(t *testing.T) {
	state := []byte{0x07, 0xea, 0x0b, 0x0f, 0x0e, 0x1e, 0x01, 0, 0, 0}
	zones := map[string]value{
		"None":                           nil,
		"a string":                       "Asia/Singapore",
		"zoneinfo":                       call("zoneinfo", "ZoneInfo._unpickle", "Asia/Singapore", int64(1)),
		"timezone without a timedelta":   call("datetime", "timezone"),
		"timezone of something else":     call("datetime", "timezone", "+08:00"),
		"timedelta of text":              call("datetime", "timezone", call("datetime", "timedelta", "8h")),
		"timedelta with four parts":      call("datetime", "timezone", call("datetime", "timedelta", int64(0), int64(0), int64(0), int64(0))),
		"offset of two days":             call("datetime", "timezone", call("datetime", "timedelta", int64(2), int64(0), int64(0))),
		"offset of a day and a second":   call("datetime", "timezone", call("datetime", "timedelta", int64(0), int64(86401), int64(0))),
		"pytz zone without offset":       call("pytz", "_p", "Asia/Singapore"),
		"pytz zone with a text offset":   call("pytz", "_p", "Asia/Singapore", "28800"),
		"pytz zone with a huge offset":   call("pytz", "_p", "Asia/Singapore", int64(1<<40), int64(0), "x"),
		"timedelta with a negative days": call("datetime", "timezone", call("datetime", "timedelta", int64(-2), int64(0), int64(0))),
	}
	for name, zone := range zones {
		got := native(call("datetime", "datetime", state, zone))
		if !same(got, utc(2026, 11, 15, 14, 30, 1, 0)) {
			t.Errorf("%s: got %#v", name, got)
		}
	}
}
