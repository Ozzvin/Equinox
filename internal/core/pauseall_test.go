package core

import "testing"

func TestSetAllPaused(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	h1, err := m.AddFile(makeTorrent(t, dir, "a.bin", 32<<10))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := m.AddFile(makeTorrent(t, dir, "b.bin", 32<<10))
	if err != nil {
		t.Fatal(err)
	}
	_ = m.SetPaused(h1, false)
	_ = m.SetPaused(h2, false)
	if n := m.SetAllPaused(true); n != 2 {
		t.Fatalf("paused %d, want 2", n)
	}
	for _, h := range []string{h1, h2} {
		if s, _ := statusOf(m, h); !s.Paused {
			t.Fatalf("%s must be paused", h)
		}
	}
	if n := m.SetAllPaused(true); n != 0 {
		t.Fatalf("pausing again changed %d torrents", n)
	}
	if n := m.SetAllPaused(false); n != 2 {
		t.Fatalf("resumed %d, want 2", n)
	}
	if s, _ := statusOf(m, h1); s.Paused {
		t.Fatal("must be running again")
	}
}
