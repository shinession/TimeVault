package ntfsindex

import (
	"encoding/binary"
	"errors"
	"unicode/utf16"
)

// usnRecordV2Header is the length of a USN_RECORD_V2 without its file name.
const usnRecordV2Header = 60

var errBadRecord = errors.New("ntfsindex: malformed journal record")

// parseUSN calls fn for every record in buf, which holds the records as FSCTL_READ_USN_JOURNAL and FSCTL_ENUM_USN_DATA
// return them, after the 8 bytes that start the answer. Records of another major version than 2 are skipped: the
// index asks for the old record layout, which has 64-bit file references and is what NTFS writes.
func parseUSN(buf []byte, fn func(usnRecord)) error {
	for len(buf) > 0 {
		if len(buf) < 8 {
			return errBadRecord
		}
		length := int(binary.LittleEndian.Uint32(buf))
		if length < 8 || length > len(buf) {
			return errBadRecord
		}
		if binary.LittleEndian.Uint16(buf[4:]) == 2 {
			rec, err := parseV2(buf[:length])
			if err != nil {
				return err
			}
			fn(rec)
		}
		buf = buf[length:]
	}
	return nil
}

func parseV2(b []byte) (usnRecord, error) {
	if len(b) < usnRecordV2Header {
		return usnRecord{}, errBadRecord
	}
	nameLen := int(binary.LittleEndian.Uint16(b[56:]))
	nameOff := int(binary.LittleEndian.Uint16(b[58:]))
	if nameLen%2 != 0 || nameOff < usnRecordV2Header || nameOff+nameLen > len(b) {
		return usnRecord{}, errBadRecord
	}
	return usnRecord{
		FRN:    binary.LittleEndian.Uint64(b[8:]),
		Parent: binary.LittleEndian.Uint64(b[16:]),
		USN:    int64(binary.LittleEndian.Uint64(b[24:])),
		Time:   int64(binary.LittleEndian.Uint64(b[32:])),
		Reason: binary.LittleEndian.Uint32(b[40:]),
		Attrs:  binary.LittleEndian.Uint32(b[52:]),
		Name:   decodeName(b[nameOff : nameOff+nameLen]),
	}, nil
}

// parseUSNDirs reads the records of an enumeration of a volume the way parseUSN does, but only reports the folders that
// are not reparse points, with their file number, the number of the folder they are in and their attributes, and does
// not read any name: that is what a first look at a volume with millions of files can afford.
func parseUSNDirs(buf []byte, fn func(frn, parent uint64, attrs uint32)) error {
	for len(buf) > 0 {
		if len(buf) < 8 {
			return errBadRecord
		}
		length := int(binary.LittleEndian.Uint32(buf))
		if length < 8 || length > len(buf) {
			return errBadRecord
		}
		if binary.LittleEndian.Uint16(buf[4:]) == 2 {
			if length < usnRecordV2Header {
				return errBadRecord
			}
			attrs := binary.LittleEndian.Uint32(buf[52:])
			if attrs&attrDirectory != 0 && attrs&attrReparsePoint == 0 {
				fn(binary.LittleEndian.Uint64(buf[8:]), binary.LittleEndian.Uint64(buf[16:]), attrs)
			}
		}
		buf = buf[length:]
	}
	return nil
}

// decodeName reads a UTF-16 little-endian file name.
func decodeName(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}
