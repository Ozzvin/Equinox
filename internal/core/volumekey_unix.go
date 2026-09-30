//go:build !windows

package core

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// volumeKey identifies the filesystem an (already absolute) path is on — enough to tell whether two torrents'
// save folders would compete for the same disk's free space (see otherNeededBytes). There is no drive letter to
// go by outside Windows, so the device id of the folder itself stands in for it; a path that cannot be statted
// (its folder does not exist yet) gets a key of its own, built from the path, so that two such failures are never
// mistaken for the same disk just because both came back empty.
func volumeKey(absDir string) string {
	var st unix.Stat_t
	if unix.Stat(absDir, &st) != nil {
		return "?" + absDir
	}
	return fmt.Sprintf("%d", int64(st.Dev))
}
