package core

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

// Many torrents with real amounts of data on disk: at no moment may more torrents be hashing than the limit.
func TestNeverMoreChecksThanTheLimit(t *testing.T) {
	for _, limit := range []int{1, 2} {
		t.Run(fmt.Sprintf("limit%d", limit), func(t *testing.T) {
			dir := t.TempDir()
			m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = limit })
			var hashes []string
			for i := 0; i < 8; i++ {
				name := fmt.Sprintf("big%d.bin", i)
				tp := makeTorrent(t, dir, name, 4<<20)
				putOnDisk(t, dir, name)
				h, err := m.AddFile(tp)
				if err != nil {
					t.Fatal(err)
				}
				hashes = append(hashes, h)
			}
			max := 0
			deadline := time.Now().Add(60 * time.Second)
			for time.Now().Before(deadline) {
				n, done := 0, 0
				for _, h := range hashes {
					tor, err := m.get(h)
					if err != nil || tor.Info() == nil {
						continue
					}
					if c, _ := checkingPieces(tor); c > 0 {
						n++
					}
					if s, ok := statusOf(m, h); ok && s.Progress == 1 {
						done++
					}
				}
				if n > max {
					max = n
				}
				if done == len(hashes) {
					break
				}
				time.Sleep(3 * time.Millisecond)
			}
			t.Logf("limit %d: at most %d torrents were being hashed at the same time", limit, max)
			if max > limit {
				t.Fatalf("limit %d, but %d torrents were hashed at once", limit, max)
			}
		})
	}
}

// After a restart the torrents that still need a check are restored: the limit holds for them too.
func TestChecksAfterRestartRespectTheLimit(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 0 })
	var hashes []string
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("r%d.bin", i)
		tp := makeTorrent(t, dir, name, 4<<20)
		putOnDisk(t, dir, name)
		h, err := m.AddFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	m.Close() // interrupted while they are still being hashed: most pieces have no verdict yet

	cfg, _ := config.Load(filepath.Join(dir, "settings.json"), dir)
	_ = cfg.Update(func(s *config.Settings) { s.MaxConcurrentChecks = 1 })
	m2, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAndSettle(m2, dir)

	max := 0
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		n, done := 0, 0
		for _, h := range hashes {
			tor, err := m2.get(h)
			if err != nil || tor.Info() == nil {
				continue
			}
			if c, _ := checkingPieces(tor); c > 0 {
				n++
			}
			if s, ok := statusOf(m2, h); ok && s.Progress == 1 {
				done++
			}
		}
		if n > max {
			max = n
		}
		if done == len(hashes) {
			break
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Logf("after a restart with limit 1: at most %d torrents hashed at once", max)
	if max > 1 {
		t.Fatalf("limit 1, but %d torrents were hashed at once", max)
	}
}

// Lowering the limit while checks are running puts the ones lowest in the queue back into the line.
func TestLoweringTheLimitPutsTheExtraCheckBackInTheLine(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 2 })
	var hashes []string
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("low%d.bin", i)
		tp := makeTorrent(t, dir, name, 160<<20)
		putOnDisk(t, dir, name)
		h, err := m.AddFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	a, b := hashes[0], hashes[1]
	tb, _ := m.get(b)
	if c, _ := checkingPieces(tb); c == 0 {
		t.Skip("the second check was already over")
	}
	if err := m.cfg.Update(func(s *config.Settings) { s.MaxConcurrentChecks = 1 }); err != nil {
		t.Fatal(err)
	}
	m.shrinkToLimit()

	sa, _ := statusOf(m, a)
	sb, ok := statusOf(m, b)
	if ok && sb.CheckQueued == 0 && !sb.Checking {
		t.Skip("the second check ended before the limit was lowered (a fast disk)")
	}
	if !sa.Checking && sa.CheckQueued == 0 {
		// The first check ended in between, freeing the only place, and the second one took it again at once:
		// there is nothing left to see, and how soon that happens depends on the speed of the disk and of the machine.
		t.Skip("the first check ended before the second one was put back into the line")
	}
	if !ok || sb.CheckQueued == 0 {
		t.Fatalf("the torrent lower in the queue must wait for its turn: %+v", sb)
	}
	if sa.CheckQueued != 0 {
		t.Fatalf("the one higher in the queue keeps its check: %+v", sa)
	}
	// Both are done in the end; the second one goes on from where it stopped.
	waitFor(t, func() bool {
		x, _ := statusOf(m, a)
		y, _ := statusOf(m, b)
		return x.Progress == 1 && y.Progress == 1 && y.CheckQueued == 0
	})
}

// A manual recheck (the "Проверить файлы" button) must respect the check limit too, queuing
// behind whatever already holds the slot instead of running alongside it.
func TestRecheckRespectsTheLimit(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 1 })
	var hashes []string
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("rl%d.bin", i)
		tp := makeTorrent(t, dir, name, 4<<20)
		seed(t, m, dir, name)
		h, err := m.AddFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
		waitFor(t, func() bool { s, ok := statusOf(m, h); return ok && s.Progress == 1 })
	}

	for _, h := range hashes {
		if err := m.Recheck(h); err != nil {
			t.Fatal(err)
		}
	}

	max := 0
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		n, done := 0, 0
		for _, h := range hashes {
			tor, err := m.get(h)
			if err != nil {
				continue
			}
			if c, _ := checkingPieces(tor); c > 0 {
				n++
			}
			if s, ok := statusOf(m, h); ok && !s.Checking && s.CheckQueued == 0 {
				done++
			}
		}
		if n > max {
			max = n
		}
		if done == len(hashes) {
			break
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Logf("limit 1: at most %d manual rechecks ran at once", max)
	if max > 1 {
		t.Fatalf("limit 1, but %d rechecks ran at once", max)
	}
}

// A fresh recheck request jumps to the front of the recheck line, ahead of one already
// waiting: b is asked for first but c, asked for afterward, must start checking before it does.
//
// The only slot is held by hand, so nothing depends on how long a real check lasts, and the order is read
// from the line itself and from what is running, both under the manager's lock: the start of a short check
// is too brief for a poll to be sure to catch it.
func TestRecheckJumpsToTheFrontOfTheQueue(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 1 })
	var hashes []string
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("front%d.bin", i)
		tp := makeTorrent(t, dir, name, 8<<20)
		seed(t, m, dir, name)
		h, err := m.AddFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
		waitFor(t, func() bool { s, ok := statusOf(m, h); return ok && s.Progress == 1 })
	}
	a, b, c := hashes[0], hashes[1], hashes[2]

	m.mu.Lock()
	m.rechecking[a] = true // the slot is taken
	m.mu.Unlock()
	line := func() []string {
		m.mu.Lock()
		defer m.mu.Unlock()
		return append([]string(nil), m.recheckQueue...)
	}

	if err := m.Recheck(b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(line()) == 1 })
	if err := m.Recheck(c); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(line()) == 2 })
	if l := line(); l[0] != c || l[1] != b {
		t.Fatalf("c asked for a recheck after b but should be first in line: %v (b=%s c=%s)", l, b, c)
	}

	// Free the slot. While c is still waiting b must not run, and both must get through in the end.
	m.mu.Lock()
	delete(m.rechecking, a)
	m.mu.Unlock()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		cWaits := false
		for _, h := range m.recheckQueue {
			cWaits = cWaits || h == c
		}
		bRuns := m.rechecking[b]
		gone := len(m.recheckQueue) == 0 && !m.rechecking[b] && !m.rechecking[c] && !m.checking[b] && !m.checking[c]
		m.mu.Unlock()
		if cWaits && bRuns {
			t.Fatal("b started checking while c, asked for later, was still waiting")
		}
		if gone {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("b and c were not both checked in time")
}
