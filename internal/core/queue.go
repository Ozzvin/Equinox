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
