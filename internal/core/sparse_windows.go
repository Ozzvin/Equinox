//go:build windows

package core

import (
	"os"

	"golang.org/x/sys/windows"
)

// markSparse makes f a sparse file, so that extending it reserves nothing and writing far past what is
// written so far costs nothing. A plain NTFS file that was only extended (SetEndOfFile) has to be
// filled with zeros from its last written byte up to the first write that lands beyond it: measured
// on a hard disk, one 16 KB piece written 9 GB into a preallocated file stalled for two minutes, and a
// torrent's first piece can be anywhere in the file. A failure is not fatal: the file just behaves as
// it did before.
func markSparse(f *os.File) {
	var n uint32
	_ = windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &n, nil)
}
