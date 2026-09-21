package core

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Limit on simultaneous checks of local files.
//
// The engine starts hashing a torrent's files the moment it learns the torrent's info, and it has no
// switch to hold that back. So a torrent that would need a check is added to the engine without its
// info first, and the info is handed over (which starts the check) only when a slot is free. Until
// then the torrent waits in a line, and Status.CheckQueued says its place. The line follows the queue
// order of the list (the "#" column): checks go from the top down, and moving a torrent up or down in the
// queue moves it in the line too.
//
// A torrent without info would fetch it from the swarm like a magnet link does, and hash behind the
// gate's back; so while its info is held the torrent gets no connections (see applyConnLimit).

// heldInfo is the info of a torrent that waits for a check slot.
type heldInfo struct {
	info []byte
	size int64 // total size, so the list can show it while the torrent waits
}

// holdBack decides whether a torrent waits for a check slot. When it does, the spec to give to the
// engine is returned without the info, which is kept here until runGate lets the torrent in.
func (m *Manager) holdBack(spec *torrent.TorrentSpec) *torrent.TorrentSpec {
	if m.cfg.Get().MaxConcurrentChecks <= 0 || len(spec.InfoBytes) == 0 {
		return spec
	}
	hash := spec.InfoHash.HexString()
	// A free slot and nobody waiting ahead: the torrent goes in with its info at once and takes the slot;
	// runGate gives it back when the first check is over.
	m.mu.Lock()
	if len(m.held) == 0 && len(m.gateActive)+len(m.rechecking) < m.cfg.Get().MaxConcurrentChecks {
		m.gateActive[hash] = true
		m.mu.Unlock()
		return spec
	}
	m.mu.Unlock()
	var size int64
	var info metainfo.Info
	if err := bencode.Unmarshal(spec.InfoBytes, &info); err == nil {
		size = info.TotalLength()
	}
	m.mu.Lock()
	m.held[hash] = &heldInfo{info: spec.InfoBytes, size: size}
	m.mu.Unlock()
	cp := *spec
	cp.InfoBytes = nil
	return &cp
}

// dropHeld forgets a torrent's place in the line and its slot (it was removed, or could not be added).
func (m *Manager) dropHeld(hash string) {
	m.mu.Lock()
	delete(m.held, hash)
	delete(m.gateActive, hash)
	delete(m.rechecking, hash)
	m.mu.Unlock()
}

// checkLine lists the waiting torrents in queue order. It must be called without m.mu held.
func (m *Manager) checkLine() []string {
	m.mu.Lock()
	line := make([]string, 0, len(m.held))
	for h := range m.held {
		line = append(line, h)
	}
	m.mu.Unlock()
	if len(line) < 2 {
		return line
	}
	recs := m.snapshotRecords()
	sort.Slice(line, func(i, j int) bool {
		a, b := recs[line[i]], recs[line[j]]
		if a.Order != b.Order || !a.Added.Equal(b.Added) {
			return orderLess(&a, &b)
		}
		return line[i] < line[j]
	})
	return line
}

// checkQueuePos is the place of a torrent in the line for a check (1 = next), 0 if it is not waiting.
func (m *Manager) checkQueuePos(hash string) (pos int, size int64) {
	m.mu.Lock()
	h := m.held[hash]
	m.mu.Unlock()
	if h == nil {
		return 0, 0
	}
	for i, x := range m.checkLine() {
		if x == hash {
			return i + 1, h.size
		}
	}
	return 0, 0
}

// runGate is called every tick: it frees the slots of torrents whose first check is over and lets the
// waiting ones in while there are free slots.
func (m *Manager) runGate() {
	m.mu.Lock()
	var active []string
	for h := range m.gateActive {
		active = append(active, h)
	}
	m.mu.Unlock()
	for _, h := range active {
		t, err := m.get(h)
		if err != nil || t.Info() == nil {
			m.mu.Lock()
			delete(m.gateActive, h)
			m.mu.Unlock()
			continue
		}
		if c, _ := checkingPieces(t); c == 0 {
			m.mu.Lock()
			delete(m.gateActive, h)
			m.mu.Unlock()
		}
	}
	m.shrinkToLimit()

	for {
		hash, info, ok := m.nextHeld()
		if !ok {
			return
		}
		t, err := m.get(hash)
		if err != nil {
			m.dropHeld(hash)
			continue
		}
		if err := t.SetInfoBytes(info); err != nil {
			m.dropHeld(hash)
			continue
		}
		m.applyConnLimit(t, hash, m.snapshotRecords()[hash]) // the swarm may be used now that the info is here
		// The engine queued whatever needs hashing while setting the info; nothing queued means nothing
		// to check, so the slot is free again at once.
		if c, _ := checkingPieces(t); c == 0 {
			m.mu.Lock()
			delete(m.gateActive, hash)
			m.mu.Unlock()
		}
	}
}

// nextHeld takes the first waiting torrent in queue order if a slot is free (no limit: any).
func (m *Manager) nextHeld() (hash string, info []byte, ok bool) {
	limit := m.cfg.Get().MaxConcurrentChecks
	m.mu.Lock()
	full := (limit > 0 && len(m.gateActive)+len(m.rechecking) >= limit) || len(m.held) == 0
	m.mu.Unlock()
	if full {
		return "", nil, false
	}
	line := m.checkLine()
	if len(line) == 0 {
		return "", nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	hash = line[0]
	h := m.held[hash]
	if h == nil {
		return "", nil, false
	}
	delete(m.held, hash)
	m.gateActive[hash] = true
	return hash, h.info, true
}

// acquireRecheck waits for a slot for a check the user asked for; the returned function frees it.
// It gives up (false) when stop is closed.
func (m *Manager) acquireRecheck(hash string, stop <-chan struct{}) (release func(), ok bool) {
	for {
		limit := m.cfg.Get().MaxConcurrentChecks
		m.mu.Lock()
		if limit <= 0 || len(m.gateActive)+len(m.rechecking) < limit {
			m.rechecking[hash] = true
			m.mu.Unlock()
			return func() {
				m.mu.Lock()
				delete(m.rechecking, hash)
				m.mu.Unlock()
			}, true
		}
		m.mu.Unlock()
		select {
		case <-stop:
			return nil, false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// applyConnLimit gives a torrent its connection limit. A torrent whose info is held back gets none:
// without info the engine would fetch it from the swarm, exactly as for a magnet link, and start
// hashing behind the gate's back.
func (m *Manager) applyConnLimit(t *torrent.Torrent, hash string, rec record) {
	m.mu.Lock()
	_, held := m.held[hash]
	m.mu.Unlock()
	if held {
		t.SetMaxEstablishedConns(0)
		return
	}
	t.SetMaxEstablishedConns(m.connLimit(rec))
}

// shrinkToLimit makes the checks that are running fit the limit after it was lowered: the torrents lowest
// in the queue go back to the line (their info is taken away by dropping the torrent from the engine and
// adding it again without it, so the check stops; what was verified is remembered and the check goes on
// from there when their turn comes).
func (m *Manager) shrinkToLimit() {
	limit := m.cfg.Get().MaxConcurrentChecks
	if limit <= 0 {
		return
	}
	m.mu.Lock()
	over := len(m.gateActive) + len(m.rechecking) - limit
	var cands []string
	if over > 0 {
		for h := range m.gateActive {
			cands = append(cands, h)
		}
	}
	m.mu.Unlock()
	if over <= 0 {
		return
	}
	recs := m.snapshotRecords()
	sort.Slice(cands, func(i, j int) bool { // the lowest in the queue first
		a, b := recs[cands[i]], recs[cands[j]]
		return orderLess(&b, &a)
	})
	for _, h := range cands {
		if over <= 0 {
			return
		}
		t, err := m.get(h)
		if err != nil || t.Info() == nil || m.isMoving(h) {
			continue
		}
		if c, _ := checkingPieces(t); c == 0 {
			continue // not hashing: its slot is freed by runGate
		}
		m.requeueCheck(h, t)
		over--
	}
}

// requeueCheck stops the check of a torrent and puts the torrent back into the line.
func (m *Manager) requeueCheck(hash string, t *torrent.Torrent) {
	m.syncCounters()
	m.mu.Lock()
	delete(m.gateActive, hash)
	m.mu.Unlock()
	m.forgetChecks(hash)
	m.dropForMove(hash, t)
	if err := m.restoreByHash(hash); err != nil {
		fmt.Fprintf(os.Stderr, "putting %s back into the line for a check: %v\n", hash, err)
	}
}
