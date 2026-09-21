package core

import (
	"fmt"
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

// addOnDisk adds n torrents whose files are already in the download folder, so each needs a check.
func addOnDisk(t *testing.T, m *Manager, dir string, n int) []string {
	t.Helper()
	var hashes []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("g%d.bin", i)
		tp := makeTorrent(t, dir, name, 40<<10)
		putOnDisk(t, dir, name)
		h, err := m.AddFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	return hashes
}

func waiting(m *Manager) (n int) {
	for _, s := range m.List() {
		if s.CheckQueued > 0 {
			n++
		}
	}
	return
}

func TestChecksWaitForAFreeSlot(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 1 })
	hashes := addOnDisk(t, m, dir, 3)

	// Right after adding, the ones beyond the limit wait, and say their place in the line.
	if w := waiting(m); w < 1 {
		t.Fatalf("with one slot the others must wait, %d waiting", w)
	}
	for _, s := range m.List() {
		if s.CheckQueued > 0 && (s.Size == 0 || s.HasMeta) {
			t.Fatalf("a waiting torrent shows its size and has no info yet: %+v", s)
		}
	}
	// They all get their turn and finish.
	waitFor(t, func() bool {
		for _, h := range hashes {
			if s, ok := statusOf(m, h); !ok || s.Progress != 1 || s.CheckQueued != 0 {
				return false
			}
		}
		return true
	})
	if waiting(m) != 0 {
		t.Fatal("nobody may be left waiting")
	}
}

func TestNoLimitMeansNoWaiting(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 0 })
	hashes := addOnDisk(t, m, dir, 3)
	if w := waiting(m); w != 0 {
		t.Fatalf("without a limit nothing waits, %d waiting", w)
	}
	waitFor(t, func() bool {
		for _, h := range hashes {
			if s, ok := statusOf(m, h); !ok || s.Progress != 1 {
				return false
			}
		}
		return true
	})
}

func TestRaisingTheLimitReleasesTheLine(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 1 })
	addOnDisk(t, m, dir, 4)
	if waiting(m) < 2 {
		t.Skip("the loop released them before the check")
	}
	if err := m.cfg.Update(func(s *config.Settings) { s.MaxConcurrentChecks = 0 }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return waiting(m) == 0 })
}

func TestRemovingAWaitingTorrentLeavesTheLine(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 1 })
	hashes := addOnDisk(t, m, dir, 3)
	last := hashes[len(hashes)-1]
	if s, _ := statusOf(m, last); s.CheckQueued == 0 {
		t.Skip("the loop released it before the removal")
	}
	if err := m.Remove(last, false); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, held := m.held[last]
	m.mu.Unlock()
	if held {
		t.Fatal("a removed torrent must not stay in the line")
	}
	waitFor(t, func() bool { return waiting(m) == 0 })
}

func TestAddPausedSetting(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.Add.Paused = true; s.MaxConcurrentChecks = 0 })
	h1, err := m.AddFile(makeTorrent(t, dir, "p1.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := statusOf(m, h1); !s.Paused {
		t.Fatal("a torrent added without a say starts paused when the setting asks for it")
	}
	// The dialog always says; its choice wins.
	h2, err := m.AddFile(makeTorrent(t, dir, "p2.bin", 20<<10), WithOptions(TorrentOptions{Paused: false}))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := statusOf(m, h2); s.Paused {
		t.Fatal("an explicit choice not to pause must be kept")
	}
	if err := m.cfg.Update(func(s *config.Settings) { s.Add.Paused = false }); err != nil {
		t.Fatal(err)
	}
	h3, err := m.AddFile(makeTorrent(t, dir, "p3.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := statusOf(m, h3); s.Paused {
		t.Fatal("without the setting nothing starts paused")
	}
}

// Checks go from the top of the queue down, and moving a torrent in the queue moves it in the line.
func TestCheckLineFollowsTheQueueOrder(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 1 })
	hashes := addOnDisk(t, m, dir, 5) // the first takes the slot; the other four wait, oldest first
	line := m.checkLine()
	if len(line) < 3 {
		t.Skip("the loop released some before the check")
	}
	for i := 1; i < len(line); i++ {
		if line[i] == hashes[0] {
			t.Fatal("the torrent in the slot is not in the line")
		}
	}
	for i := 0; i+1 < len(line); i++ {
		var a, b int
		for k, h := range hashes {
			if h == line[i] {
				a = k
			}
			if h == line[i+1] {
				b = k
			}
		}
		if a > b {
			t.Fatalf("the line must follow the order of adding: %v", line)
		}
	}
	last := line[len(line)-1]
	if err := m.MoveInQueue(last, "top"); err != nil {
		t.Fatal(err)
	}
	if got := m.checkLine(); len(got) == len(line) && got[0] != last {
		t.Fatalf("a torrent moved to the top must be first in the line: %v (wanted %s first)", got, last[:6])
	}
	if pos, _ := m.checkQueuePos(last); pos > 1 {
		t.Fatalf("its place in the line: %d", pos)
	}
}
