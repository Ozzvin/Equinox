//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// lockInstance takes the lock that keeps a second server off the same state directory; the lock goes with the process.
func lockInstance(stateDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(stateDir, "server.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errRunning
		}
		return nil, err
	}
	return f, nil
}
