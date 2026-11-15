//go:build !windows

package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ResolvePath returns the real location of an existing file or folder with
// every symbolic link resolved.
func ResolvePath(path string) (string, error) { return filepath.EvalSymlinks(path) }

// SyncDir flushes a folder's entries to disk, so files that were just created
// or renamed in it survive a power failure.
func SyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// FileAttributes is only meaningful on Windows.
func FileAttributes(fs.FileInfo) uint32 { return 0 }

// SetFileAttributes is a no-op outside Windows.
func SetFileAttributes(string, uint32) error { return nil }

// DiskSpace is only implemented on Windows.
func DiskSpace(string) (free, total uint64, err error) {
	return 0, 0, errors.ErrUnsupported
}

// IsLockViolation is always false outside Windows, where files are not locked mandatorily.
func IsLockViolation(error) bool { return false }
