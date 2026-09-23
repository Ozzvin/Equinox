//go:build windows

package core

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// A far-off first write into a preallocated NTFS file zero-fills everything before it, which on a hard
// disk stalled a download's start for minutes; a sparse file avoids that.
func TestAllocateFileIsSparse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.bin")
	if err := allocateFile(p, 1<<30, false); err != nil {
		t.Fatal(err)
	}
	u, _ := windows.UTF16PtrFromString(p)
	attrs, err := windows.GetFileAttributes(u)
	if err != nil {
		t.Fatal(err)
	}
	if attrs&windows.FILE_ATTRIBUTE_SPARSE_FILE == 0 {
		t.Fatal("the preallocated file must be sparse")
	}

	z := filepath.Join(t.TempDir(), "zero.bin")
	if err := allocateFile(z, 1<<20, true); err != nil {
		t.Fatal(err)
	}
	u, _ = windows.UTF16PtrFromString(z)
	attrs, _ = windows.GetFileAttributes(u)
	if attrs&windows.FILE_ATTRIBUTE_SPARSE_FILE != 0 {
		t.Fatal("an explicit zero-fill asks for real space: the file must not be sparse")
	}
}
