package core

import (
	"testing"
	"time"
)

// Everything the manager keeps per torrent must be gone once the torrent is: flushActiveTime and List walk
// some of these maps on every refresh, so what stays behind after a removal only makes them slower.
func TestRemoveForgetsEverythingKeptPerTorrent(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "gone.bin", 20<<10), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.HasMeta })
	h, _ := parseHash(hash)

	m.mu.Lock()
	m.seenDown[h], m.seenUp[h] = 1, 1
	m.perm[h] = permState{}
	m.queued[h] = 1
	m.seedQueued[h] = 1
	m.seedHeld[h] = true
	m.seedPend[hash] = time.Second
	m.activePend[hash] = time.Second
	m.activeAt[h] = time.Now()
	m.moves[hash] = &moveJob{}
	m.phase[hash] = &checkPhase{}
	m.checkProg[hash] = 0.5
	m.errs[hash] = torrentError{}
	m.recheckQueue = append(m.recheckQueue, hash)
	m.mu.Unlock()
	m.lowMu.Lock()
	m.lowOn[h] = true
	m.lowMu.Unlock()

	if err := m.Remove(hash, false); err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for name, left := range map[string]bool{
		"torrents":     m.torrents[h] != nil,
		"seenDown":     hasKey(m.seenDown, h),
		"seenUp":       hasKey(m.seenUp, h),
		"perm":         hasKey(m.perm, h),
		"queued":       hasKey(m.queued, h),
		"seedQueued":   hasKey(m.seedQueued, h),
		"seedHeld":     hasKey(m.seedHeld, h),
		"seedPend":     hasKey(m.seedPend, hash),
		"activePend":   hasKey(m.activePend, hash),
		"activeAt":     hasKey(m.activeAt, h),
		"moves":        hasKey(m.moves, hash),
		"phase":        hasKey(m.phase, hash),
		"checkProg":    hasKey(m.checkProg, hash),
		"errs":         hasKey(m.errs, hash),
		"recheckQueue": len(dropString(m.recheckQueue, hash)) != len(m.recheckQueue),
	} {
		if left {
			t.Errorf("%s still holds the removed torrent", name)
		}
	}
	m.lowMu.Lock()
	defer m.lowMu.Unlock()
	if hasKey(m.lowOn, h) {
		t.Error("lowOn still holds the removed torrent")
	}
}

func hasKey[K comparable, V any](m map[K]V, k K) bool { _, ok := m[k]; return ok }
