//go:build windows

package core

import (
	"os/exec"
	"strings"
)

// openPath selects path in Explorer (its parent folder opens with the item highlighted).
func openPath(path string) error {
	// Explorer wants the whole argument as one token: /select,"C:\some path".
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = explorerAttr(`/select,"` + strings.ReplaceAll(path, `"`, "") + `"`)
	return cmd.Start() // Explorer exits with status 1 even on success, so do not Wait
}
