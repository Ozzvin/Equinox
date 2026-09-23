//go:build !windows

package core

import "os"

// Other filesystems already leave the unwritten part of an extended file as a hole.
func markSparse(*os.File) {}
