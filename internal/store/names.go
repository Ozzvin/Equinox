package store

import (
	"runtime"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// RootName is the name of the torrent's own folder, or of its one file, on the disk: its name made fit for the system
// (see safeName). It is "" for a torrent without a folder of its own (metainfo.NoName), whose files lie right in the
// save folder.
func RootName(info *metainfo.Info) string {
	if n := info.BestName(); n != metainfo.NoName {
		return safeName(n)
	}
	return ""
}

// safeName makes one name from a torrent (a folder or a file) fit for the disk. On Windows a colon writes into a
// hidden stream of the file before it, "CON" or "NUL" cannot be made at all, and "a." is the same file as "a", so
// two files of a torrent would be written over each other; such names are mended the way qBittorrent does. Elsewhere
// a name stays as it is.
func safeName(name string) string {
	if runtime.GOOS != "windows" {
		return name
	}
	return windowsName(name)
}

func windowsName(name string) string {
	b := []rune(name)
	for i, r := range b {
		if r < 32 || strings.ContainsRune(`<>:"|?*/\`, r) {
			b[i] = '_'
		}
	}
	name = strings.TrimRight(string(b), ". ")
	if name == "" {
		return "_"
	}
	stem, ext, _ := strings.Cut(name, ".")
	switch u := strings.ToUpper(strings.TrimRight(stem, " ")); u {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		if ext != "" {
			return stem + "_." + ext
		}
		return stem + "_"
	}
	return name
}
