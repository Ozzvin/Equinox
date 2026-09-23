//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

// SystemDownloads is ~/Downloads when it exists, or "" otherwise.
func SystemDownloads() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	d := filepath.Join(home, "Downloads")
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		return ""
	}
	return d
}
