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
	size int64
	mod  time.Time
}

// scanWatch adds the .torrent files that appeared in the watch folder. A file is taken only
// when it looks the same as on the previous scan, so a file that is still being copied is
// left alone. Added files get the suffix ".added", unusable ones ".failed", which also
// keeps them from being picked up again.
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
		now := watchStamp{fi.Size(), fi.ModTime()}
		seen[p] = now
		if prev, ok := m.watchSeen[p]; !ok || prev != now || fi.Size() == 0 {
			continue // new, still changing, or empty: look again next time
		}
		suffix := ".added"
		if _, err := m.AddFile(p); err != nil {
			fmt.Fprintf(os.Stderr, "watch folder: %s: %v\n", e.Name(), err)
			suffix = ".failed"
		}
		if err := os.Rename(p, p+suffix); err != nil {
			fmt.Fprintf(os.Stderr, "watch folder: cannot rename %s: %v\n", e.Name(), err)
			continue
		}
		delete(seen, p)
	}
	m.watchSeen = seen
}
