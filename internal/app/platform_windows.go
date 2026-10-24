//go:build windows

package app

import (
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "TSG Embroidery Pro"
	// StartupFlag is passed when Windows launches the app at sign-in.
	StartupFlag = "--startup"
)

// setRunAtStartup adds or removes the app from the user's startup list
// (HKCU\...\Run, no admin rights needed).
func setRunAtStartup(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, `"`+exe+`" `+StartupFlag)
}

// runAtStartup reports whether the startup entry exists.
func runAtStartup() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func startupSupported() bool { return true }

// downloadsDir is the user's Downloads folder (wherever they moved it).
func downloadsDir() string {
	if p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0); err == nil && p != "" {
		return p
	}
	return defaultDownloads()
}
