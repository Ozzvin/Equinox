//go:build windows

package app

import (
	"os"

	"golang.org/x/sys/windows"
)

// setRuntimeStderr points the process's standard error handle at f. The Go runtime writes the trace of a
// panic straight to that handle, not through os.Stderr, and a GUI program (-H windowsgui) has no valid
// one: without this, a crash leaves nothing in the log. It has to be the file itself and not the pipe that
// feeds it, because a panicking process stops the other goroutines and exits at once, so nothing would be
// left to copy the trace out of a pipe. The file is opened for appending, so these writes land at its end.
func setRuntimeStderr(f *os.File) {
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
}
