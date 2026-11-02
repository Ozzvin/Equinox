//go:build !windows

package core

import (
	"errors"
	"syscall"
)

func storageIOError(err error) bool {
	for _, code := range []error{syscall.EIO, syscall.ENODEV, syscall.ENOTCONN, syscall.ESTALE, syscall.ETIMEDOUT} {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
