package core

import (
	"errors"
	"testing"
)

func permOf(m *Manager, hash string) permState {
	h, _ := parseHash(hash)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.perm[h]; ok {
		return p
	}
	return permState{true, true} // never told anything: a fresh torrent may do everything
}

// Pause, the download queue and a failure all decide the same two switches. They must
// combine, not overwrite each other.
func TestPauseQueueAndFailureCombine(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	if err := m.SetMaxActiveDownloads(1); err != nil {
		t.Fatal(err)
	}
	first, _ := m.AddFile(makeTorrent(t, dir, "p1.bin", 20<<10))
	second, _ := m.AddFile(makeTorrent(t, dir, "p2.bin", 20<<10))

	// One slot: the second waits. Waiting stops downloading only; a torrent can still upload.
	waitFor(t, func() bool { return queuedOf(m, second) == 1 })
	waitFor(t, func() bool { p := permOf(m, second); return !p.down && p.up })
	if p := permOf(m, first); !p.down || !p.up {
		t.Fatalf("the torrent in the slot may do everything: %+v", p)
	}

	// Pausing the first frees the slot, and the second starts.
	_ = m.SetPaused(first, true)
	waitFor(t, func() bool { return queuedOf(m, second) == 0 && permOf(m, second).down })
	if p := permOf(m, first); p.down || p.up {
		t.Fatalf("a paused torrent may do nothing: %+v", p)
	}

	// Resuming the first puts it back in front: it runs again and the second waits again.
	_ = m.SetPaused(first, false)
	waitFor(t, func() bool { return queuedOf(m, second) == 1 && !permOf(m, second).down })
	if p := permOf(m, first); !p.down || !p.up {
		t.Fatalf("resumed torrent must be allowed again: %+v", p)
	}

	// A paused torrent that is also waiting stays stopped when the queue lets go of it.
	_ = m.SetPaused(second, true)
	if err := m.SetMaxActiveDownloads(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return queuedOf(m, second) == 0 })
	if p := permOf(m, second); p.down || p.up {
		t.Fatalf("lifting the queue limit must not wake a paused torrent: %+v", p)
	}

	// A failure pauses; resuming is the retry and lets the torrent work again.
	tor, _ := m.get(first)
	m.onWriteError(tor, first, errors.New("boom"))
	if p := permOf(m, first); p.down || p.up {
		t.Fatalf("a failed torrent is stopped: %+v", p)
	}
	_ = m.SetPaused(first, false)
	if p := permOf(m, first); !p.down || !p.up {
		t.Fatalf("resuming after a failure must allow it again: %+v", p)
	}
}

func TestEngineIsOnlyToldAboutChanges(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, _ := m.AddFile(makeTorrent(t, dir, "chg.bin", 20<<10))
	tor, _ := m.get(hash)

	m.syncPerm(tor, hash) // nothing to change: a fresh torrent is already allowed
	if p := permOf(m, hash); !p.down || !p.up {
		t.Fatalf("unexpected: %+v", p)
	}
	_ = m.SetPaused(hash, true)
	before := permOf(m, hash)
	m.syncPerm(tor, hash)
	m.syncPerm(tor, hash)
	if permOf(m, hash) != before {
		t.Fatal("repeated syncs must not change the outcome")
	}
}
