//go:build windows

package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"unsafe"
)

const (
	attrReadOnly         = 0x1
	attrHidden           = 0x2
	attrSystem           = 0x4
	attrArchive          = 0x20
	attrNormal           = 0x80
	attrNotContentIndexd = 0x2000

	errSharingViolation syscall.Errno = 32
	errLockViolation    syscall.Errno = 33

	// Only these attributes are ever re-applied to restored files; flags such as
	// compression, encryption, sparse or reparse point describe storage state.
	restorableAttrs = attrReadOnly | attrHidden | attrSystem | attrArchive | attrNotContentIndexd
)

// FileAttributes returns the Win32 attribute bits of a file, or 0 if unknown.
func FileAttributes(fi fs.FileInfo) uint32 {
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes
	}
	return 0
}

// SetFileAttributes re-applies the restorable subset of attrs to path.
func SetFileAttributes(path string, attrs uint32) error {
	attrs &= restorableAttrs
	if attrs == 0 {
		attrs = attrNormal
	}
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return err
	}
	return syscall.SetFileAttributes(p, attrs)
}

// DiskSpace returns the free and total bytes of the volume holding path.
func DiskSpace(path string) (free, total uint64, err error) {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return 0, 0, err
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	var avail, tot, totalFree uint64
	r, _, callErr := proc.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&tot)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx: %w", callErr)
	}
	return avail, tot, nil
}

var getFinalPathNameByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// ResolvePath returns the real location of an existing file or folder after
// following every symbolic link, junction and subst/network redirection. Go's
// filepath.EvalSymlinks does not resolve junctions, so it cannot be used for
// checks that must not be fooled by them.
func ResolvePath(path string) (string, error) {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return "", err
	}
	// Access 0 only queries the object; the backup-semantics flag lets folders be opened.
	h, err := syscall.CreateFile(p, 0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &fs.PathError{Op: "resolve", Path: path, Err: err}
	}
	defer syscall.CloseHandle(h)

	buf := make([]uint16, 512)
	for {
		n, _, callErr := getFinalPathNameByHandle.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		switch {
		case n == 0:
			return "", &fs.PathError{Op: "resolve", Path: path, Err: callErr}
		case int(n) >= len(buf):
			buf = make([]uint16, n+1)
		default:
			return trimExtendedPrefix(syscall.UTF16ToString(buf[:n])), nil
		}
	}
}

// trimExtendedPrefix turns \\?\C:\x into C:\x and \\?\UNC\host\share into \\host\share.
func trimExtendedPrefix(p string) string {
	switch {
	case strings.HasPrefix(p, `\\?\UNC\`):
		return `\\` + p[len(`\\?\UNC\`):]
	case strings.HasPrefix(p, `\\?\`):
		return p[len(`\\?\`):]
	}
	return p
}

// SyncDir flushes a folder's entries to disk, so files that were just created
// or renamed in it survive a power failure. Windows needs write access to the
// folder handle for this.
func SyncDir(path string) error {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return &fs.PathError{Op: "sync", Path: path, Err: err}
	}
	defer syscall.CloseHandle(h)
	if err := syscall.FlushFileBuffers(h); err != nil {
		return &fs.PathError{Op: "sync", Path: path, Err: err}
	}
	return nil
}

// IsLockViolation reports whether err means another process holds the file
// open without sharing it.
func IsLockViolation(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == errSharingViolation || errno == errLockViolation
	}
	return false
}

var setPriorityClass = syscall.NewLazyDLL("kernel32.dll").NewProc("SetPriorityClass")

// Priority classes of SetPriorityClass. The background mode is the one that also lowers the disk priority.
const (
	normalPriorityClass      = 0x00000020
	belowNormalPriorityClass = 0x00004000
	processModeBackground    = 0x00100000
)

// SetPriority changes the priority of the current process.
func SetPriority(p Priority) error {
	var class uintptr
	switch p {
	case PriorityNormal:
		class = normalPriorityClass
	case PriorityBelowNormal:
		class = belowNormalPriorityClass
	case PriorityBackground:
		class = processModeBackground
	default:
		return fmt.Errorf("platform: unknown priority %q", p)
	}
	self, err := syscall.GetCurrentProcess()
	if err != nil {
		return fmt.Errorf("GetCurrentProcess: %w", err)
	}
	if r, _, callErr := setPriorityClass.Call(uintptr(self), class); r == 0 {
		return fmt.Errorf("SetPriorityClass: %w", callErr)
	}
	return nil
}

func longPath(p string) string {
	if len(p) < 248 || strings.HasPrefix(p, `\\?\`) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}
