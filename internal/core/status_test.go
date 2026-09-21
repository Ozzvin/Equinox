package core

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTorrentOptionsAndStatusExtras(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddMetaInfo(namedTwoFileTorrent(t, dir, "optpack"), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { l := m.List(); return len(l) == 1 && l[0].HasMeta })

	d, err := m.Details(hash)
	if err != nil || d.EdgePieces || d.MoveDone != "" || d.Sequential {
		t.Fatalf("a new torrent has no options set: %+v %v", d, err)
	}
	if err := m.SetEdgePieces(hash, true); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "done-here")
	if err := m.SetMoveDone(hash, target); err != nil {
		t.Fatal(err)
	}
	d, _ = m.Details(hash)
	if !d.EdgePieces || d.MoveDone != target {
		t.Fatalf("options not stored: %+v", d)
	}
	if err := m.SetMoveDone(hash, ""); err != nil {
		t.Fatal(err)
	}
	if d, _ = m.Details(hash); d.MoveDone != "" {
		t.Fatalf("an empty path must go back to the global setting: %q", d.MoveDone)
	}
	if err := m.SetEdgePieces("zz", true); err == nil {
		t.Fatal("an unknown torrent must be rejected")
	}

	// running time is counted and kept
	m.addActiveTime(hash, 2500*time.Millisecond)
	x, err := m.StatusExtra(hash)
	if err != nil || x.ActiveSeconds != 2 || x.LastTransfer != -1 {
		t.Fatalf("status extras: %+v %v", x, err)
	}
	m.flushActiveTime()
	if x, _ = m.StatusExtra(hash); x.ActiveSeconds != 2 {
		t.Fatalf("flushing must not change the total: %+v", x)
	}
	if x.Availability < 0 {
		t.Fatalf("availability: %v", x.Availability)
	}
}
