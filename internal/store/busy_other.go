//go:build !windows

package store

// busyFile: elsewhere a program cannot keep another from opening or writing a file (locks there are advisory), so
// no failure is a passing one.
func busyFile(error) bool { return false }
