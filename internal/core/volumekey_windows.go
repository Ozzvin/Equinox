//go:build windows

package core

import "path/filepath"

// volumeKey identifies the drive an (already absolute) path is on, "C:" for instance — enough to tell whether
// two torrents' save folders would compete for the same disk's free space (see otherNeededBytes).
func volumeKey(absDir string) string {
	return filepath.VolumeName(absDir)
}
