package weblog

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"strings"
)

// Opcodes of pickle protocol 4 that a pickled str needs.
const (
	opProto           = 0x80
	opFrame           = 0x95
	opShortBinUnicode = 0x8c
	opBinUnicode      = 'X'
	opMemoize         = 0x94
	opStop            = '.'
)

// Encode returns line the way the other applications store the elements of the web log:
// zlib.compress(pickle.dumps(line, protocol=4)). A reader that does zlib.decompress and pickle.loads gets the text
// back. The pickle is the one Python writes for a str, byte for byte: protocol 4, one frame, SHORT_BINUNICODE for up
// to 255 bytes of UTF-8 and BINUNICODE above that, then MEMOIZE and STOP. Text that is not valid UTF-8 has its bad
// bytes replaced, as Python would refuse to pickle it.
func Encode(line string) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(pickleString(line))
	_ = zw.Close()
	return buf.Bytes()
}

// pickleString is pickle.dumps(s, protocol=4). Lines are cut far below the 4 GiB that BINUNICODE can hold, so the
// 8-byte length (BINUNICODE8) is not needed.
func pickleString(s string) []byte {
	s = strings.ToValidUTF8(s, "\uFFFD")
	body := make([]byte, 0, len(s)+8)
	if len(s) <= 0xff {
		body = append(body, opShortBinUnicode, byte(len(s)))
	} else {
		body = append(body, opBinUnicode)
		body = binary.LittleEndian.AppendUint32(body, uint32(len(s)))
	}
	body = append(body, s...)
	body = append(body, opMemoize, opStop)

	out := make([]byte, 0, len(body)+11)
	out = append(out, opProto, 4, opFrame)
	out = binary.LittleEndian.AppendUint64(out, uint64(len(body)))
	return append(out, body...)
}
