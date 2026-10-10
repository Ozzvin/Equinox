//go:build linux

package app

import (
	"os"

	"github.com/Ozzvin/equinox/internal/api"
)

// withAddedMounts adds the folders mounted into the container by the user (see addedMounts) to the given places.
func withAddedMounts(stateDir string, places []api.Place) []api.Place {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return places
	}
	defer f.Close()
	isDir := func(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
	return append(places, addedMounts(f, stateDir, places, isDir)...)
}
