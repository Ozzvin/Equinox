package core

import (
	"errors"
	"golang.org/x/sys/windows"
)

func storageIOError(err error) bool {
	for _, code := range []error{windows.ERROR_NOT_READY, windows.ERROR_PATH_NOT_FOUND,
		windows.ERROR_NETNAME_DELETED, windows.ERROR_BAD_NETPATH, windows.ERROR_DEVICE_NOT_CONNECTED,
		windows.ERROR_IO_DEVICE, windows.ERROR_SEM_TIMEOUT, windows.ERROR_UNEXP_NET_ERR} {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
