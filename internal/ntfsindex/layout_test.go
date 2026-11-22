package ntfsindex

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unicode/utf16"
)

// layoutFile describes a file of a synthetic answer of FSCTL_QUERY_FILE_LAYOUT.
type layoutFile struct {
	frn     uint64
	attrs   uint32
	names   []layoutName
	streams []layoutStream
	// lastWrite is the time of the last write, 0 for a file without the extra information.
	lastWrite int64
}

type layoutName struct {
	parent uint64
	name   string
	flags  uint32
}

type layoutStream struct {
	typ  uint32
	id   string
	size int64
}

func put32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
func put64(b []byte, off int, v uint64) { binary.LittleEndian.PutUint64(b[off:], v) }

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func align8(n int) int { return (n + 7) &^ 7 }

// layoutAnswer builds what the file system answers: the header and the entries, with names, streams and the times,
// the way the real answer is laid out (see volume_windows_test.go for what it was checked against).
func layoutAnswer(files ...layoutFile) []byte {
	buf := make([]byte, layoutOutputHeader)
	put32(buf, 0, uint32(len(files)))
	put32(buf, 4, layoutOutputHeader)
	for i, f := range files {
		entry := make([]byte, fileLayoutEntrySize)
		put32(entry, 0, 1)
		put32(entry, 12, f.attrs)
		put64(entry, 16, f.frn)
		// Names.
		var names []byte
		firstName := 0
		if len(f.names) > 0 {
			firstName = len(entry)
		}
		for j, n := range f.names {
			raw := utf16le(n.name)
			h := make([]byte, align8(nameEntryHeader+len(raw)))
			if j < len(f.names)-1 {
				put32(h, 0, uint32(len(h)))
			}
			put32(h, 4, n.flags)
			put64(h, 8, n.parent)
			put32(h, 16, uint32(len(raw)))
			copy(h[nameEntryHeader:], raw)
			names = append(names, h...)
		}
		entry = append(entry, names...)
		// Streams.
		firstStream := 0
		if len(f.streams) > 0 {
			firstStream = len(entry)
		}
		for j, s := range f.streams {
			raw := utf16le(s.id)
			h := make([]byte, align8(streamEntryHeader+len(raw)))
			if j < len(f.streams)-1 {
				put32(h, 4, uint32(len(h)))
			}
			put64(h, 16, uint64(s.size))
			put64(h, 24, uint64(s.size))
			put32(h, 36, s.typ)
			put32(h, 44, uint32(len(raw)))
			copy(h[streamEntryHeader:], raw)
			entry = append(entry, h...)
		}
		// Extra information: four times, the attributes, the owner, the security, the USN.
		extra, extraLen := 0, 0
		if f.lastWrite != 0 {
			info := make([]byte, 56)
			put64(info, 0, 1)
			put64(info, 8, 2)
			put64(info, 16, uint64(f.lastWrite))
			put64(info, 24, 4)
			extra, extraLen = len(entry), len(info)
			entry = append(entry, info...)
		}
		put32(entry, 24, uint32(firstName))
		put32(entry, 28, uint32(firstStream))
		put32(entry, 32, uint32(extra))
		put32(entry, 36, uint32(extraLen))
		if i < len(files)-1 {
			put32(entry, 4, uint32(len(entry)))
		}
		buf = append(buf, entry...)
	}
	return buf
}

func collect(t *testing.T, buf []byte, want func(frn, parent uint64, attrs uint32) bool) []fsEntry {
	t.Helper()
	var got []fsEntry
	if err := parseLayout(buf, want, func(e fsEntry) {
		e.Name = e.name()
		e.Raw = nil
		got = append(got, e)
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestParseLayoutReadsNamesSizesAndTimes(t *testing.T) {
	const primary, dos = nameFlagPrimary, nameFlagDOS
	buf := layoutAnswer(
		layoutFile{frn: ref(100), attrs: 0x20, lastWrite: 133_000_000_000_000_000,
			names: []layoutName{{parent: ref(5), name: "Report 2024.docx", flags: primary}, {parent: ref(5), name: "REPORT~1.DOC", flags: dos}},
			// A file has other attributes than its data: they are listed as streams too, and a named stream is another one.
			streams: []layoutStream{{typ: 0x10, id: "::$STANDARD_INFORMATION", size: 72}, {typ: 0x80, id: ":Zone.Identifier:$DATA", size: 26}, {typ: 0x80, id: "", size: 70_000}}},
		layoutFile{frn: ref(101), attrs: attrDirectory, lastWrite: 133_000_000_000_000_001,
			names:   []layoutName{{parent: ref(100), name: "文档 😀", flags: primary}},
			streams: []layoutStream{{typ: 0x90, id: "::$I30", size: 4096}}},
		// A file without data, and a file with two names in different folders (a hard link).
		layoutFile{frn: ref(102), attrs: 0x20, names: []layoutName{{parent: ref(101), name: "empty", flags: primary}}},
		layoutFile{frn: ref(103), attrs: 0x20, lastWrite: 5,
			names:   []layoutName{{parent: ref(101), name: "one", flags: primary}, {parent: ref(5), name: "two", flags: primary}},
			streams: []layoutStream{{typ: 0x80, id: "", size: 9}}},
	)
	want := []fsEntry{
		{FRN: ref(100), Parent: ref(5), Name: "Report 2024.docx", Attrs: 0x20, Size: 70_000, MTime: 133_000_000_000_000_000},
		{FRN: ref(101), Parent: ref(100), Name: "文档 😀", Attrs: attrDirectory, Size: 0, MTime: 133_000_000_000_000_001},
		{FRN: ref(102), Parent: ref(101), Name: "empty", Attrs: 0x20},
		{FRN: ref(103), Parent: ref(101), Name: "one", Attrs: 0x20, Size: 9, MTime: 5},
		{FRN: ref(103), Parent: ref(5), Name: "two", Attrs: 0x20, Size: 9, MTime: 5},
	}
	if got := collect(t, buf, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("entries:\n got %+v\nwant %+v", got, want)
	}

	// A file that nobody wants is not read: its name is not decoded and the callback is not called.
	only := func(frn, parent uint64, attrs uint32) bool { return frnKey(frn) == 101 }
	if got := collect(t, buf, only); len(got) != 1 || got[0].Name != "文档 😀" {
		t.Errorf("only one wanted: %+v", got)
	}
	called := 0
	if err := parseLayout(buf, func(frn, parent uint64, attrs uint32) bool { called++; return false }, func(fsEntry) { t.Error("an entry that was not wanted") }); err != nil {
		t.Fatal(err)
	}
	if called != 5 {
		t.Errorf("want was asked %d times, once for every name that is not a short one (5)", called)
	}
}

func TestParseLayoutRefusesDamage(t *testing.T) {
	good := layoutAnswer(layoutFile{frn: ref(100), attrs: 0x20, lastWrite: 1,
		names:   []layoutName{{parent: ref(5), name: "a", flags: nameFlagPrimary}},
		streams: []layoutStream{{typ: 0x80, id: "", size: 1}}})
	cases := map[string]func([]byte) []byte{
		"shorter than the header": func(b []byte) []byte { return b[:8] },
		"entry beyond the buffer": func(b []byte) []byte { put32(b, 4, uint32(len(b))); return b },
		"name beyond the buffer":  func(b []byte) []byte { put32(b, layoutOutputHeader+fileLayoutEntrySize+16, 5000); return b },
		"odd name length":         func(b []byte) []byte { put32(b, layoutOutputHeader+fileLayoutEntrySize+16, 3); return b },
		"name offset beyond":      func(b []byte) []byte { put32(b, layoutOutputHeader+24, uint32(len(b))); return b },
		"stream offset beyond":    func(b []byte) []byte { put32(b, layoutOutputHeader+28, uint32(len(b))); return b },
	}
	for name, damage := range cases {
		buf := damage(append([]byte{}, good...))
		if err := parseLayout(buf, nil, func(fsEntry) {}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := parseLayout(append([]byte{}, good...), nil, func(fsEntry) {}); err != nil {
		t.Errorf("the good answer: %v", err)
	}
}

func TestParseUSNDirsReportsFoldersOnly(t *testing.T) {
	buf := append(v2(ref(100), ref(5), 1, 0, 0, attrDirectory, "docs"), v2(ref(101), ref(100), 2, 0, 0, 0x20, "a.txt")...)
	buf = append(buf, v2(ref(102), ref(100), 3, 0, 0, attrDirectory|attrReparsePoint, "link")...)
	buf = append(buf, v2(ref(103), ref(100), 4, 0, 0, attrDirectory, "文档")...)
	type dir struct{ frn, parent uint64 }
	var got []dir
	if err := parseUSNDirs(buf, func(frn, parent uint64, attrs uint32) { got = append(got, dir{frnKey(frn), frnKey(parent)}) }); err != nil {
		t.Fatal(err)
	}
	if want := []dir{{100, 5}, {103, 100}}; !reflect.DeepEqual(got, want) {
		t.Errorf("folders = %v, want %v", got, want)
	}
	if err := parseUSNDirs(buf[:30], func(uint64, uint64, uint32) {}); err == nil {
		t.Error("a cut record was accepted")
	}
}
