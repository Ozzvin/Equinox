package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A recheck stopped half way keeps its place in checks.json; going on from that place finishes the check and
// forgets the place. While a check runs, the torrent does not download.
func TestRecheckKeepsItsPlaceAndHoldsTheDownload(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "data.bin", 64*16<<10)) // 64 pieces
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.HasMeta && !s.Checking })
	tor, err := m.get(hash)
	if err != nil {
		t.Fatal(err)
	}

	// stopped at once (the program closing): the place is kept
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.verifyFrom(ctx, tor, hash, 10); err == nil {
		t.Fatal("a stopped check must say so")
	}
	if pos, ok := m.loadCheckPlaces()[hash]; !ok || pos != 10 {
		t.Fatalf("the place must be kept, got %d (%v)", pos, ok)
	}

	// going on from there: the progress is the share of the bytes checked, and the place is forgotten at the end
	if err := m.verifyFrom(context.Background(), tor, hash, 10); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	pos, prog := m.checkPos[hash], m.checkProg[hash]
	m.mu.Unlock()
	if pos != 64 || prog != 1 {
		t.Fatalf("after the check: place %d, progress %v", pos, prog)
	}
	if _, err := os.Stat(m.checksPath()); !os.IsNotExist(err) {
		t.Fatalf("checks.json must be gone once no check is under way: %v", err)
	}

	// a check holds the download
	m.mu.Lock()
	delete(m.checkPos, hash)
	delete(m.checkProg, hash)
	m.checking[hash] = true
	m.mu.Unlock()
	m.syncPerm(tor, hash)
	m.mu.Lock()
	held := !m.perm[tor.InfoHash()].down
	delete(m.checking, hash)
	m.mu.Unlock()
	if !held {
		t.Fatal("no downloading while the files are checked")
	}
	m.syncPerm(tor, hash)
	m.mu.Lock()
	back := m.perm[tor.InfoHash()].down
	m.mu.Unlock()
	if !back {
		t.Fatal("downloading must go on once the check is over")
	}
}

// The rechecks of the last run go on at the next start.
func TestRecheckGoesOnAfterARestart(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "data.bin", 32*16<<10), WithSavePath(filepath.Join(dir, "src"))) // the data is there
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 && !s.Checking })
	m.saveCheckPlace(hash, 20) // as if the program had closed in the middle of a recheck
	m.resumeChecks()
	waitFor(t, func() bool {
		_, err := os.Stat(m.checksPath())
		return os.IsNotExist(err)
	})
	if s, _ := statusOf(m, hash); s.Progress != 1 {
		t.Fatalf("the torrent must still be complete after the check: %v", s.Progress)
	}
}
