package ntfsindex

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unicode/utf16"
)

// v2 writes a USN_RECORD_V2 the way NTFS does, padded to a multiple of 8 bytes.
func v2(frn, parent uint64, usn, ft int64, reason, attrs uint32, name string) []byte {
	u := utf16.Encode([]rune(name))
	length := (usnRecordV2Header + 2*len(u) + 7) &^ 7
	b := make([]byte, length)
	binary.LittleEndian.PutUint32(b[0:], uint32(length))
	binary.LittleEndian.PutUint16(b[4:], 2)
	binary.LittleEndian.PutUint64(b[8:], frn)
	binary.LittleEndian.PutUint64(b[16:], parent)
	binary.LittleEndian.PutUint64(b[24:], uint64(usn))
	binary.LittleEndian.PutUint64(b[32:], uint64(ft))
	binary.LittleEndian.PutUint32(b[40:], reason)
	binary.LittleEndian.PutUint32(b[52:], attrs)
	binary.LittleEndian.PutUint16(b[56:], uint16(2*len(u)))
	binary.LittleEndian.PutUint16(b[58:], usnRecordV2Header)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[usnRecordV2Header+2*i:], c)
	}
	return b
}

func TestParseUSNReadsRecordsInARow(t *testing.T) {
	buf := append(v2(ref(100), ref(5), 1001, 133000000000000000, reasonFileCreate|reasonClose, 0x20, "a.txt"),
		v2(ref(101), ref(100), 1002, 133000000000000001, reasonRenameNewName, attrDirectory, "文档 café 😀")...)
	var got []usnRecord
	if err := parseUSN(buf, func(r usnRecord) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	want := []usnRecord{
		{FRN: ref(100), Parent: ref(5), USN: 1001, Time: 133000000000000000, Reason: reasonFileCreate | reasonClose, Attrs: 0x20, Name: "a.txt"},
		{FRN: ref(101), Parent: ref(100), USN: 1002, Time: 133000000000000001, Reason: reasonRenameNewName, Attrs: attrDirectory, Name: "文档 café 😀"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("records = %+v, want %+v", got, want)
	}
	if err := parseUSN(nil, func(usnRecord) { t.Error("a record out of nothing") }); err != nil {
		t.Errorf("an empty buffer: %v", err)
	}
}

func TestParseUSNSkipsOtherVersionsAndRefusesDamage(t *testing.T) {
	v3 := make([]byte, 80)
	binary.LittleEndian.PutUint32(v3, 80)
	binary.LittleEndian.PutUint16(v3[4:], 3)
	buf := append(append(v2(ref(1), ref(5), 1, 0, reasonFileCreate, 0, "x"), v3...), v2(ref(2), ref(5), 2, 0, reasonFileCreate, 0, "y")...)
	var names []string
	if err := parseUSN(buf, func(r usnRecord) { names = append(names, r.Name) }); err != nil || !reflect.DeepEqual(names, []string{"x", "y"}) {
		t.Errorf("a record of version 3 between two of version 2: %v, %v", names, err)
	}

	good := v2(ref(1), ref(5), 1, 0, reasonFileCreate, 0, "name")
	damaged := map[string]func() []byte{
		"shorter than its header": func() []byte { return good[:20] },
		"length beyond the buffer": func() []byte {
			b := append([]byte{}, good...)
			binary.LittleEndian.PutUint32(b, uint32(len(b)+8))
			return b
		},
		"length zero": func() []byte {
			b := append([]byte{}, good...)
			binary.LittleEndian.PutUint32(b, 0)
			return b
		},
		"name beyond the record": func() []byte {
			b := append([]byte{}, good...)
			binary.LittleEndian.PutUint16(b[56:], 400)
			return b
		},
		"odd name length": func() []byte {
			b := append([]byte{}, good...)
			binary.LittleEndian.PutUint16(b[56:], 5)
			return b
		},
		"name before the header ends": func() []byte {
			b := append([]byte{}, good...)
			binary.LittleEndian.PutUint16(b[58:], 8)
			return b
		},
	}
	for name, make := range damaged {
		if err := parseUSN(make(), func(usnRecord) {}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRecordChangedIgnoresTheClosingOnItsOwn(t *testing.T) {
	if (usnRecord{Reason: reasonClose}).changed() {
		t.Error("a close alone is not a change")
	}
	if !(usnRecord{Reason: reasonClose | 0x2}).changed() || !(usnRecord{Reason: reasonFileDelete}).changed() {
		t.Error("a close with an extension, and a delete, are changes")
	}
}

func TestFiletimeRoundTrip(t *testing.T) {
	ft := filetime(fromFiletime(133_540_924_280_000_000))
	if ft != 133_540_924_280_000_000 {
		t.Errorf("round trip = %d", ft)
	}
	if !fromFiletime(0).IsZero() || !fromFiletime(5).IsZero() {
		t.Error("an implausible FILETIME must give the zero time")
	}
	if got := fromFiletime(unixToFiletime); got.Unix() != 0 {
		t.Errorf("the Unix epoch = %v", got)
	}
}

func TestFrnKeyDropsTheSequenceNumber(t *testing.T) {
	if frnKey(ref(100)) != 100 || frnKey(0x0005000000000005) != 5 {
		t.Error("the sequence number is part of the key")
	}
}
