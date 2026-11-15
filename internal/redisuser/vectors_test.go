package redisuser

import (
	"bytes"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"
)

var (
	wall     = utc(2026, 11, 15, 14, 30, 1, 123456000)
	wallSGT  = utc(2026, 11, 15, 6, 30, 1, 123456000) // the same instant at +08:00
	bytes256 = append(func() []byte {
		b := make([]byte, 256)
		for i := range b {
			b[i] = byte(i)
		}
		return b
	}(), "xyz"...)
	structMixed = &pyList{items: []value{
		int64(1),
		&pyList{items: []value{int64(2), int64(3)}},
		&pyDict{keys: []value{"a", "b", int64(7)}, vals: []value{pyTuple{int64(4)}, nil, 1.5}},
		pyTuple{},
		pyTuple{"x"},
		pyTuple{int64(1), int64(2), int64(3), int64(4)},
	}}
	structShared = &pyList{items: []value{&pyList{items: []value{int64(1)}}, &pyList{items: []value{int64(1)}}}}
	structCycle  = func() *pyList { l := &pyList{}; l.items = []value{l}; return l }()
)

// vectorWant is what each vector of vectors_data_test.go decodes to, after native().
// The variants of a value in other protocols ("p0_str_ascii") share the entry of the
// value ("str_ascii") unless they have an entry of their own.
var vectorWant = map[string]any{
	"str_ascii":    "Hello, TimeVault",
	"str_cjk":      "陈小明",
	"str_emoji":    "ok \U0001F642",
	"str_empty":    "",
	"str_long":     strings.Repeat("0123456789", 35),
	"true":         true,
	"false":        false,
	"none":         nil,
	"int_0":        int64(0),
	"int_1":        int64(1),
	"int_255":      int64(255),
	"int_256":      int64(256),
	"int_65535":    int64(65535),
	"int_65536":    int64(65536),
	"int_neg1":     int64(-1),
	"int_2p31":     int64(1) << 31,
	"int_2p63":     mustBig("9223372036854775808"),
	"int_1e20":     mustBig("100000000000000000000"),
	"int_neg2p70":  mustBig("-1180591620717411303424"),
	"int_2p3000":   new(big.Int).Lsh(big.NewInt(1), 3000),
	"float_1_5":    1.5,
	"float_nan":    math.NaN(),
	"float_inf":    math.Inf(1),
	"float_123456": 123456.0,

	"datetime_naive":    wall,
	"datetime_escapes":  utc(2026, 11, 10, 10, 10, 10, 23562000),
	"datetime_utc":      wall,
	"datetime_sgt":      wallSGT,
	"datetime_pytz_sgt": wallSGT,
	"datetime_pytz_utc": wall,
	"datetime_zoneinfo": wall, // a zone that is not a fixed offset is read as UTC
	"ts_us":             wall,
	"ts_ns":             utc(2026, 11, 15, 14, 30, 1, 123456789),
	"ts_ms":             utc(2026, 11, 15, 14, 30, 1, 123000000),
	"ts_s":              utc(2026, 11, 15, 14, 30, 1, 0),
	"ts_pre_epoch":      utc(1969, 12, 31, 23, 59, 59, 500000000),
	"ts_tz_sgt":         utc(2026, 11, 15, 6, 30, 1, 0),
	"ts_tz_utc":         utc(2026, 11, 15, 14, 30, 1, 0),
	"ts_tz_fixed":       utc(2026, 11, 15, 6, 30, 1, 0),
	"ts_nat":            inert{"pandas._libs.tslibs.nattype", "__nat_unpickle"},

	"np_bool_true":  true,
	"np_bool_false": false,
	"np_int64":      int64(123456),
	"np_int32":      int64(-7),
	"np_uint8":      int64(200),
	"np_float64":    1.5,
	"np_float32":    2.5,
	"np_dt64_s":     utc(2026, 11, 15, 14, 30, 1, 0),
	"np_dt64_ns":    utc(2026, 11, 15, 14, 30, 1, 123456789),
	"np_dt64_nat":   inert{"numpy._core.multiarray", "scalar"},

	"struct_mixed":    structMixed,
	"struct_shared":   structShared,
	"struct_cycle":    structCycle,
	"set_small":       &pySet{items: []value{int64(5), int64(6)}},
	"p0_set_small":    inert{"__builtin__", "set"},
	"p2_set_small":    inert{"__builtin__", "set"},
	"frozenset_small": &pySet{items: []value{int64(7)}},
	"bytes_short":     []byte("abc"),
	"bytes_long":      bytes256,

	"bytearray_small":    inert{"builtins", "bytearray"},
	"p5_bytearray_small": []byte("ba"),
}

func TestVectors(t *testing.T) {
	covered := map[string]bool{}
	for name := range vectors {
		key := name
		if _, ok := vectorWant[name]; !ok && len(name) > 3 && name[0] == 'p' && name[2] == '_' {
			key = name[3:] // the same value pickled with another protocol
		}
		want, ok := vectorWant[key]
		if !ok {
			t.Errorf("vector %s has no expectation", name)
			continue
		}
		covered[key] = true
		t.Run(name, func(t *testing.T) {
			v, err := decodeField(vector(t, name))
			if err != nil {
				t.Fatalf("decodeField: %v", err)
			}
			if got := native(v); !same(got, want) {
				t.Errorf("decoded %#v, want %#v", got, want)
			}
		})
	}
	for name := range vectorWant {
		if !covered[name] {
			t.Errorf("expectation %s has no vector", name)
		}
	}
}

func TestSharedStructureOfVectors(t *testing.T) {
	v, err := decodeField(vector(t, "struct_shared"))
	if err != nil {
		t.Fatal(err)
	}
	if l := v.(*pyList); l.items[0] != l.items[1] {
		t.Error("the two entries are one list in Python and must be one value")
	}
	v, err = decodeField(vector(t, "struct_cycle"))
	if err != nil {
		t.Fatal(err)
	}
	if l := v.(*pyList); l.items[0] != value(l) {
		t.Error("the list contains itself")
	}
}

// The protocol 2 encoding of bytes, _codecs.encode(str, "latin1"), is read where bytes are expected.
func TestLegacyBytesEncoding(t *testing.T) {
	for _, name := range []string{"p0_datetime_escapes", "p1_datetime_naive", "p2_datetime_naive", "p3_datetime_naive", "p5_datetime_naive"} {
		v, err := decodeField(vector(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, isTime := native(v).(time.Time); !isTime {
			t.Errorf("%s: got %#v", name, native(v))
		}
	}
	v, err := decodeField(vector(t, "p2_np_int64"))
	if err != nil {
		t.Fatal(err)
	}
	if got := native(v); got != value(int64(123456)) {
		t.Errorf("p2 numpy scalar: got %#v", got)
	}
}

// pyPickle must write what Python writes, or the users seeded by the store tests would not be realistic.
func TestEncoderMatchesPython(t *testing.T) {
	cases := map[string]any{
		"str_ascii": "Hello, TimeVault", "str_cjk": "陈小明", "str_empty": "", "str_long": strings.Repeat("0123456789", 35),
		"true": true, "false": false, "none": nil, "datetime_naive": wall,
		"ts_ns": timestamp(utc(2026, 11, 15, 14, 30, 1, 123456789)),
	}
	for name, val := range cases {
		if got, want := pyPickle(val), inflated(t, name); !bytes.Equal(got, want) {
			t.Errorf("%s: pyPickle wrote % x, Python wrote % x", name, got, want)
		}
	}
}

// The hashes are built like refresh_app_user_data() builds them, DataFrame included.
func TestDashHashes(t *testing.T) {
	cases := []struct {
		key      string
		want     Record
		password string
	}{
		{
			"app_user_e1001_alice tan_alice.tan@example.test",
			Record{ECode: "E1001", Name: "Alice Tan", Email: "alice.tan@example.test", Type: "user", Activated: true, Updated: utc(2026, 11, 15, 14, 30, 1, 250000000)},
			"fake-Secret-1",
		},
		{
			"app_user_e1002_bob lee_bob.lee@example.test",
			Record{ECode: "E1002", Name: "Bob Lee", Email: "bob.lee@example.test", Type: "admin", Updated: utc(2026, 11, 14, 9, 0, 0, 0)},
			"",
		},
		{
			"app_user_e1003_陈小明_chen.xm@example.test",
			Record{ECode: "E1003", Name: "陈小明", Email: "chen.xm@example.test", Type: "user", Activated: true, Updated: utc(2026, 11, 13, 8, 15, 0, 0)},
			"fake-密码-3",
		},
	}
	if len(dashHashes) != len(cases) {
		t.Fatalf("%d hashes, %d cases", len(dashHashes), len(cases))
	}
	for _, c := range cases {
		hash := rawHash(t, c.key)
		info := decodeHash(c.key, hash, true)
		if !info.usable {
			t.Fatalf("%s: unreadable: %v", c.key, info.reason)
		}
		for i, spec := range fieldSpecs {
			if info.state[i] != fieldOK {
				t.Errorf("%s: field %s is %v (%s)", c.key, spec.name, info.state[i], info.reason[i])
			}
		}
		got := info.rec
		if got.Ref != c.key || got.ECode != c.want.ECode || got.Name != c.want.Name || got.Email != c.want.Email ||
			got.Type != c.want.Type || got.Activated != c.want.Activated || !got.Updated.Equal(c.want.Updated) {
			t.Errorf("record = %#v\nwant   %#v", got, c.want)
		}
		if got.HasPassword() != (c.password != "") || got.CheckPassword(c.password) != (c.password != "") {
			t.Errorf("%s: password check failed", c.key)
		}
		if got.CheckPassword("0") || got.CheckPassword(c.password+"x") || got.CheckPassword(strings.ToUpper(c.password+"x")) {
			t.Errorf("%s: a wrong password was accepted", c.key)
		}
	}
}

// rawHash returns the fields of a generated hash as Redis hands them out.
func rawHash(t testing.TB, key string) map[string]string {
	t.Helper()
	fields, ok := dashHashes[key]
	if !ok {
		t.Fatalf("no hash %q", key)
	}
	raw := make(map[string]string, len(fields))
	for name, h := range fields {
		raw[name] = string(fromHex(t, h))
	}
	return raw
}
