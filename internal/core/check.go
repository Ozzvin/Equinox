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
// recheck of ours its pieces already done, for the engine's own check the pieces no longer waiting for a hash. A file a
// check of chosen files does not include gets -1: it is not being checked and shows its usual progress. While a
// recheck of ours waits for its turn, the files it will check get -2.
func (m *Manager) fileChecked(t *torrent.Torrent, hash string) []float64 {
	m.mu.Lock()
	pos, ours := m.checkPos[hash]
	sel, chosen := m.checkSel[hash]
	_, checking := m.checkProg[hash]
	m.mu.Unlock()
	fs := t.Files()
	if !ours && !checking {
		if !chosen {
			return nil
		}
		in := map[int]bool{}
		for _, i := range sel.files {
			in[i] = true
		}
		out := make([]float64, len(fs))
		for i := range out {
			out[i] = -1
			if len(sel.files) == 0 || in[i] {
				out[i] = -2
			}
		}
		return out
	}
	out := make([]float64, len(fs))
	if ours {
		done := make([]bool, t.NumPieces())
		for k := 0; k < pos && k < len(sel.pieces); k++ {
			done[sel.pieces[k]] = true
		}
		in := map[int]bool{}
		for _, i := range sel.files {
			in[i] = true
		}
		info := t.Info()
		for i, f := range fs {
			if len(sel.files) > 0 && !in[i] {
				out[i] = -1
				continue
			}
			if f.Length() == 0 || info == nil {
				out[i] = 1
				continue
			}
			var got int64
			for p := f.BeginPieceIndex(); p < f.EndPieceIndex() && p < len(done); p++ {
				if !done[p] {
					continue
				}
				pc := info.Piece(p)
				lo, hi := max(pc.Offset(), f.Offset()), min(pc.Offset()+pc.Length(), f.Offset()+f.Length())
				if hi > lo {
					got += hi - lo
				}
			}
			out[i] = float64(got) / float64(f.Length())
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
