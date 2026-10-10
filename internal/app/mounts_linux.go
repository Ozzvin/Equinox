//go:build linux

package app

import (
	"os"

	"github.com/Ozzvin/equinox/internal/api"
)

// addedMountsHere returns the folders mounted into this container by the user (see addedMounts).
func addedMountsHere(stateDir string, places []api.Place) []api.Place {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	isDir := func(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
	return addedMounts(f, stateDir, places, isDir)
}
