package core

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
)

// Peers the user added by hand are kept with the torrent, so a restart does not lose them, and are tried again now
// and then. One that has not been connected for a long time is not dropped behind the user's back (a seedbox at
// home can be off for weeks): it is reported as stale, and the interface offers to remove it.

const (
	maxSavedPeers    = 50                  // per torrent
	manualPeerStale  = 14 * 24 * time.Hour // no connection with it for this long: offer to remove it
	manualSeenEvery  = 30                  // ticks (seconds) between looks at who is connected
	manualRetryEvery = 300                 // ticks between new attempts to connect to the saved peers
)

// manualPeer is one saved entry. Addr is the address in its plain form or a name with the port; a name is looked
// up again each time, so a dynamic address is followed.
type manualPeer struct {
	Addr     string    `json:"addr"`
	Added    time.Time `json:"added"`
	LastSeen time.Time `json:"lastSeen,omitempty"` // when a connection with this peer (by its IP) was last seen
}

// ManualPeerInfo is a saved peer as the interface shows it.
type ManualPeerInfo struct {
	Addr      string    `json:"addr"`
	Added     time.Time `json:"added"`
	LastSeen  time.Time `json:"lastSeen"`
	Connected bool      `json:"connected"` // a connection with it exists now
	Stale     bool      `json:"stale"`     // not connected for manualPeerStale: worth removing
}

// saveManualPeers keeps the entries with the torrent; one that is already there counts as seen just now.
func (m *Manager) saveManualPeers(hash string, keys []string) error {
	var tooMany bool
	now := time.Now()
	err := m.state.with(func(s *state) {
		r := s.Torrents[hash]
		if r == nil {
			return
		}
		have := map[string]int{}
		for i, p := range r.ManualPeers {
			have[p.Addr] = i
		}
		var fresh []manualPeer
		for _, k := range keys {
			if i, ok := have[k]; ok {
				if i >= 0 {
					r.ManualPeers[i].LastSeen = now
				}
				continue
			}
			have[k] = -1
			fresh = append(fresh, manualPeer{Addr: k, Added: now, LastSeen: now})
		}
		if len(r.ManualPeers)+len(fresh) > maxSavedPeers {
			tooMany = true
			return
		}
		r.ManualPeers = append(r.ManualPeers, fresh...)
	})
	if err != nil {
		return err
	}
	if tooMany {
		return fmt.Errorf("%w: at most %d peers can be kept for one torrent; remove some first", ErrInvalidInput, maxSavedPeers)
	}
	return nil
}

// rememberPeerIPs notes which IP addresses an entry stands for at the moment, to tell its connections apart.
func (m *Manager) rememberPeerIPs(hash, key string, addrs []string) {
	m.manualMu.Lock()
	defer m.manualMu.Unlock()
	if m.manualIPs == nil {
		m.manualIPs = map[string][]string{}
	}
	m.manualIPs[hash+"|"+key] = addrs
}

func (m *Manager) forgetPeerIPs(hash string) {
	m.manualMu.Lock()
	defer m.manualMu.Unlock()
	for k := range m.manualIPs {
		if strings.HasPrefix(k, hash+"|") {
			delete(m.manualIPs, k)
		}
	}
}

// entryIPs are the IP addresses (no ports) an entry stands for.
func (m *Manager) entryIPs(hash, key string) map[string]bool {
	m.manualMu.Lock()
	addrs := m.manualIPs[hash+"|"+key]
	m.manualMu.Unlock()
	if len(addrs) == 0 {
		addrs = []string{key}
	}
	out := map[string]bool{}
	for _, a := range addrs {
		if ap, err := netip.ParseAddrPort(a); err == nil {
			out[ap.Addr().Unmap().String()] = true
		}
	}
	return out
}

// connectedIPs are the addresses of the peers the torrent is connected to now, without ports: a peer that
// connects to us does so from a port of its own.
func connectedIPs(t *torrent.Torrent) map[string][]*torrent.PeerConn {
	out := map[string][]*torrent.PeerConn{}
	for _, pc := range t.PeerConns() {
		host, _, err := net.SplitHostPort(pc.RemoteAddr.String())
		if err != nil {
			continue
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			k := ip.Unmap().String()
			out[k] = append(out[k], pc)
		}
	}
	return out
}

func (m *Manager) livePeers(hash string, t *torrent.Torrent, entries []manualPeer) map[string]bool {
	conns := connectedIPs(t)
	live := map[string]bool{}
	for _, e := range entries {
		for ip := range m.entryIPs(hash, e.Addr) {
			if len(conns[ip]) > 0 {
				live[e.Addr] = true
			}
		}
	}
	return live
}

// tryManualPeers looks the entries up and hands them to the engine. It may take a while (names), so callers run
// it apart from the control loop.
func (m *Manager) tryManualPeers(t *torrent.Torrent, hash string, keys []string) {
	ctx, cancel := context.WithTimeout(context.Background(), resolveDeadline)
	defer cancel()
	var infos []torrent.PeerInfo
	for _, k := range keys {
		_, addrs, reason := resolvePeerAddr(ctx, k)
		if reason != "" {
			continue
		}
		m.rememberPeerIPs(hash, k, addrs)
		for _, a := range addrs {
			infos = append(infos, torrent.PeerInfo{Addr: torrent.StringAddr(a), Source: torrent.PeerSourceDirect, Trusted: true})
		}
	}
	if len(infos) > 0 {
		t.AddPeers(infos)
	}
}

// reapplyManualPeers gives a restored torrent the peers the user added earlier.
func (m *Manager) reapplyManualPeers(t *torrent.Torrent, r record) {
	if len(r.ManualPeers) == 0 {
		return
	}
	keys := make([]string, len(r.ManualPeers))
	for i, p := range r.ManualPeers {
		keys[i] = p.Addr
	}
	go m.tryManualPeers(t, r.InfoHash, keys)
}

// serviceManualPeers runs from the control loop: it notes which saved peers are connected, and every few minutes
// tries again the ones that are not (the engine drops an address it could not reach).
func (m *Manager) serviceManualPeers(ts []*torrent.Torrent, recs map[string]record, tick int) {
	seen, retry := tick%manualSeenEvery == 0, tick%manualRetryEvery == 0
	if !seen && !retry {
		return
	}
	now := time.Now()
	for _, t := range ts {
		hash := t.InfoHash().HexString()
		r, ok := recs[hash]
		if !ok || len(r.ManualPeers) == 0 {
			continue
		}
		live := m.livePeers(hash, t, r.ManualPeers)
		if seen && len(live) > 0 {
			m.state.touch(func(s *state) {
				if rec := s.Torrents[hash]; rec != nil {
					for i := range rec.ManualPeers {
						if live[rec.ManualPeers[i].Addr] {
							rec.ManualPeers[i].LastSeen = now
						}
					}
				}
			})
		}
		if retry && !r.Paused {
			var keys []string
			for _, p := range r.ManualPeers {
				if !live[p.Addr] {
					keys = append(keys, p.Addr)
				}
			}
			if len(keys) > 0 {
				go m.tryManualPeers(t, hash, keys)
			}
		}
	}
}

// ManualPeers lists the peers saved for a torrent, with whether each is connected and whether it has gone stale.
func (m *Manager) ManualPeers(hash string) ([]ManualPeerInfo, error) {
	t, err := m.get(hash)
	if err != nil {
		return nil, err
	}
	rec, ok := m.record(hash)
	if !ok {
		return nil, ErrNotFound
	}
	live := m.livePeers(hash, t, rec.ManualPeers)
	now := time.Now()
	out := make([]ManualPeerInfo, 0, len(rec.ManualPeers))
	for _, p := range rec.ManualPeers {
		last := p.LastSeen
		if last.Before(p.Added) {
			last = p.Added
		}
		out = append(out, ManualPeerInfo{
			Addr: p.Addr, Added: p.Added, LastSeen: p.LastSeen,
			Connected: live[p.Addr], Stale: !live[p.Addr] && now.Sub(last) > manualPeerStale,
		})
	}
	return out, nil
}

// RemoveManualPeers forgets saved peers (by the addresses ManualPeers gave) and drops their connections.
func (m *Manager) RemoveManualPeers(hash string, addrs []string) (int, error) {
	t, err := m.get(hash)
	if err != nil {
		return 0, err
	}
	want := map[string]bool{}
	for _, a := range addrs {
		want[strings.ToLower(strings.TrimSpace(a))] = true
	}
	var gone []manualPeer
	if err := m.state.with(func(s *state) {
		r := s.Torrents[hash]
		if r == nil {
			return
		}
		kept := make([]manualPeer, 0, len(r.ManualPeers))
		for _, p := range r.ManualPeers {
			if want[strings.ToLower(p.Addr)] {
				gone = append(gone, p)
			} else {
				kept = append(kept, p)
			}
		}
		r.ManualPeers = kept
	}); err != nil {
		return 0, err
	}
	conns := connectedIPs(t)
	for _, p := range gone {
		for ip := range m.entryIPs(hash, p.Addr) {
			for _, pc := range conns[ip] {
				pc.Close()
			}
		}
	}
	m.manualMu.Lock()
	for _, p := range gone {
		delete(m.manualIPs, hash+"|"+p.Addr)
	}
	m.manualMu.Unlock()
	return len(gone), nil
}
