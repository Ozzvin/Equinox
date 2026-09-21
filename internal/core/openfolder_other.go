//go:build !windows

package core

import "os/exec"

// openPath opens the folder with the desktop's default file manager.
func openPath(path string) error {
	return exec.Command("xdg-open", path).Start()
}
