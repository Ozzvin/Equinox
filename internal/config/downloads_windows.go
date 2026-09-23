//go:build windows

package config

import "golang.org/x/sys/windows"

// SystemDownloads is the user's own Downloads folder (which may have been moved to another drive), or
// "" when Windows cannot say.
func SystemDownloads() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return p
}
