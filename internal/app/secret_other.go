//go:build !windows

package app

// Off Windows (development builds) secrets are stored as-is in a file only
// the user can read.
func protect(data []byte) ([]byte, error)   { return append([]byte(nil), data...), nil }
func unprotect(data []byte) ([]byte, error) { return append([]byte(nil), data...), nil }
