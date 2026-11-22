package ntfsindex

import (
	"encoding/binary"
	"errors"
)

// The answer of FSCTL_QUERY_FILE_LAYOUT is a QUERY_FILE_LAYOUT_OUTPUT followed by FILE_LAYOUT_ENTRY structures, each with
// a chain of FILE_LAYOUT_NAME_ENTRY (one for every name of the file), a chain of STREAM_LAYOUT_ENTRY (one for every
// attribute with data: the data streams, but also the standard information and the file names) and a
// FILE_LAYOUT_INFO_ENTRY (the times). Every offset is relative to the start of the structure that holds it.
// parseLayout reads that; it has no system calls, so it is tested with buffers (the layout is what a real volume
// answers, see volume_windows_test.go).
const (
	layoutOutputHeader  = 16
	fileLayoutEntrySize = 40
	nameEntryHeader     = 24
	streamEntryHeader   = 48
	// nameFlagDOS marks a short (8.3) name; nameFlagPrimary the name the file is known by.
	nameFlagPrimary = 0x1
	nameFlagDOS     = 0x2
	// attrTypeData is the type of the attribute of a data stream. The stream without a name has an empty identifier.
	attrTypeData = 0x80
	// infoLastWrite is where the time of the last write is in a FILE_LAYOUT_INFO_ENTRY (after the times of creation
	// and of the last access).
	infoLastWrite = 16
)

var errBadLayout = errors.New("ntfsindex: malformed file layout answer")

func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

// parseLayout calls fn for every name of every file in buf, except the short names and the names that want turns down.
// The entry carries the size of the unnamed data stream and the time of the last write, which is read from the buffer
// only for a file that has a name that is wanted; the answer has to have been asked for the names, the streams, the
// streams that have no clusters (the files whose data is in their file record) and the extra information. The name is
// given as the bytes of the buffer.
func parseLayout(buf []byte, want func(frn, parent uint64, attrs uint32) bool, fn func(fsEntry)) error {
	if len(buf) < layoutOutputHeader {
		return errBadLayout
	}
	count := int(le32(buf))
	off := int(le32(buf[4:]))
	for i := 0; i < count; i++ {
		if off < layoutOutputHeader || off+fileLayoutEntrySize > len(buf) {
			return errBadLayout
		}
		e := buf[off:]
		next := int(le32(e[4:]))
		attrs := le32(e[12:])
		frn := le64(e[16:])
		firstName := int(le32(e[24:]))
		firstStream := int(le32(e[28:]))
		extra, extraLen := int(le32(e[32:])), int(le32(e[36:]))

		haveData := false
		var size, mtime int64
		for name := firstName; name != 0; {
			at := off + name
			if at+nameEntryHeader > len(buf) {
				return errBadLayout
			}
			nextName := int(le32(buf[at:]))
			flags := le32(buf[at+4:])
			parent := le64(buf[at+8:])
			length := int(le32(buf[at+16:]))
			if length%2 != 0 || at+nameEntryHeader+length > len(buf) {
				return errBadLayout
			}
			if (flags&nameFlagDOS == 0 || flags&nameFlagPrimary != 0) && (want == nil || want(frn, parent, attrs)) {
				if !haveData {
					haveData = true
					if attrs&attrDirectory == 0 {
						s, err := unnamedStreamSize(buf, off, firstStream)
						if err != nil {
							return err
						}
						size = s
					}
					if extra != 0 && extraLen >= infoLastWrite+8 && off+extra+infoLastWrite+8 <= len(buf) {
						mtime = int64(le64(buf[off+extra+infoLastWrite:]))
					}
				}
				fn(fsEntry{FRN: frn, Parent: parent, Raw: buf[at+nameEntryHeader : at+nameEntryHeader+length], Attrs: attrs, Size: size, MTime: mtime})
			}
			if nextName == 0 {
				break
			}
			name += nextName
		}
		if next == 0 {
			break
		}
		off += next
	}
	return nil
}

// unnamedStreamSize finds the length of the data stream without a name among the streams that start at first (relative
// to the file entry at off). A file that has none, as a file without data, has the size 0.
func unnamedStreamSize(buf []byte, off, first int) (int64, error) {
	for s := first; s != 0; {
		at := off + s
		if at+streamEntryHeader > len(buf) {
			return 0, errBadLayout
		}
		next := int(le32(buf[at+4:]))
		typ := le32(buf[at+36:])
		idLen := int(le32(buf[at+44:]))
		if idLen%2 != 0 || at+streamEntryHeader+idLen > len(buf) {
			return 0, errBadLayout
		}
		if typ == attrTypeData && idLen == 0 {
			return int64(le64(buf[at+24:])), nil
		}
		if next == 0 {
			break
		}
		s += next
	}
	return 0, nil
}
