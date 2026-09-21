package core

import (
	"time"

	"github.com/anacrolix/torrent"
)

// When a torrent is added and its files are already on disk, the engine hashes them to find
// out what is complete. That is the "check of local files": it happens by itself, and here it
// is made visible with a percentage. The same measure serves a manual recheck.

// checkPhase follows the first check of a torrent after it was added or restored.
type checkPhase struct {
	since time.Time
	seen  bool // pieces were seen waiting for or undergoing a check
}

// startCheckPhase begins watching a torrent whose files were just opened.
func (m *Manager) startCheckPhase(hash string) {
	m.mu.Lock()
	m.phase[hash] = &checkPhase{since: time.Now()}
	m.mu.Unlock()
}

// checkingPieces counts pieces waiting to be hashed or being hashed.
func checkingPieces(t *torrent.Torrent) (checking, total int) {
	for _, run := range t.PieceStateRuns() {
		total += run.Length
		if run.Hashing || run.QueuedForHash {
			checking += run.Length
		}
	}
	return
}

// updateChecks refreshes the check progress of torrents in their first check or being
// rechecked. Other torrents are not looked at: counting pieces is not free, and pieces are
// hashed all the time while downloading, which is not a "check".
func (m *Manager) updateChecks(ts []*torrent.Torrent) {
	m.mu.Lock()
	watch := map[string]bool{}
	for h := range m.phase {
		watch[h] = true
	}
	for h := range m.checking {
		watch[h] = true
	}
	m.mu.Unlock()
	if len(watch) == 0 {
		return
	}
	for _, t := range ts {
		hash := t.InfoHash().HexString()
		if !watch[hash] || t.Info() == nil {
			continue
		}
		c, total := checkingPieces(t)
		m.mu.Lock()
		ph := m.phase[hash]
		switch {
		case c > 0 && total > 0:
			if ph != nil {
				ph.seen = true
			}
			m.checkProg[hash] = 1 - float64(c)/float64(total)
		default:
			delete(m.checkProg, hash)
			// Done once a check was seen and has finished, or when nothing needed checking.
			if ph != nil && (ph.seen || time.Since(ph.since) > 5*time.Second) {
				delete(m.phase, hash)
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) forgetChecks(hash string) {
	m.mu.Lock()
	delete(m.phase, hash)
	delete(m.checkProg, hash)
	m.mu.Unlock()
}
