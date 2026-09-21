package api

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/windows"
)

const computerName = "Этот компьютер"

// drives lists the drives with their volume labels, like Explorer: "System SSD (C:)".
func drives() []fsPlace {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []fsPlace
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		root := letter + `:\`
		rp, _ := windows.UTF16PtrFromString(root)
		kind := windows.GetDriveType(rp)
		if kind == windows.DRIVE_NO_ROOT_DIR {
			continue
		}
		label := ""
		if kind != windows.DRIVE_REMOTE { // an unreachable network drive would stall the query
			buf := make([]uint16, 261)
			if err := windows.GetVolumeInformation(rp, &buf[0], uint32(len(buf)), nil, nil, nil, nil, 0); err != nil {
				if kind == windows.DRIVE_CDROM || kind == windows.DRIVE_REMOVABLE {
					continue // no disk in it
				}
			} else {
				label = windows.UTF16ToString(buf)
			}
		}
		name := letter + ":"
		if label != "" {
			name = fmt.Sprintf("%s (%s:)", label, letter)
		}
		out = append(out, fsPlace{Name: name, Path: root, Kind: "drive"})
	}
	return out
}

// hiddenEntry tells whether Explorer would hide the entry (hidden or system attribute).
func hiddenEntry(full, _ string) bool {
	p, err := syscall.UTF16PtrFromString(full)
	if err != nil {
		return false
	}
	attrs, err := syscall.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
}
