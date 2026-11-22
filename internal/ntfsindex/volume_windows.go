package ntfsindex

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The control codes of the file system (CTL_CODE(FILE_DEVICE_FILE_SYSTEM, function, method, FILE_ANY_ACCESS)).
const (
	fsctlQueryUSNJournal = 0x000900F4 // function 61, buffered
	fsctlReadUSNJournal  = 0x000900BB // function 46, neither
	fsctlEnumUSNData     = 0x000900B3 // function 44, neither
	fsctlQueryFileLayout = 0x00090277 // function 157, neither
)

// The flags of FSCTL_QUERY_FILE_LAYOUT (QUERY_FILE_LAYOUT_*).
const (
	layoutRestart           = 0x1
	layoutIncludeNames      = 0x2
	layoutIncludeStreams    = 0x4
	layoutIncludeExtraInfo  = 0x10
	layoutStreamsNoClusters = 0x20 // also the streams that are in the file record: every small file has only those
)

const (
	layoutOutputBytes  = 1 << 20
	enumOutputBytes    = 1 << 20
	journalOutputBytes = 1 << 16
	// readJournalRequestLength is the length of READ_USN_JOURNAL_DATA_V0, enumRequestLength that of MFT_ENUM_DATA_V1.
	readJournalRequestLength = 40
	enumRequestLength        = 32
	// maxUSN is the highest USN a record can have: the enumeration of a volume reports the files whose newest record
	// is between two USNs, and every file is wanted, also one that changed a moment ago.
	maxUSN = 0x7FFFFFFFFFFFFFFF
)

// The errors of the change journal that mean it cannot be followed.
const (
	errJournalDeleteInProgress syscall.Errno = 1178
	errJournalNotActive        syscall.Errno = 1179
	errJournalEntryDeleted     syscall.Errno = 1181
)

// alignedBytes returns n bytes that start at an address divisible by 8. The file system controls that take their buffers
// as they are (the USN ones) refuse a buffer that is not aligned, and a byte slice that the compiler keeps on the
// stack is not.
func alignedBytes(n int) []byte {
	words := make([]uint64, (n+7)/8)
	return unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), n)
}

// winVolume is an NTFS volume opened for reading its file records and its change journal.
type winVolume struct {
	h     windows.Handle
	drive string
}

// openVolume opens the volume of a drive letter ("D:"). It needs administrator rights.
func openVolume(drive string) (volume, error) {
	root, err := windows.UTF16PtrFromString(drive + `\`)
	if err != nil {
		return nil, err
	}
	var fsName [32]uint16
	if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnsupported, drive, err)
	}
	if name := windows.UTF16ToString(fsName[:]); name != "NTFS" {
		return nil, fmt.Errorf("%w: %s is %s, not NTFS", ErrUnsupported, drive, name)
	}
	device, err := windows.UTF16PtrFromString(`\\.\` + drive)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(device, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, ErrAccessDenied
		}
		return nil, fmt.Errorf("ntfsindex: open %s: %w", drive, err)
	}
	return &winVolume{h: h, drive: drive}, nil
}

func (v *winVolume) ioctl(code uint32, in, out []byte) (uint32, error) {
	var inPtr *byte
	if len(in) > 0 {
		inPtr = &in[0]
	}
	var n uint32
	err := windows.DeviceIoControl(v.h, code, inPtr, uint32(len(in)), &out[0], uint32(len(out)), &n, nil)
	return n, err
}

func (v *winVolume) Journal() (journalState, error) {
	out := alignedBytes(64)
	n, err := v.ioctl(fsctlQueryUSNJournal, nil, out)
	if err != nil {
		if errors.Is(err, errJournalNotActive) || errors.Is(err, errJournalDeleteInProgress) {
			return journalState{}, ErrJournalOff
		}
		return journalState{}, fmt.Errorf("ntfsindex: query the change journal of %s: %w", v.drive, err)
	}
	if n < 24 {
		return journalState{}, errBadRecord
	}
	return journalState{ID: le64(out), First: int64(le64(out[8:])), Next: int64(le64(out[16:]))}, nil
}

func (v *winVolume) ReadJournal(id uint64, from int64, fn func(usnRecord)) (int64, error) {
	in := alignedBytes(readJournalRequestLength)
	binary.LittleEndian.PutUint64(in[0:], uint64(from))
	binary.LittleEndian.PutUint32(in[8:], 0xFFFFFFFF) // every reason
	// ReturnOnlyOnClose, Timeout and BytesToWaitFor stay 0: the call answers at once, with what there is.
	binary.LittleEndian.PutUint64(in[32:], id)
	out := alignedBytes(journalOutputBytes)
	n, err := v.ioctl(fsctlReadUSNJournal, in, out)
	if err != nil {
		if errors.Is(err, errJournalEntryDeleted) || errors.Is(err, errJournalNotActive) || errors.Is(err, errJournalDeleteInProgress) ||
			errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return from, ErrJournalLost
		}
		return from, fmt.Errorf("ntfsindex: read the change journal of %s: %w", v.drive, err)
	}
	if n < 8 {
		return from, errBadRecord
	}
	next := int64(le64(out))
	if err := parseUSN(out[8:n], fn); err != nil {
		return from, err
	}
	return next, nil
}

func (v *winVolume) Dirs(fn func(frn, parent uint64, attrs uint32)) error {
	in := alignedBytes(enumRequestLength)
	binary.LittleEndian.PutUint64(in[16:], maxUSN) // HighUsn; the start and LowUsn are 0
	binary.LittleEndian.PutUint16(in[24:], 2)      // MinMajorVersion
	binary.LittleEndian.PutUint16(in[26:], 2)      // MaxMajorVersion
	out := alignedBytes(enumOutputBytes)
	for {
		n, err := v.ioctl(fsctlEnumUSNData, in, out)
		if err != nil {
			if errors.Is(err, windows.ERROR_HANDLE_EOF) {
				return nil
			}
			return fmt.Errorf("ntfsindex: list the folders of %s: %w", v.drive, err)
		}
		if n < 8 {
			return errBadRecord
		}
		binary.LittleEndian.PutUint64(in[0:], le64(out)) // where the next call goes on
		if err := parseUSNDirs(out[8:n], fn); err != nil {
			return err
		}
	}
}

func (v *winVolume) Entries(want func(frn, parent uint64, attrs uint32) bool, fn func(fsEntry)) error {
	flags := uint32(layoutIncludeNames | layoutIncludeStreams | layoutIncludeExtraInfo | layoutStreamsNoClusters)
	in := alignedBytes(32)
	out := alignedBytes(layoutOutputBytes)
	for first := true; ; first = false {
		f := flags
		if first {
			f |= layoutRestart
		}
		binary.LittleEndian.PutUint32(in[4:], f) // the filter type stays 0: every file
		n, err := v.ioctl(fsctlQueryFileLayout, in, out)
		more := errors.Is(err, windows.ERROR_MORE_DATA)
		if err != nil && !more {
			if errors.Is(err, windows.ERROR_HANDLE_EOF) {
				return nil
			}
			return fmt.Errorf("ntfsindex: list the files of %s: %w", v.drive, err)
		}
		if n < layoutOutputHeader {
			return nil
		}
		if err := parseLayout(out[:n], want, fn); err != nil {
			return err
		}
		if !more && le32(out) == 0 {
			return nil
		}
	}
}

func (v *winVolume) FileID(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 0, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return 0, fmt.Errorf("ntfsindex: %s is not a folder", path)
	}
	return uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), nil
}

func (v *winVolume) Stat(path string) (int64, int64, uint32, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, 0, 0, err
	}
	var attrs uint32
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		attrs = d.FileAttributes
	}
	return fi.Size(), filetime(fi.ModTime()), attrs, nil
}

func (v *winVolume) Close() error { return windows.CloseHandle(v.h) }
