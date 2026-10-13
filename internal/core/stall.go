package core

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// The engine can be left connected to a seed that has everything it wants, unchoked by it, asking it for nothing:
// seen in the tests, a fresh torrent and a seed connected and both silent for good, roughly once in fifty runs, on
// Windows and Linux alike. Why, the engine's state does not tell (no request is held anywhere, every piece is
// wanted and requestable).
//
// So a torrent that may download, is connected to at least one seed and has had no useful data for stallNudgeAfter
// gets its connections closed, and they come up afresh. At most once every two such spells, so a torrent that the
// seeds simply do not serve (they choke it, as busy seeds do) is not churned.

var stallNudgeAfter = 30 * time.Second // the tests take less

type stallWatch struct {
	read   int64     // useful data read when it last changed
	since  time.Time // since when it has not changed
	nudged time.Time
}

// nudgeStalled is run every tick for each torrent that is not paused.
func (m *Manager) nudgeStalled(t *torrent.Torrent, r record) {
	h := t.InfoHash()
	st := t.Stats()
	read := st.BytesReadUsefulData.Int64()
	size, done := selection(t, r.FilePrios)
	now := time.Now()

	m.mu.Lock()
	w := m.stalls[h]
	if w == nil || read != w.read || st.ConnectedSeeders == 0 || done >= size || !m.mayDownload(h) {
		// moving, nobody to ask, nothing to ask for or not allowed to: watch afresh from now
		if w == nil {
			w = &stallWatch{}
			m.stalls[h] = w
		}
		w.read, w.since = read, now
		m.mu.Unlock()
		return
	}
	due := now.Sub(w.since) >= stallNudgeAfter && now.Sub(w.nudged) >= 2*stallNudgeAfter
	if due {
		w.nudged, w.since = now, now
	}
	m.mu.Unlock()
	if due {
		fmt.Fprintf(os.Stderr, "%s: connected to a seed, nothing received for %v, reconnecting\n", t.Name(), stallNudgeAfter)
		m.nudge(t, h)
	}
}

// nudge closes the torrent's connections and hands their peers back to the engine to dial afresh, unless the program
// has meanwhile decided the torrent may not download.
//
// Switching downloading off and on was tried first: the engine worked the requests out again, and still asked for
// nothing. A fresh connection has no such trouble. The engine does not dial a closed peer again by itself (it would
// wait for the trackers or the DHT to bring it back, minutes away), so its address goes back on the list: the one it
// listens on, which for a peer that called us is not the port it called from.
func (m *Manager) nudge(t *torrent.Torrent, h metainfo.Hash) {
	m.permMu.Lock()
	defer m.permMu.Unlock()
	m.mu.Lock()
	allowed := m.mayDownload(h)
	m.mu.Unlock()
	if !allowed {
		return
	}
	var again []torrent.PeerInfo
	for _, pc := range t.PeerConns() {
		addr := pc.RemoteAddr.String()
		if host, _, err := net.SplitHostPort(addr); err == nil && pc.PeerListenPort > 0 {
			addr = net.JoinHostPort(host, strconv.Itoa(pc.PeerListenPort))
		}
		again = append(again, torrent.PeerInfo{Addr: torrent.StringAddr(addr), Source: torrent.PeerSourceDirect})
		_ = pc.Close()
	}
	t.AddPeers(again)
}

// mayDownload is what the engine was last told about the torrent's downloading (see syncPerm). m.mu is held.
func (m *Manager) mayDownload(h metainfo.Hash) bool {
	p, known := m.perm[h]
	return !known || p.down // a torrent not yet seen by syncPerm may do everything
}
