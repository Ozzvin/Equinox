package core

import (
	"testing"
	"time"
)

// QueueExternalAdd/TakePendingAdds is the hand-off from an "Open with" file or a second launch to
// the web page's "Add torrents" dialog: nothing gets added until the page consumes the queue.
func TestPendingAddQueueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)

	if got := m.TakePendingAdds(true); len(got) != 0 {
		t.Fatalf("a new manager must start with nothing pending: %+v", got)
	}

	mi := loadMI(t, makeTorrent(t, dir, "pending.bin", 4<<10))
	st, err := m.Stage(mi, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.QueueExternalAdd(PendingAdd{Kind: "stage", Stage: st.ID})
	m.QueueExternalAdd(PendingAdd{Kind: "magnet", Magnet: "magnet:?xt=urn:btih:" + "a"})

	got := m.TakePendingAdds(true)
	if len(got) != 2 || got[0].Stage != st.ID || got[1].Magnet == "" {
		t.Fatalf("queued items not returned in order: %+v", got)
	}
	if len(m.pending) != 0 {
		t.Fatal("TakePendingAdds must clear the queue")
	}
	if got := m.TakePendingAdds(true); len(got) != 0 {
		t.Fatalf("taking twice must not repeat items: %+v", got)
	}

	// The staged file itself must not have been consumed or removed by queueing it.
	if again, ok := m.StagedInfo(st.ID); !ok || again.Hash != st.Hash {
		t.Fatalf("staged torrent lost after queueing: %+v ok=%v", again, ok)
	}
}

// StagedInfo lets a second call recover the same details a stage.Stage call already returned,
// without consuming or duplicating the entry, and reports unknown ids honestly.
func TestStagedInfoRepeatsWithoutConsuming(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	mi := loadMI(t, makeTorrent(t, dir, "again.bin", 4<<10))
	st, err := m.Stage(mi, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, ok := m.StagedInfo(st.ID)
		if !ok || got.Name != st.Name || got.Hash != st.Hash || len(got.Files) != len(st.Files) {
			t.Fatalf("round %d: %+v ok=%v, want %+v", i, got, ok, st)
		}
	}
	if _, ok := m.StagedInfo("nope"); ok {
		t.Fatal("an unknown id must report not found")
	}
	m.Unstage(st.ID)
	if _, ok := m.StagedInfo(st.ID); ok {
		t.Fatal("StagedInfo must not find an unstaged entry")
	}
}

// The main window does not get what the window for adding is about to take, only what has been left waiting.
func TestMainWindowOnlyGetsWhatHasWaited(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	m.QueueExternalAdd(PendingAdd{Kind: "magnet", Magnet: "magnet:?xt=urn:btih:" + "b"})
	if got := m.TakePendingAdds(false); len(got) != 0 {
		t.Fatalf("a fresh item belongs to the window for adding: %v", got)
	}
	m.mu.Lock()
	m.pending[0].at = time.Now().Add(-2 * PendingGrace) // nobody came for it
	m.mu.Unlock()
	if got := m.TakePendingAdds(false); len(got) != 1 {
		t.Fatalf("an item nobody took must go to the main window: %v", got)
	}
	m.QueueExternalAdd(PendingAdd{Kind: "magnet", Magnet: "magnet:?xt=urn:btih:" + "c"})
	if got := m.TakePendingAdds(true); len(got) != 1 {
		t.Fatalf("the window for adding takes everything: %v", got)
	}
}
