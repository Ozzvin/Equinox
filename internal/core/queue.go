package core

import (
	"fmt"
	"sort"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/config"
)

// orderLess sorts records by queue position; older records (which have no explicit
// position) fall back to the time they were added.
func orderLess(a, b *record) bool {
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	return a.Added.Before(b.Added)
}

// newOrder gives a new torrent the last place in the queue.
func newOrder() int64 { return time.Now().UnixNano() }

// MoveInQueue changes the queue position of a torrent: "top", "up", "down" or "bottom".
func (m *Manager) MoveInQueue(hash, dir string) error {
	if _, err := m.get(hash); err != nil {
		return err
	}
	switch dir {
	case "top", "up", "down", "bottom":
	default:
		return fmt.Errorf("%w: unknown move %q", ErrInvalidInput, dir)
	}
	return m.state.with(func(s *state) {
		all := make([]*record, 0, len(s.Torrents))
		for _, r := range s.Torrents {
			all = append(all, r)
		}
		sort.Slice(all, func(i, j int) bool { return orderLess(all[i], all[j]) })
		for i, r := range all { // renumber so that positions are unique and dense
			r.Order = int64(i+1) * 10
		}
		at := -1
		for i, r := range all {
			if r.InfoHash == hash {
				at = i
			}
		}
		if at < 0 {
			return
		}
		switch dir {
		case "top":
			all[at].Order = 0
		case "bottom":
			all[at].Order = int64(len(all)+1) * 10
		case "up":
			if at > 0 {
				all[at].Order, all[at-1].Order = all[at-1].Order, all[at].Order
			}
		case "down":
			if at < len(all)-1 {
				all[at].Order, all[at+1].Order = all[at+1].Order, all[at].Order
			}
		}
	})
}

// SetMaxActiveDownloads limits how many incomplete torrents download at the same time
// (0 = no limit). The rest wait in the queue, in their queue order.
func (m *Manager) SetMaxActiveDownloads(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: negative limit", ErrInvalidInput)
	}
	return m.cfg.Update(func(s *config.Settings) { s.MaxActiveDownloads = n })
}

// applyQueue lets the first N downloading torrents run and holds back the others. It
// only touches the engine when a torrent's state changes.
func (m *Manager) applyQueue(ts []*torrent.Torrent, recs map[string]record) {
	limit := m.cfg.Get().MaxActiveDownloads

	type cand struct {
		t *torrent.Torrent
		r *record
	}
	var cands []cand
	for _, t := range ts {
		r, ok := recs[t.InfoHash().HexString()]
		if !ok || r.Paused || t.Info() == nil {
			continue
		}
		if size, done := selection(t, r.FilePrios); done >= size {
			continue // finished: seeding does not use a download slot
		}
		rc := r
		cands = append(cands, cand{t, &rc})
	}
	sort.Slice(cands, func(i, j int) bool { return orderLess(cands[i].r, cands[j].r) })

	queued := map[metainfo.Hash]int{}
	for i, c := range cands {
		if limit > 0 && i >= limit {
			queued[c.t.InfoHash()] = i - limit + 1 // 1 = next in line
		}
	}
	byHash := map[metainfo.Hash]*torrent.Torrent{}
	for _, t := range ts {
		byHash[t.InfoHash()] = t
	}

	m.mu.Lock()
	prev := m.queued
	m.queued = queued
	m.mu.Unlock()

	// Only torrents whose waiting state changed need the engine told (see syncPerm).
	for h := range queued {
		if prev[h] == 0 {
			m.syncPerm(byHash[h], h.HexString())
		}
	}
	for h := range prev {
		if queued[h] == 0 && byHash[h] != nil {
			m.syncPerm(byHash[h], h.HexString())
		}
	}
}

// SetMaxActiveSeeds limits how many finished torrents share at the same time (0 = no limit). The rest wait
// for a place; see applySeedQueue for who gets one.
func (m *Manager) SetMaxActiveSeeds(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: negative limit", ErrInvalidInput)
	}
	return m.cfg.Update(func(s *config.Settings) { s.MaxActiveSeeds = n })
}

// seedCand is a finished torrent that could share.
type seedCand struct {
	hash   metainfo.Hash
	rec    *record
	demand bool // someone is receiving from it, or connected and still missing pieces
	held   bool // it had a place at the last look
}

// rankSeeds picks who shares when there are more finished torrents than places, and returns the waiting
// ones with their place in the line (1 = next). Those with demand come first, so a place is not spent on
// a torrent nobody wants anything from while another one has peers waiting; among equals the ones that
// already have a place keep it (so that places do not change hands back and forth), and then the queue
// order decides. A limit of 0 means no limit.
func rankSeeds(cands []seedCand, limit int) map[metainfo.Hash]int {
	waiting := map[metainfo.Hash]int{}
	if limit <= 0 || len(cands) <= limit {
		return waiting
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.demand != b.demand {
			return a.demand
		}
		if a.held != b.held {
			return a.held
		}
		return orderLess(a.rec, b.rec)
	})
	for i, c := range cands[limit:] {
		waiting[c.hash] = i + 1
	}
	return waiting
}

// applySeedQueue gives the places to share out, and tells the engine about any torrent whose turn came or
// went. A torrent without a place may not upload (see syncPerm) but stays connected, so that its peers can
// still be counted as demand and it can take a place as soon as one is free.
func (m *Manager) applySeedQueue(ts []*torrent.Torrent, recs map[string]record) {
	limit := m.cfg.Get().MaxActiveSeeds

	m.mu.Lock()
	prevHeld := m.seedHeld
	m.mu.Unlock()

	var cands []seedCand
	byHash := map[metainfo.Hash]*torrent.Torrent{}
	if limit > 0 {
		for _, t := range ts {
			h := t.InfoHash()
			byHash[h] = t
			r, ok := recs[h.HexString()]
			if !ok || r.Paused || t.Info() == nil {
				continue
			}
			if size, done := selection(t, r.FilePrios); size == 0 || done < size {
				continue // not finished: it is downloading, and downloading torrents share without a place
			}
			st := t.Stats()
			m.mu.Lock()
			uploading := m.rates[h][1] > 0 || time.Since(m.activeAt[h]) < activeHold
			m.mu.Unlock()
			rc := r
			cands = append(cands, seedCand{
				hash: h, rec: &rc, held: prevHeld[h],
				demand: uploading || st.ActivePeers > st.ConnectedSeeders,
			})
		}
	} else {
		for _, t := range ts {
			byHash[t.InfoHash()] = t
		}
	}
	waiting := rankSeeds(cands, limit)
	held := map[metainfo.Hash]bool{}
	for _, c := range cands {
		if waiting[c.hash] == 0 {
			held[c.hash] = true
		}
	}

	m.mu.Lock()
	prev := m.seedQueued
	m.seedQueued, m.seedHeld = waiting, held
	m.mu.Unlock()

	for h := range waiting {
		if prev[h] == 0 {
			m.syncPerm(byHash[h], h.HexString())
		}
	}
	for h := range prev {
		if waiting[h] == 0 && byHash[h] != nil {
			m.syncPerm(byHash[h], h.HexString())
		}
	}
}
