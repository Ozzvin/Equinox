package core

import (
	"testing"
	"time"
)

// A torrent staged and forgotten leaves memory on the timer, not only when the next one is staged.
func TestSweepStagedDropsOnlyTheOldOnes(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	m.mu.Lock()
	m.staged["old"] = &stagedEntry{at: time.Now().Add(-2 * stagedTTL), bytes: 10}
	m.staged["new"] = &stagedEntry{at: time.Now(), bytes: 20}
	m.mu.Unlock()

	m.sweepStaged()

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.staged["old"]; ok {
		t.Error("an expired staged torrent stayed")
	}
	if _, ok := m.staged["new"]; !ok {
		t.Error("a fresh staged torrent was dropped")
	}
}
