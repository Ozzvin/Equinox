package app

import (
	"bufio"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Ozzvin/equinox/internal/api"
)

// Folders the user adds to the container themselves (umbrelOS 2.0: the app's settings → "Add your own folder") are
// shown next to the given places, by their whole mount point without the leading slash. The container cannot see
// where the folder really is (Umbrel says nothing of it), but the mount point is the user's own choice: mounted at
// /External/RAID/Torrents it reads "External/RAID/Torrents/<film>" here. Where Umbrel's settings are at hand, the name
// comes from them instead (see placesFor). Without this the folder picker, which offers only the places, did not show such
// a folder at all (found on Umbrel, 2026-10-11).

// systemMounts are where the container's own mounts live; nothing there is the user's folder.
var systemMounts = []string{"/proc", "/sys", "/dev", "/run", "/etc", "/tmp", "/var", "/usr", "/lib", "/bin", "/sbin"}

// addedMounts reads mountinfo and returns the mounted folders that are not the system's, the state folder or one of
// the given places (or inside them). isDir says whether a path is a folder (Docker also mounts single files).
func addedMounts(mountinfo io.Reader, stateDir string, places []api.Place, isDir func(string) bool) []api.Place {
	skip := func(p string) bool {
		if p == "/" || !isDir(p) {
			return true
		}
		for _, s := range systemMounts {
			if p == s || strings.HasPrefix(p, s+"/") {
				return true
			}
		}
		taken := []string{filepath.ToSlash(filepath.Clean(stateDir))}
		for _, pl := range places {
			taken = append(taken, filepath.ToSlash(filepath.Clean(pl.Path)))
		}
		for _, t := range taken {
			if p == t || strings.HasPrefix(p, t+"/") || strings.HasPrefix(t, p+"/") {
				return true
			}
		}
		return false
	}
	var out []api.Place
	seen := map[string]bool{}
	sc := bufio.NewScanner(mountinfo)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		p := unescapeMount(f[4]) // the mount point
		if seen[p] || skip(p) {
			continue
		}
		seen[p] = true
		out = append(out, api.Place{Name: strings.TrimPrefix(p, "/"), Path: p})
	}
	return out
}

// unescapeMount undoes mountinfo's escapes: a space is \040, a tab \011, a backslash \134.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
