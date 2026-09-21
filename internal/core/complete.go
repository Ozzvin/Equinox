package core

import (
	"fmt"
	"os"

	"github.com/anacrolix/torrent"
)

// handleCompletion notices when a torrent that was unfinished becomes finished, and moves
// it to the "completed" folder if one is configured. Torrents that were already complete
// when added (or when the folder was configured) are left alone.
// keepEdges re-raises the file edges of a torrent that asked for them (see prioritise).
func (m *Manager) keepEdges(t *torrent.Torrent, r record) {
	if r.EdgePieces && !r.Sequential { // sequential mode does it every second anyway
		m.prioritise(t, false)
	}
}

func (m *Manager) handleCompletion(t *torrent.Torrent, hash string, r record) {
	size, done := selection(t, r.FilePrios)
	switch {
	case done < size && !r.WasIncomplete:
		_ = m.state.with(func(s *state) {
			if x := s.Torrents[hash]; x != nil {
				x.WasIncomplete = true
			}
		})
	case done >= size && r.WasIncomplete:
		_ = m.state.with(func(s *state) {
			if x := s.Torrents[hash]; x != nil {
				x.WasIncomplete = false
			}
		})
		if d, _ := counters(t); r.Downloaded+d > 0 { // not merely found on disk by the first check
			m.emit(Event{Kind: "completed", Hash: hash, Name: t.Name()})
		}
		dir := r.MoveDone // this torrent's own folder wins over the global one
		if dir == "" {
			dir = m.cfg.Get().MoveCompletedDir
		}
		if dir != "" && !m.isMoving(hash) {
			if err := m.MoveStorage(hash, dir); err != nil {
				fmt.Fprintf(os.Stderr, "move completed %s: %v\n", t.Name(), err)
			}
		}
	}
}
