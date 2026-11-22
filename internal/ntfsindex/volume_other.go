//go:build !windows

package ntfsindex

import "fmt"

// openVolume cannot open anything here: NTFS volumes are read through the Windows file system controls.
func openVolume(drive string) (volume, error) {
	return nil, fmt.Errorf("%w: %s (the NTFS index only runs on Windows)", ErrUnsupported, drive)
}
