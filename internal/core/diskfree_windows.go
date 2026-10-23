//go:build windows

package core

import "golang.org/x/sys/windows"

func freeSpace(dir string) (uint64, error) {
	_, free, err := DiskUsage(dir)
	return free, err
}

// DiskUsage reports the size of the drive holding dir and how much of it this user may still fill (a quota
// counts), as Explorer shows the drive.
func DiskUsage(dir string) (total, free uint64, err error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, 0, err
	}
	return total, free, nil
}
