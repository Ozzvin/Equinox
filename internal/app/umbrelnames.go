package app

import (
	"bufio"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Ozzvin/equinox/internal/api"
)

// umbrelSettingsFile is the copy of the app's settings.yml that the Umbrel package's pre-start hook puts into the
// state folder before every start. Inside the container nothing tells where a mounted folder really is; Umbrel's own
// settings do, by the paths of its Files ("/External/RAID/Torrents" mounted at /storage). With them the folders are
// shown by those paths, which is where the user finds the downloaded files (asked for on Umbrel, 2026-10-11).
const umbrelSettingsFile = "umbrel-settings.yml"

// umbrelNames reads from Umbrel's settings.yml the folders the user chose: where each folder added under "Add your own
// folder" comes from (mount point → path in Files), and where the Downloads folder was pointed, if anywhere else than
// its default ("" then). Only these few keys are read, line by line; anything else in the file is passed over.
func umbrelNames(r io.Reader) (downloads string, mounts map[string]string) {
	mounts = map[string]string{}
	var section, target, source string
	flush := func() {
		if section == "customMounts" && target != "" && source != "" {
			mounts[path.Clean(target)] = source
		}
		target, source = "", ""
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '-' { // a top-level key opens a section
			flush()
			section, _, _ = strings.Cut(trimmed, ":")
			continue
		}
		if strings.HasPrefix(trimmed, "- ") { // the next item of a list
			flush()
			trimmed = strings.TrimSpace(trimmed[2:])
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch {
		case key == "targetPath":
			target = val
		case key == "sourcePath":
			source = val
		case key == "umbrel-downloads" && strings.HasPrefix(val, "/"): // Downloads pointed at another folder
			downloads = val
		}
	}
	flush()
	return downloads, mounts
}

// placesFor gives the places a container shows: the given ones and the folders the user mounted (see addedMounts),
// named by the paths of Umbrel's Files where the copy of its settings tells them.
func placesFor(stateDir string, given []api.Place) []api.Place {
	places := append(append([]api.Place(nil), given...), addedMountsHere(stateDir, given)...)
	f, err := os.Open(filepath.Join(stateDir, umbrelSettingsFile))
	if err != nil {
		return places
	}
	defer f.Close()
	downloads, mounts := umbrelNames(f)
	for i, p := range places {
		if src, ok := mounts[path.Clean(filepath.ToSlash(p.Path))]; ok {
			places[i].Name = filesName(src)
		} else if downloads != "" && filepath.ToSlash(filepath.Clean(p.Path)) == "/downloads" { // Umbrel's Downloads slot
			places[i].Name = filesName(downloads)
		}
	}
	return places
}

// filesName is how Umbrel itself shows a folder it mounts: its path without the first step (Home, External, Network),
// "/External/RAID/Torrents" as RAID › Torrents, here "RAID/Torrents".
func filesName(src string) string {
	s := strings.Trim(src, "/")
	if _, rest, ok := strings.Cut(s, "/"); ok && rest != "" {
		return rest
	}
	return s
}
