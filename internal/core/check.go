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
		m.mu.Lock()
		_, counted := m.checkPos[hash] // a recheck of ours keeps its own, exact, progress
		m.mu.Unlock()
		if counted {
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
		m.syncPerm(t, hash) // a check that begins or ends lets downloading stop or go on
	}
}

// checkHolds says whether a torrent is being checked now, which stops its downloading. Called with m.mu held.
func (m *Manager) checkHolds(hash string) bool {
	if m.checking[hash] {
		return true
	}
	_, ok := m.checkProg[hash]
	return ok
}

// fileChecked is the share of each file of t already checked while it is being checked, nil when it is not: for a
// recheck of ours what lies before its place, for the engine's own check the pieces no longer waiting for a hash.
func (m *Manager) fileChecked(t *torrent.Torrent, hash string) []float64 {
	m.mu.Lock()
	pos, ours := m.checkPos[hash]
	_, checking := m.checkProg[hash]
	m.mu.Unlock()
	if !ours && !checking {
		return nil
	}
	fs := t.Files()
	out := make([]float64, len(fs))
	if ours {
		upto := checkedBytes(t, pos)
		for i, f := range fs {
			if f.Length() > 0 {
				out[i] = float64(min(max(upto-f.Offset(), 0), f.Length())) / float64(f.Length())
			} else {
				out[i] = 1
			}
		}
		return out
	}
	pending := make([]bool, t.NumPieces())
	at := 0
	for _, run := range t.PieceStateRuns() {
		for k := 0; k < run.Length && at < len(pending); k++ {
			pending[at] = run.Hashing || run.QueuedForHash
			at++
		}
	}
	for i, f := range fs {
		b, e := f.BeginPieceIndex(), f.EndPieceIndex()
		if e <= b {
			out[i] = 1
			continue
		}
		left := 0
		for p := b; p < e && p < len(pending); p++ {
			if pending[p] {
				left++
			}
		}
		out[i] = 1 - float64(left)/float64(e-b)
	}
	return out
}

func (m *Manager) forgetChecks(hash string) {
	m.mu.Lock()
	delete(m.phase, hash)
	delete(m.checkProg, hash)
	m.mu.Unlock()
}
