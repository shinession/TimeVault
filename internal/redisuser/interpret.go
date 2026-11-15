package redisuser

import (
	"encoding/binary"
	"math"
	"math/big"
	"strconv"
	"time"
)

// The functions here read the inert tree of parsePickle. They match a few
// known shapes of Python values and nothing else; a pickle that calls any
// other class, such as os.system, stays a *pyObject and is rejected.

var (
	clsNumpyScalar   = pyGlobal{"numpy._core.multiarray", "scalar"}
	clsNumpyScalarV1 = pyGlobal{"numpy.core.multiarray", "scalar"}
	clsNumpyDtype    = pyGlobal{"numpy", "dtype"}
	clsDatetime      = pyGlobal{"datetime", "datetime"}
	clsTimezone      = pyGlobal{"datetime", "timezone"}
	clsTimedelta     = pyGlobal{"datetime", "timedelta"}
	clsPytzUTC       = pyGlobal{"pytz", "_UTC"}
	clsPytzZone      = pyGlobal{"pytz", "_p"}
	clsTimestamp     = pyGlobal{"pandas._libs.tslibs.timestamps", "_unpickle_timestamp"}
	clsNaT           = pyGlobal{"pandas._libs.tslibs.nattype", "__nat_unpickle"}
	clsCodecsEncode  = pyGlobal{"_codecs", "encode"}
)

// Seconds since the epoch of 0001-01-01T00:00:00Z and 9999-12-31T23:59:59Z,
// the range of Python's datetime. Anything outside is not a plausible UpdateDate.
const (
	minUnix = -62135596800
	maxUnix = 253402300799
)

// native turns the objects of numpy scalars, datetime.datetime and
// pandas.Timestamp into bool, int64, *big.Int, float64 or time.Time. Any other
// value, including an object of an unknown class, is returned unchanged.
func native(v value) value {
	o, ok := v.(*pyObject)
	if !ok {
		return v
	}
	switch o.Class {
	case clsNumpyScalar, clsNumpyScalarV1:
		if n, ok := numpyScalar(o); ok {
			return n
		}
	case clsDatetime:
		if t, ok := datetimeValue(o); ok {
			return t
		}
	case clsTimestamp:
		if t, ok := timestampValue(o); ok {
			return t
		}
	}
	return v
}

// asString accepts a Python str.
func asString(v value) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// asOptionalString accepts a Python str or None, which reads as "".
func asOptionalString(v value) (string, bool) {
	if v == nil {
		return "", true
	}
	return asString(v)
}

// asPassword accepts a str, None (no password), an int (as its decimal text)
// and a whole float (as Python's str() prints it, 123456.0). NaN and the like
// are not passwords.
func asPassword(v value) (string, bool) {
	switch x := native(v).(type) {
	case nil:
		return "", true
	case string:
		return x, true
	case int64:
		return strconv.FormatInt(x, 10), true
	case *big.Int:
		if x.BitLen() > 1024 {
			return "", false
		}
		return x.String(), true
	case float64:
		if x != math.Trunc(x) || math.Abs(x) >= 1e16 {
			return "", false
		}
		return strconv.FormatFloat(x, 'f', 1, 64), true
	}
	return "", false
}

// asBool accepts a bool, an int that is 0 or 1 (numpy scalars included) and
// None, which reads as false. A string is never converted: "False" is not False.
func asBool(v value) (bool, bool) {
	switch x := native(v).(type) {
	case nil:
		return false, true
	case bool:
		return x, true
	case int64:
		return x == 1, x == 0 || x == 1
	}
	return false, false
}

// asTime accepts datetime.datetime, pandas.Timestamp and numpy.datetime64.
// The zero time means "unknown". A time without zone is read as UTC; it only
// has to order records of the same table consistently.
func asTime(v value) (time.Time, bool) {
	t, ok := native(v).(time.Time)
	return t, ok
}

// isMissingTime reports None and pandas.NaT, the two ways a date is missing.
func isMissingTime(v value) bool {
	o, ok := v.(*pyObject)
	return v == nil || ok && o.Class == clsNaT
}

// asBytes accepts bytes, and the forms that protocols 0 to 2 have for them: a
// str of Latin-1 code points, or _codecs.encode(that str, "latin1").
func asBytes(v value) ([]byte, bool) {
	switch x := v.(type) {
	case []byte:
		return x, true
	case string:
		return latin1(x)
	case *pyObject:
		if x.Class != clsCodecsEncode || len(x.Args) != 2 {
			return nil, false
		}
		s, ok := x.Args[0].(string)
		if enc, _ := x.Args[1].(string); !ok || (enc != "latin1" && enc != "latin-1") {
			return nil, false
		}
		return latin1(s)
	}
	return nil, false
}

func latin1(s string) ([]byte, bool) {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			return nil, false
		}
		b = append(b, byte(r))
	}
	return b, true
}

// numpyScalar reads numpy.core.multiarray.scalar(dtype, bytes), which is how
// numpy pickles np.bool_, np.int64, np.float64, np.datetime64 and friends.
func numpyScalar(o *pyObject) (value, bool) {
	if len(o.Args) != 2 {
		return nil, false
	}
	dtype, ok := o.Args[0].(*pyObject)
	data, dataOK := asBytes(o.Args[1])
	if !ok || !dataOK || dtype.Class != clsNumpyDtype || len(dtype.Args) == 0 {
		return nil, false
	}
	code, ok := dtype.Args[0].(string) // "b1", "i8", "f8", "M8", ...
	if !ok || len(code) < 2 {
		return nil, false
	}
	size, err := strconv.Atoi(code[1:])
	if err != nil || size < 1 || size != len(data) {
		return nil, false
	}
	state, _ := dtype.State.(pyTuple) // (version, byteorder, ..., metadata)
	var order binary.ByteOrder = binary.LittleEndian
	if len(state) > 1 && state[1] == ">" {
		order = binary.BigEndian
	}
	switch code[0] {
	case 'b':
		if size != 1 || data[0] > 1 {
			return nil, false
		}
		return data[0] == 1, true
	case 'i', 'u':
		if size != 1 && size != 2 && size != 4 && size != 8 {
			return nil, false
		}
		return integer(data, order, code[0] == 'i'), true
	case 'f':
		switch size {
		case 4:
			return float64(math.Float32frombits(order.Uint32(data))), true
		case 8:
			return math.Float64frombits(order.Uint64(data)), true
		}
	case 'M':
		if size != 8 {
			return nil, false
		}
		unit, ok := datetimeUnit(state)
		if !ok {
			return nil, false
		}
		return timeFromUnit(int64(order.Uint64(data)), unit)
	}
	return nil, false
}

// integer reads a 1, 2, 4 or 8 byte integer.
func integer(data []byte, order binary.ByteOrder, signed bool) value {
	var u uint64
	switch len(data) {
	case 1:
		u = uint64(data[0])
		if signed {
			return int64(int8(u))
		}
	case 2:
		u = uint64(order.Uint16(data))
		if signed {
			return int64(int16(u))
		}
	case 4:
		u = uint64(order.Uint32(data))
		if signed {
			return int64(int32(u))
		}
	default:
		u = order.Uint64(data)
		if signed {
			return int64(u)
		}
	}
	if u > math.MaxInt64 {
		return new(big.Int).SetUint64(u)
	}
	return int64(u)
}

// datetimeUnit finds the unit of a datetime64 dtype. Its state ends with
// (dict, (unit, count, ...)), where the unit is bytes such as b"s" or b"ns".
func datetimeUnit(state pyTuple) (string, bool) {
	if len(state) < 9 {
		return "", false
	}
	meta, ok := state[8].(pyTuple)
	if !ok || len(meta) != 2 {
		return "", false
	}
	parts, ok := meta[1].(pyTuple)
	if !ok || len(parts) < 2 || parts[1] != int64(1) {
		return "", false
	}
	unit, ok := parts[0].([]byte)
	return string(unit), ok
}

// timeFromUnit converts a count of the numpy unit to a time. NaT, the smallest
// int64, falls outside the valid range. Years, months and weeks are not fixed
// lengths and are not supported.
func timeFromUnit(n int64, unit string) (value, bool) {
	var t time.Time
	var ok bool
	switch unit {
	case "D":
		t, ok = fromCoarse(n, 86400)
	case "h":
		t, ok = fromCoarse(n, 3600)
	case "m":
		t, ok = fromCoarse(n, 60)
	case "s":
		t, ok = fromSubSecond(n, 1)
	case "ms":
		t, ok = fromSubSecond(n, 1e3)
	case "us":
		t, ok = fromSubSecond(n, 1e6)
	case "ns":
		t, ok = fromSubSecond(n, 1e9)
	}
	if !ok {
		return nil, false
	}
	return t, true
}

func fromCoarse(n, seconds int64) (time.Time, bool) {
	if n > maxUnix/seconds || n < minUnix/seconds {
		return time.Time{}, false
	}
	return time.Unix(n*seconds, 0).UTC(), true
}

func fromSubSecond(n, perSecond int64) (time.Time, bool) {
	sec, rem := n/perSecond, n%perSecond
	if rem < 0 {
		sec, rem = sec-1, rem+perSecond
	}
	if sec < minUnix || sec > maxUnix {
		return time.Time{}, false
	}
	return time.Unix(sec, rem*(1e9/perSecond)).UTC(), true
}

// timestampValue reads pandas._libs.tslibs.timestamps._unpickle_timestamp(value, freq, tz, reso).
// The count is relative to the epoch in UTC whatever tz is, so tz is not needed.
// pandas 2 adds reso, a numpy unit code; older pickles have three arguments and count nanoseconds.
func timestampValue(o *pyObject) (time.Time, bool) {
	if len(o.Args) != 3 && len(o.Args) != 4 {
		return time.Time{}, false
	}
	n, ok := o.Args[0].(int64)
	if !ok {
		return time.Time{}, false
	}
	reso := int64(10)
	if len(o.Args) == 4 {
		if reso, ok = o.Args[3].(int64); !ok {
			return time.Time{}, false
		}
	}
	switch reso {
	case 7:
		return fromSubSecond(n, 1)
	case 8:
		return fromSubSecond(n, 1e3)
	case 9:
		return fromSubSecond(n, 1e6)
	case 10:
		return fromSubSecond(n, 1e9)
	}
	return time.Time{}, false
}

// datetimeValue reads datetime.datetime(state[, tzinfo]). The 10 byte state is
// year (2 bytes), month, day, hour, minute, second and microsecond (3 bytes).
func datetimeValue(o *pyObject) (time.Time, bool) {
	if len(o.Args) < 1 || len(o.Args) > 2 {
		return time.Time{}, false
	}
	b, ok := asBytes(o.Args[0])
	if !ok || len(b) != 10 {
		return time.Time{}, false
	}
	year, month, day := int(b[0])<<8|int(b[1]), int(b[2]&0x7f), int(b[3]) // the high bit of the month is "fold"
	hour, minute, second := int(b[4]), int(b[5]), int(b[6])
	micro := int(b[7])<<16 | int(b[8])<<8 | int(b[9])
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || second > 59 || micro > 999_999 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, micro*1000, time.UTC)
	if t.Day() != day { // February 30th and the like
		return time.Time{}, false
	}
	if len(o.Args) == 2 {
		// A zone that is not a fixed offset (zoneinfo, ...) is read as UTC rather than dropping the time.
		if off, ok := utcOffset(o.Args[1]); ok {
			t = t.Add(-off)
		}
	}
	return t, true
}

// utcOffset reads datetime.timezone(timedelta), pytz.utc and the pytz zone
// objects, which carry their current offset in seconds.
func utcOffset(v value) (time.Duration, bool) {
	o, ok := v.(*pyObject)
	if !ok {
		return 0, false
	}
	var seconds int64
	switch o.Class {
	case clsPytzUTC:
		return 0, true
	case clsTimezone:
		if len(o.Args) == 0 {
			return 0, false
		}
		delta, ok := o.Args[0].(*pyObject)
		if !ok || delta.Class != clsTimedelta || len(delta.Args) > 3 {
			return 0, false
		}
		var parts [3]int64 // days, seconds, microseconds
		for i, a := range delta.Args {
			if parts[i], ok = a.(int64); !ok {
				return 0, false
			}
		}
		if parts[0] < -1 || parts[0] > 0 { // an offset is less than a day
			return 0, false
		}
		seconds = parts[0]*86400 + parts[1]
	case clsPytzZone:
		if len(o.Args) < 2 {
			return 0, false
		}
		if seconds, ok = o.Args[1].(int64); !ok {
			return 0, false
		}
	default:
		return 0, false
	}
	if seconds <= -86400 || seconds >= 86400 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
