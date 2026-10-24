//go:build windows

package document

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// isRemovable reports whether path is on a removable drive (USB stick,
// SD card, machine stick emulator).
func isRemovable(path string) bool {
	vol := filepath.VolumeName(path)
	if len(vol) != 2 || vol[1] != ':' { // UNC paths etc.
		return false
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOVABLE
}
