package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// watchStamp identifies the state of a file, to tell when a copy has finished.
type watchStamp struct {
	size    int64
	mod     time.Time
	handled bool // added (or found unusable) already, but it could not be renamed: left alone until it changes
}

// maxWatchedTorrent is the biggest file the watch folder takes, as for a .torrent sent to the page.
const maxWatchedTorrent = 8 << 20

// scanWatch adds the .torrent files that appeared in the watch folder. A file is taken only
// when it looks the same as on the previous scan, so a file that is still being copied is
// left alone. Added files get the suffix ".added", unusable ones ".failed", which also
// keeps them from being picked up again; one that cannot be renamed (a folder only for
// reading, a file held open) is remembered instead, and taken again only once it changes.
func (m *Manager) scanWatch() {
	dir := m.cfg.Get().WatchDir
	if dir == "" {
		m.watchSeen = map[string]watchStamp{}
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	seen := map[string]watchStamp{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".torrent") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		now := watchStamp{size: fi.Size(), mod: fi.ModTime()}
		prev, ok := m.watchSeen[p]
		if ok && prev.handled && prev.size == now.size && prev.mod.Equal(now.mod) {
			seen[p] = prev
			continue
		}
		seen[p] = now
		if !ok || prev != now || fi.Size() == 0 {
			continue // new, still changing, or empty: look again next time
		}
		suffix := ".added"
		if fi.Size() > maxWatchedTorrent {
			fmt.Fprintf(os.Stderr, "watch folder: %s: bigger than %d MiB, not a .torrent\n", e.Name(), maxWatchedTorrent>>20)
			suffix = ".failed"
		} else if _, err := m.AddFile(p); err != nil {
			fmt.Fprintf(os.Stderr, "watch folder: %s: %v\n", e.Name(), err)
			suffix = ".failed"
		}
		if err := os.Rename(p, p+suffix); err != nil {
			fmt.Fprintf(os.Stderr, "watch folder: cannot rename %s: %v\n", e.Name(), err)
			now.handled = true
			seen[p] = now
			continue
		}
		delete(seen, p)
	}
	m.watchSeen = seen
}
