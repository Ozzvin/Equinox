package core

import (
	"fmt"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/config"
)

func seedCands(n int) []seedCand {
	var out []seedCand
	for i := 0; i < n; i++ {
		var h metainfo.Hash
		h[0] = byte(i + 1)
		out = append(out, seedCand{hash: h, rec: &record{Order: int64(i+1) * 10, Added: time.Unix(int64(i), 0)}})
	}
	return out
}

func waitingNames(c []seedCand, w map[metainfo.Hash]int) string {
	got := ""
	for _, x := range c {
		if w[x.hash] > 0 {
			got += fmt.Sprintf("%d:%d ", x.hash[0], w[x.hash])
		}
	}
	return got
}

func TestRankSeeds(t *testing.T) {
	// No limit, or room for everybody: nobody waits.
	if w := rankSeeds(seedCands(4), 0); len(w) != 0 {
		t.Fatalf("no limit: %v", w)
	}
	if w := rankSeeds(seedCands(3), 3); len(w) != 0 {
		t.Fatalf("as many places as torrents: %v", w)
	}

	// Equal in every way: the queue order decides; the ones after the limit wait, numbered from 1.
	c := seedCands(4)
	w := rankCopy(c, 2)
	if got := waitingNames(c, w); got != "3:1 4:2 " {
		t.Fatalf("by queue order, waiting = %q", got)
	}

	// Someone is waiting for the last one: it takes a place over the ones with nobody to give to.
	c = seedCands(4)
	c[3].demand = true
	w = rankCopy(c, 2)
	if w[c[3].hash] != 0 || w[c[0].hash] != 0 || w[c[1].hash] == 0 || w[c[2].hash] == 0 {
		t.Fatalf("demand must win, waiting = %q", waitingNames(c, w))
	}

	// Among equals, the one that already has a place keeps it over an earlier one in the queue.
	c = seedCands(3)
	c[2].held = true
	w = rankCopy(c, 1)
	if w[c[2].hash] != 0 {
		t.Fatalf("a holder keeps its place among equals, waiting = %q", waitingNames(c, w))
	}
	// ... but demand beats holding: an idle holder gives way to a torrent with peers.
	c = seedCands(3)
	c[0].held = true
	c[2].demand = true
	w = rankCopy(c, 1)
	if w[c[2].hash] != 0 || w[c[0].hash] == 0 {
		t.Fatalf("an idle holder gives way to demand, waiting = %q", waitingNames(c, w))
	}
	// Two with demand: the holder stays (no places changing hands back and forth).
	c = seedCands(3)
	c[0].demand, c[2].demand, c[2].held = true, true, true
	w = rankCopy(c, 1)
	if w[c[2].hash] != 0 {
		t.Fatalf("with equal demand the holder stays, waiting = %q", waitingNames(c, w))
	}
}

// With a limit of one place, of three finished torrents one shares and the other two wait in line, showing
// their place; taking the limit off lets all of them share.
func TestSeedLimitQueuesTheRest(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	var hashes []string
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("s%d.bin", i)
		tp := makeTorrent(t, dir, name, 32<<10)
		seed(t, m, dir, name)
		h, err := m.AddFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
		waitFor(t, func() bool { s, ok := statusOf(m, h); return ok && s.Progress == 1 })
	}

	if err := m.SetMaxActiveSeeds(1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		sharing, places := 0, map[int]bool{}
		for _, h := range hashes {
			s, _ := statusOf(m, h)
			if s.SeedQueued == 0 {
				sharing++
			} else {
				places[s.SeedQueued] = true
			}
		}
		return sharing == 1 && places[1] && places[2]
	})

	if err := m.SetMaxActiveSeeds(0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, h := range hashes {
			if s, _ := statusOf(m, h); s.SeedQueued != 0 {
				return false
			}
		}
		return true
	})

	if err := m.SetMaxActiveSeeds(-1); err == nil {
		t.Fatal("a negative limit must be refused")
	}
}

// rankSeeds reorders its argument, so the tests keep their own order to look the candidates up by.
func rankCopy(c []seedCand, limit int) map[metainfo.Hash]int {
	return rankSeeds(append([]seedCand(nil), c...), limit)
}

// Remove deletes from the seed maps under m.mu while the loop is inside applySeedQueue; the loop must not read
// a map that is published to the manager without the lock (the race detector, and in a bad moment a
// "concurrent map read and map write" crash, catch it).
func TestApplySeedQueueDoesNotReadPublishedMapsUnlocked(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxActiveSeeds = 1 })
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("r%d.bin", i)
		tp := makeTorrent(t, dir, name, 32<<10)
		seed(t, m, dir, name)
		h, err := m.AddFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { s, ok := statusOf(m, h); return ok && s.Progress == 1 })
	}
	m.cancel() // no loop of its own: only this test drives applySeedQueue
	<-m.done

	var h metainfo.Hash
	h[0] = 9
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			m.mu.Lock()
			for k := range m.seedQueued {
				m.seedQueued[k] = 1 + int(k[0])%2
			}
			m.seedQueued[h] = 1
			delete(m.seedQueued, h)
			for k := range m.seedHeld {
				delete(m.seedHeld, k)
			}
			m.mu.Unlock()
		}
	}()
	ts := m.cl.Torrents()
	for i := 0; i < 3000; i++ {
		m.applySeedQueue(ts, m.snapshotRecords())
	}
	close(stop)
	<-done
}

// The same for the download queue: applyQueue reads the map it has just published while Remove deletes from it.
func TestApplyQueueDoesNotReadPublishedMapsUnlocked(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxActiveDownloads = 1 })
	for i := 0; i < 3; i++ {
		if _, err := m.AddFile(makeTorrent(t, dir, fmt.Sprintf("q%d.bin", i), 32<<10)); err != nil {
			t.Fatal(err)
		}
	}
	m.cancel()
	<-m.done

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			m.mu.Lock()
			for k := range m.queued {
				m.queued[k] = 1 + int(k[0])%2
				delete(m.queued, k)
			}
			m.mu.Unlock()
		}
	}()
	ts := m.cl.Torrents()
	for i := 0; i < 3000; i++ {
		m.applyQueue(ts, m.snapshotRecords())
	}
	close(stop)
	<-done
}
