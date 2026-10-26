//go:build !windows

package updater

import (
	"errors"
	"os"
)

// ErrElevationDeclined means the user declined the administrator prompt.
var ErrElevationDeclined = errors.New("administrator approval was declined")

// InstallElevated is only implemented on Windows.
func InstallElevated(string) error {
	return errors.New("this install location needs administrator rights; reinstall in a folder you can write to")
}

func isPermission(err error) bool { return errors.Is(err, os.ErrPermission) }
