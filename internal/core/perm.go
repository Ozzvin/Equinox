package core

import (
	"github.com/anacrolix/torrent"
)

// Several things decide whether a torrent may download or upload right now: the user's
// pause, the download queue and a failure that stopped it. They all end in the same two
// engine switches, so the decision lives here, in one place, and the engine is only
// touched when the outcome changes. (Letting each of them flip the switches on its own
// would make them undo one another.)

type permState struct{ down, up bool }

func (m *Manager) record(hash string) (rec record, ok bool) {
	m.state.view(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			rec, ok = *r, true
		}
	})
	return
}

// syncPerm recomputes what the torrent may do and applies any change to the engine.
func (m *Manager) syncPerm(t *torrent.Torrent, hash string) {
	rec, ok := m.record(hash)
	if !ok {
		return
	}
	h := t.InfoHash()
	m.mu.Lock()
	prev, known := m.perm[h]
	if !known {
		prev = permState{true, true} // what a freshly added torrent may do
	}
	want := permState{
		down: !rec.Paused && m.queued[h] == 0,
		up:   !rec.Paused && m.seedQueued[h] == 0,
	}
	m.perm[h] = want
	m.mu.Unlock()

	if want.down != prev.down {
		if want.down {
			t.AllowDataDownload()
		} else {
			t.DisallowDataDownload()
		}
	}
	if want.up != prev.up {
		if want.up {
			t.AllowDataUpload()
		} else {
			t.DisallowDataUpload()
		}
	}
}

// A per-torrent SPEED limit was tried and removed. The engine has one rate limiter for the
// whole client. Enforcing a torrent's limit from outside (a token bucket that switches
// downloading on and off) held the average only when the file was much bigger than what the
// peers have in flight: after each "allow" the engine asks for everything still missing at
// once, so a 12 MiB file with a 1 MiB/s limit finished at 3 MiB/s in some runs. An upload
// limit built the same way was worse, because the engine does not wake its writer when
// uploading is allowed again. Speed is limited for the whole client (and by the turtle mode).
