//go:build windows

package core

import (
	"fmt"
	"os/exec"
	"strings"
)

// openPath selects path in Explorer (its parent folder opens with the item highlighted).
func openPath(path string) error {
	// Explorer wants the whole argument as one token: /select,"C:\some path". A quote cannot be escaped
	// inside it, and no existing Windows path holds one, so such a path is refused instead of being
	// changed into another one that would open the wrong folder.
	if strings.Contains(path, `"`) {
		return fmt.Errorf("cannot show %q in Explorer: the path has a quote in it", path)
	}
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = explorerAttr(`/select,"` + path + `"`)
	return cmd.Start() // Explorer exits with status 1 even on success, so do not Wait
}
