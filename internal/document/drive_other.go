//go:build !windows

package document

func isRemovable(string) bool { return false }
