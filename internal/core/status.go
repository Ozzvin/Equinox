package core

import (
	"strings"
	"time"

	"github.com/anacrolix/torrent"
)

// Extras for the Status tab of the details panel and the options of a single torrent.

// StatusExtra is what the Status tab shows besides the numbers of the torrent list.
type StatusExtra struct {
	ActiveSeconds int64   `json:"activeSeconds"` // time the torrent has been running (not paused), adds up across restarts
	LastTransfer  int64   `json:"lastTransfer"`  // seconds since data last moved in this session, -1 = not yet
	Availability  float64 `json:"availability"`  // distributed copies among the connected peers and us
}

// StatusExtra returns the extras of one torrent.
func (m *Manager) StatusExtra(hash string) (StatusExtra, error) {
	t, err := m.get(hash)
	if err != nil {
		return StatusExtra{}, err
	}
	rec := m.snapshotRecords()[hash]
	out := StatusExtra{ActiveSeconds: m.activeSeconds(hash, rec), LastTransfer: -1}
	m.mu.Lock()
	at := m.activeAt[t.InfoHash()]
	m.mu.Unlock()
	if !at.IsZero() {
		out.LastTransfer = int64(time.Since(at) / time.Second)
	}
	if t.Info() != nil {
		out.Availability = availability(t)
	}
	return out, nil
}

// availability is the number of complete copies of the torrent among the connected peers and us:
// the integer part is how many copies every piece has at least, the fraction is the share of pieces
// that have more than that (the same as "distributed copies" in other clients).
func availability(t *torrent.Torrent) float64 {
	n := t.NumPieces()
	if n == 0 {
		return 0
	}
	counts := make([]int, n)
	at := 0
	for _, run := range t.PieceStateRuns() {
		for i := 0; i < run.Length && at < n; i++ {
			if run.Complete {
				counts[at]++
			}
			at++
		}
	}
	for _, pc := range t.PeerConns() {
		pc.PeerPieces().Iterate(func(x uint32) bool {
			if int(x) < n {
				counts[x]++
			}
			return true
		})
	}
	min := counts[0]
	for _, c := range counts {
		if c < min {
			min = c
		}
	}
	more := 0
	for _, c := range counts {
		if c > min {
			more++
		}
	}
	return float64(min) + float64(more)/float64(n)
}

// ---------------------------------------------------------------- active time

func (m *Manager) activeSeconds(hash string, r record) int64 {
	m.mu.Lock()
	pend := m.activePend[hash]
	m.mu.Unlock()
	return r.ActiveSeconds + int64(pend/time.Second)
}

// addActiveTime counts dt of running time for a torrent that is not paused.
func (m *Manager) addActiveTime(hash string, dt time.Duration) {
	m.mu.Lock()
	m.activePend[hash] += dt
	m.mu.Unlock()
}

// flushActiveTime moves the counted running time into the persisted records.
func (m *Manager) flushActiveTime() {
	m.mu.Lock()
	whole := map[string]int64{}
	for h, d := range m.activePend {
		if s := int64(d / time.Second); s > 0 {
			whole[h] = s
			m.activePend[h] = d - time.Duration(s)*time.Second
		}
	}
	m.mu.Unlock()
	if len(whole) == 0 {
		return
	}
	_ = m.state.with(func(s *state) {
		for h, secs := range whole {
			if r := s.Torrents[h]; r != nil {
				r.ActiveSeconds += secs
			}
		}
	})
}

// ---------------------------------------------------------------- per-torrent options

// SetEdgePieces switches "first and last pieces first" for one torrent.
func (m *Manager) SetEdgePieces(hash string, on bool) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.EdgePieces = on
		}
	}); err != nil {
		return err
	}
	m.applyFiles(t, hash)
	return nil
}

// SetMoveDone sets the folder this torrent moves to when it finishes; an empty path goes back to the
// global setting. A folder that does not exist is created.
func (m *Manager) SetMoveDone(hash, path string) error {
	if _, err := m.get(hash); err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if path != "" {
		abs, err := ensureDir(path)
		if err != nil {
			return err
		}
		path = abs
	}
	return m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.MoveDone = path
		}
	})
}
