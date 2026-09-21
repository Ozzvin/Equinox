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
	defer func() { m2.Close(); time.Sleep(500 * time.Millisecond) }()

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
