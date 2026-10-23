//go:build !windows

package core

import "golang.org/x/sys/unix"

func freeSpace(dir string) (uint64, error) {
	_, free, err := DiskUsage(dir)
	return free, err
}

// DiskUsage reports the size of the file system holding dir and how much of it this program may still fill (the
// blocks kept for root are not counted as free). A network file system that does not know its size gives 0 total.
func DiskUsage(dir string) (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Blocks) * uint64(st.Bsize), uint64(st.Bavail) * uint64(st.Bsize), nil
}
