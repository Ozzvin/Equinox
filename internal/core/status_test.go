package core

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
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
	if err := m.SetMoveDone(hash, true, target); err != nil {
		t.Fatal(err)
	}
	d, _ = m.Details(hash)
	if !d.EdgePieces || d.MoveDone != target || d.MoveDoneOff {
		t.Fatalf("options not stored: %+v", d)
	}
	if err := m.SetMoveDone(hash, true, ""); err != nil {
		t.Fatal(err)
	}
	if d, _ = m.Details(hash); d.MoveDone != "" || d.MoveDoneOff {
		t.Fatalf("an empty path must go back to the global setting: %q", d.MoveDone)
	}
	if err := m.SetMoveDone(hash, false, ""); err != nil {
		t.Fatal(err)
	}
	if d, _ = m.Details(hash); !d.MoveDoneOff {
		t.Fatalf("disabling the checkbox must mean never move this torrent")
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

// A connected peer with every piece is both a seed (LastSeedSeen) and, trivially, makes the whole torrent
// available somewhere among the connected peers (LastFullAvailable), even though the local copy is still
// empty. Both must be remembered once seen, and must stay set afterwards even if that peer then leaves.
func TestTrackAvailabilityRemembersASeedThatWasSeen(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a := newManager(t, dirA, nil)
	tp := makeTorrent(t, dirA, "avail.bin", 200<<10)
	seed(t, a, dirA, "avail.bin")
	hashA, err := a.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(a, hashA); return ok && s.Progress == 1 })

	// Capped low on purpose: a 200 KiB transfer over loopback would otherwise finish inside a single tick of
	// the loop that does the tracking under test, before it gets a chance to see the torrent while it is
	// still incomplete and connected to a's peer (see trackAvailability's "already complete" skip). The full
	// (not just per-peer) availability check only runs every availabilityCheckEvery-th tick, so the transfer
	// needs to still be running at tick 15, not just any tick: 8 KiB/s keeps ~120 KiB (of 200) still to go at
	// the 15s mark, finishing around 25s, comfortably inside waitFor's 30s budget.
	b := newManager(t, dirB, func(s *config.Settings) { s.DownLimitKBps = 8 })
	hashB, err := b.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	if x, _ := b.StatusExtra(hashB); !x.LastSeedSeen.IsZero() || !x.LastFullAvailable.IsZero() {
		t.Fatalf("nothing seen yet: %+v", x)
	}

	addr := "127.0.0.1:" + strconv.Itoa(a.Port())
	if _, err := b.AddPeers(hashB, []string{addr}); err != nil {
		t.Fatal(err)
	}
	// As in TestAddedPeerIsUsed: the engine can drop an address it could not reach at the first try, so keep
	// offering it until the connection (and with it, the fields under test) actually takes.
	waitFor(t, func() bool {
		rec := b.snapshotRecords()[hashB]
		if s, ok := statusOf(b, hashB); ok && s.Progress < 1 && s.Peers == 0 {
			_, _ = b.AddPeers(hashB, []string{addr})
		}
		return !rec.LastSeedSeen.IsZero() && !rec.LastFullAvailable.IsZero()
	})
	x, err := b.StatusExtra(hashB)
	if err != nil || x.LastSeedSeen.IsZero() || x.LastFullAvailable.IsZero() {
		t.Fatalf("status extras did not pick up the record: %+v %v", x, err)
	}

	// once it finishes, an already-complete torrent is skipped (nothing left to learn), and what was already
	// remembered must not be wiped by that.
	waitFor(t, func() bool { s, ok := statusOf(b, hashB); return ok && s.Progress == 1 })
	time.Sleep(200 * time.Millisecond) // let a couple more ticks pass, now that it is complete
	if x, _ = b.StatusExtra(hashB); x.LastSeedSeen.IsZero() || x.LastFullAvailable.IsZero() {
		t.Fatalf("finishing must not clear what was already seen: %+v", x)
	}
}
