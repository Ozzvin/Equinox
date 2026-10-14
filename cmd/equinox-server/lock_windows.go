//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// lockInstance takes the lock that keeps a second server off the same state directory; the lock goes with the process.
func lockInstance(stateDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(stateDir, "server.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errRunning
		}
		return nil, err
	}
	return f, nil
}
