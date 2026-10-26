//go:build windows

package updater

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// shellExecuteInfo is SHELLEXECUTEINFOW (field order and alignment match C).
type shellExecuteInfo struct {
	cbSize        uint32
	fMask         uint32
	hwnd          uintptr
	verb          *uint16
	file          *uint16
	params        *uint16
	dir           *uint16
	show          int32
	hInstApp      uintptr
	idList        uintptr
	class         *uint16
	keyClass      uintptr
	hotKey        uint32
	iconOrMonitor uintptr
	process       windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	errorCancelled        = 1223
)

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// ErrElevationDeclined means the user said No to the Windows UAC prompt.
var ErrElevationDeclined = errors.New("administrator approval was declined")

// InstallElevated re-runs this (installed, trusted) exe as administrator to
// download, verify and install version tag into its own folder. The
// elevated copy does the whole check itself with its embedded release key,
// so nothing prepared by this unprivileged process is trusted.
func InstallElevated(tag string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(ElevatedUpdateFlag + " " + syscall.EscapeArg(tag))
	info := shellExecuteInfo{
		fMask:  seeMaskNoCloseProcess | seeMaskNoAsync,
		verb:   verb,
		file:   file,
		params: params,
		show:   windows.SW_HIDE,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errno, ok := e.(syscall.Errno); ok && errno == errorCancelled {
			return ErrElevationDeclined
		}
		return fmt.Errorf("could not request administrator approval: %v", e)
	}
	if info.process == 0 {
		return errors.New("installer did not start")
	}
	defer windows.CloseHandle(info.process)
	if _, err := windows.WaitForSingleObject(info.process, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("the administrator update step failed (code %d); see the debug log", code)
	}
	return nil
}

// isPermission reports an access-denied error from Windows.
func isPermission(err error) bool {
	return errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "access is denied")
}
