package core

import (
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

// A torrent added into an empty destination has nothing to verify, so it must skip the hash check
// (and the check queue behind it) instead of waiting for its turn like a torrent that genuinely
// needs checking.
func TestFreshTorrentSkipsTheCheckQueue(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 1 })

	// existing.bin already sits on disk, complete: it needs a real check and takes the one slot.
	existingPath := makeTorrent(t, dir, "existing.bin", 64<<20)
	putOnDisk(t, dir, "existing.bin")
	existingHash, err := m.AddFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	existingTor, err := m.get(existingHash)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return existingTor.Info() != nil })
	if c, _ := checkingPieces(existingTor); c == 0 {
		t.Skip("the check of the existing file was already over")
	}

	// fresh.bin has nothing on disk. With the one slot taken, it would normally wait in line; instead
	// it must skip the gate entirely and start downloading right away.
	freshPath := makeTorrent(t, dir, "fresh.bin", 64<<20)
	freshHash, err := m.AddFile(freshPath)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var s Status
	for time.Now().Before(deadline) {
		var ok bool
		if s, ok = statusOf(m, freshHash); ok && s.HasMeta {
			break
		}
	}
	if !s.HasMeta {
		t.Fatal("the fresh torrent never got its metadata")
	}
	if s.CheckQueued != 0 {
		t.Fatalf("a fresh torrent must not wait in the check queue, got position %d", s.CheckQueued)
	}
	freshTor, err := m.get(freshHash)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := checkingPieces(freshTor); c != 0 {
		t.Fatalf("a fresh torrent must not be hashed: %d pieces checking", c)
	}
}

// A torrent whose destination already has a full-size file must still be checked normally: the
// skip only applies when there is nothing to verify.
func TestExistingFileIsStillChecked(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	path := makeTorrent(t, dir, "there.bin", 4<<20)
	putOnDisk(t, dir, "there.bin")
	hash, err := m.AddFile(path)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })
}
