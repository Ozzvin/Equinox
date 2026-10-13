package core

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/anacrolix/torrent"
)

// Versions before 1.0.45 put every file of a torrent right after the one before it, but in a torrent with a v2 part
// (BEP 52, hybrid ones too) every file starts on a piece boundary: from the second file on, such a torrent was written
// at the wrong places and read back from them, so its pieces passed their hashes while the files on the disk were
// spoiled. The first run of a version that does it right checks every such torrent once: what is wrong is found and
// downloaded again.

// recheckOldV2 checks, once, the torrents added by an older version that have a v2 part, more than one file and some
// data. Called once, after the torrents are back.
func (m *Manager) recheckOldV2() {
	for hash, r := range m.snapshotRecords() {
		if r.V2Layout {
			continue
		}
		t, err := m.get(hash)
		if err != nil {
			continue
		}
		go m.recheckIfOldV2(t, hash)
	}
}

func (m *Manager) recheckIfOldV2(t *torrent.Torrent, hash string) {
	select {
	case <-t.GotInfo():
	case <-m.done:
		return
	}
	info := t.Info()
	if info == nil {
		return
	}
	if !info.HasV2() || len(info.UpvertedFiles()) < 2 || t.BytesCompleted() == 0 {
		m.markV2Layout(hash) // nothing that could have been written wrongly
		return
	}
	for {
		err := m.Recheck(hash)
		if err == nil {
			m.markV2Layout(hash)
			return
		}
		if !errors.Is(err, ErrBusy) { // removed, or the like
			return
		}
		select { // being checked or moved right now: try again a little later
		case <-m.done:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (m *Manager) markV2Layout(hash string) {
	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.V2Layout = true
		}
	}); err != nil {
		fmt.Fprintf(os.Stderr, "saving v2Layout for %s: %v\n", hash, err)
	}
}
