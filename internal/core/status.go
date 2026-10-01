package core

import (
	"strings"
	"time"

	"github.com/anacrolix/torrent"
)

// Extras for the Status tab of the details panel and the options of a single torrent.

// StatusExtra is what the Status tab shows besides the numbers of the torrent list.
type StatusExtra struct {
	ActiveSeconds     int64     `json:"activeSeconds"`     // time the torrent has been running (not paused), adds up across restarts
	LastTransfer      int64     `json:"lastTransfer"`      // seconds since data last moved in this session, -1 = not yet
	Availability      float64   `json:"availability"`      // distributed copies among the connected peers and us, right now
	LastSeedSeen      time.Time `json:"lastSeedSeen"`      // see record.LastSeedSeen
	LastFullAvailable time.Time `json:"lastFullAvailable"` // see record.LastFullAvailable
}

// StatusExtra returns the extras of one torrent.
func (m *Manager) StatusExtra(hash string) (StatusExtra, error) {
	t, err := m.get(hash)
	if err != nil {
		return StatusExtra{}, err
	}
	rec := m.snapshotRecords()[hash]
	out := StatusExtra{
		ActiveSeconds: m.activeSeconds(hash, rec), LastTransfer: -1,
		LastSeedSeen: rec.LastSeedSeen, LastFullAvailable: rec.LastFullAvailable,
	}
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

// availabilityCheckEvery is how many ticks of the main loop pass between the (pricier, since it walks
// every piece) full-availability checks in trackAvailability; the same cadence as the loop's own periodic
// save.
const availabilityCheckEvery = 15

// trackAvailability remembers, per incomplete torrent, the last time a single connected peer had every
// piece (a plain seed) and the last time every piece was available somewhere among the connected peers and
// us combined, even if no one of them alone had them all. Both answer "could this have finished" after the
// fact, once the live Availability number (see StatusExtra) has since dropped back under 1 — a seed that
// left, or a swarm that only briefly had every piece covered between several partial peers. A peer's own
// reported piece count is cheap to check and done every call; the full walk (availability) only every
// availabilityCheckEvery-th.
func (m *Manager) trackAvailability(ts []*torrent.Torrent, recs map[string]record, tickN int) {
	type found struct{ seed, full bool }
	updates := map[string]found{}
	for _, t := range ts {
		if t.Info() == nil {
			continue
		}
		hash := t.InfoHash().HexString()
		rec, ok := recs[hash]
		if !ok {
			continue
		}
		if size, done := selection(t, rec.FilePrios); done >= size {
			continue // already complete: trivially available from us alone, nothing to learn
		}
		var f found
		n := t.NumPieces()
		for _, pc := range t.PeerConns() {
			if pc.Stats().RemotePieceCount >= n {
				f.seed = true
				break
			}
		}
		if tickN%availabilityCheckEvery == 0 && availability(t) >= 1 {
			f.full = true
		}
		if f.seed || f.full {
			updates[hash] = f
		}
	}
	if len(updates) == 0 {
		return
	}
	now := time.Now()
	m.state.touch(func(s *state) {
		for hash, f := range updates {
			r := s.Torrents[hash]
			if r == nil {
				continue
			}
			if f.seed {
				r.LastSeedSeen = now
			}
			if f.full {
				r.LastFullAvailable = now
			}
		}
	})
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
	m.state.touch(func(s *state) {
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

// SetMoveDone sets whether and where this torrent moves when it finishes. enabled=false means
// never move it, even if a global or own folder is set. enabled=true with an empty path falls
// back to the global folder; a non-empty path overrides it (and is created if it is missing).
func (m *Manager) SetMoveDone(hash string, enabled bool, path string) error {
	if _, err := m.get(hash); err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if enabled && path != "" {
		abs, err := ensureDir(path)
		if err != nil {
			return err
		}
		path = abs
	} else {
		path = ""
	}
	return m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.MoveDone = path
			r.MoveDoneOff = !enabled
		}
	})
}
