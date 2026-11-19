package weblog

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

// The expected pickles are what pickle.dumps(text, protocol=4) printed in Python.
func TestEncodeWritesWhatPythonPickles(t *testing.T) {
	cases := []struct{ name, text, pickle string }{
		{"empty", "", "80049504000000000000008c00942e"},
		{"short", "abc", "80049507000000000000008c03616263942e"},
		{
			"a line of the web log", "127.0.0.1 | 2026-11-18 13:58:06.446524 | home_page: access | 200 | info | sgmeslt595",
			"80049558000000000000008c543132372e302e302e31207c20323032362d31312d31382031333a35383a30362e343436353234207c20686f6d655f706167653a20616363657373207c20323030207c20696e666f207c2073676d65736c74353935942e",
		},
		{
			"Chinese", "10.0.0.1 | 2026-11-18 14:00:00.000001 | TimeVault: \u6587\u4ef6\u6d4f\u89c8 user=E1 | 200 | info | host",
			"8004955f000000000000008c5b31302e302e302e31207c20323032362d31312d31382031343a30303a30302e303030303031207c2054696d655661756c743a20e69687e4bbb6e6b58fe8a78820757365723d4531207c20323030207c20696e666f207c20686f7374942e",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := hex.DecodeString(c.pickle)
			if err != nil {
				t.Fatal(err)
			}
			if got := unzip(t, Encode(c.text)); !bytes.Equal(got, want) {
				t.Errorf("pickle = %x, want %x", got, want)
			}
		})
	}
}

// 255 bytes is the longest text SHORT_BINUNICODE holds; from 256 bytes Python writes BINUNICODE.
func TestEncodeSwitchesToTheLongOpcodeAtTheSameLengthAsPython(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		total  int    // length of the pickle
		header string // its first bytes
	}{
		{"255 bytes", strings.Repeat("a", 255), 270, "80049503010000000000008cff"},
		{"256 bytes", strings.Repeat("a", 256), 274, "8004950701000000000000" + "5800010000"},
		{"300 bytes of Chinese", strings.Repeat("\u6587", 100), 318, "800495330100000000000058" + "2c010000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := unzip(t, Encode(c.text))
			if len(got) != c.total {
				t.Errorf("pickle is %d bytes, want %d", len(got), c.total)
			}
			if h := hex.EncodeToString(got[:len(c.header)/2]); h != c.header {
				t.Errorf("pickle starts %s, want %s", h, c.header)
			}
			if tail := got[len(got)-2:]; tail[0] != 0x94 || tail[1] != '.' {
				t.Errorf("pickle ends %x, want MEMOIZE and STOP", tail)
			}
			if back := unpickle(t, Encode(c.text)); back != c.text {
				t.Errorf("round trip gave %d bytes, want %d", len(back), len(c.text))
			}
		})
	}
}

func TestEncodeIsAZlibStreamThatPythonReads(t *testing.T) {
	// zlib.compress() starts every stream with 78 9c, and so does this.
	if b := Encode("abc"); len(b) < 2 || b[0] != 0x78 || b[1] != 0x9c {
		t.Errorf("stream starts %x, want 789c", b[:2])
	}
}

func TestEncodeReplacesBytesThatAreNotUTF8(t *testing.T) {
	if got := unpickle(t, Encode("a\xffb")); got != "a\uFFFDb" {
		t.Errorf("got %q", got)
	}
}

func unzip(t testing.TB, b []byte) []byte {
	t.Helper()
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a zlib stream: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("zlib: %v", err)
	}
	return out
}

// unpickle reads back the one kind of pickle that Encode writes, so that a test sees what a reader would.
func unpickle(t testing.TB, raw []byte) string {
	t.Helper()
	p := unzip(t, raw)
	if len(p) < 12 || p[0] != 0x80 || p[1] != 4 || p[2] != 0x95 {
		t.Fatalf("not a framed protocol 4 pickle: %x", p)
	}
	frame := p[11:]
	if got := binary.LittleEndian.Uint64(p[3:11]); got != uint64(len(frame)) {
		t.Fatalf("frame says %d bytes, there are %d", got, len(frame))
	}
	var n, at int
	switch frame[0] {
	case 0x8c:
		n, at = int(frame[1]), 2
	case 'X':
		n, at = int(binary.LittleEndian.Uint32(frame[1:5])), 5
	default:
		t.Fatalf("unexpected opcode %x", frame[0])
	}
	if at+n+2 != len(frame) || frame[at+n] != 0x94 || frame[at+n+1] != '.' {
		t.Fatalf("pickle is not one string followed by MEMOIZE and STOP: %x", frame)
	}
	return string(frame[at : at+n])
}
