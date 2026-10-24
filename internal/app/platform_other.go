//go:build !windows

package app

import "errors"

// StartupFlag is passed when the OS launches the app at sign-in.
const StartupFlag = "--startup"

func setRunAtStartup(bool) error { return errors.New("launch at startup is only available on Windows") }
func runAtStartup() bool         { return false }
func startupSupported() bool     { return false }
func downloadsDir() string       { return defaultDownloads() }
