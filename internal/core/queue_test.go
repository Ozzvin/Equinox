package core

import (
	"testing"
)

func queuedOf(m *Manager, hash string) int {
	for _, s := range m.List() {
		if s.Hash == hash {
			return s.Queued
		}
	}
	return -1
}

func TestQueueLimitsActiveDownloadsAndReorders(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	if err := m.SetMaxActiveDownloads(1); err != nil {
		t.Fatal(err)
	}
	first, err := m.AddFile(makeTorrent(t, dir, "one.bin", 40<<10))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.AddFile(makeTorrent(t, dir, "two.bin", 40<<10))
	if err != nil {
		t.Fatal(err)
	}

	// Nothing to download from, so both stay incomplete and compete for the single slot.
	waitFor(t, func() bool { return queuedOf(m, first) == 0 && queuedOf(m, second) == 1 })

	// The list is ordered by queue position.
	if l := m.List(); l[0].Hash != first || l[1].Hash != second {
		t.Fatalf("list must follow queue order: %s, %s", l[0].Hash, l[1].Hash)
	}

	if err := m.MoveInQueue(second, "top"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return queuedOf(m, second) == 0 && queuedOf(m, first) == 1 })
	if l := m.List(); l[0].Hash != second {
		t.Fatal("moved torrent must be listed first")
	}

	// Pausing the running one hands the slot over.
	if err := m.SetPaused(second, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return queuedOf(m, first) == 0 })

	// Lifting the limit releases everything.
	_ = m.SetPaused(second, false)
	if err := m.SetMaxActiveDownloads(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return queuedOf(m, first) == 0 && queuedOf(m, second) == 0 })

	if err := m.MoveInQueue(first, "sideways"); err == nil {
		t.Fatal("unknown move must be rejected")
	}
	if err := m.MoveInQueue("00000000000000000000000000000000000000ff", "up"); err == nil {
		t.Fatal("unknown torrent must be rejected")
	}
}

func TestLabels(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "lab.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetLabel(hash, "  Фильмы "); err != nil {
		t.Fatal(err)
	}
	if got := m.List()[0].Label; got != "Фильмы" {
		t.Fatalf("label not stored/trimmed: %q", got)
	}
	if err := m.SetLabel(hash, ""); err != nil || m.List()[0].Label != "" {
		t.Fatalf("empty label must clear it (err %v)", err)
	}
	long := make([]rune, MaxLabelLen+1)
	for i := range long {
		long[i] = 'я'
	}
	if err := m.SetLabel(hash, string(long)); err == nil {
		t.Fatal("over-long label must be rejected")
	}
	if err := m.SetLabel("00000000000000000000000000000000000000ff", "x"); err == nil {
		t.Fatal("unknown torrent must be rejected")
	}
}
