package store

import (
	"errors"

	"golang.org/x/sys/windows"
)

// busyFile tells a file that another program holds for a moment (an antivirus scanning a fresh .dll or .exe, the
// search indexer) from a failure that waiting does not mend: Windows refuses to open a file another process has
// open without sharing it, or to touch a range of it that is locked.
func busyFile(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
