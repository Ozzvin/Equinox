package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

func TestSeedTimeLimitStopsSeedingAndReportsIt(t *testing.T) {
	dir := t.TempDir()
	tp := makeTorrent(t, dir, "a.bin", 50<<10)
	putOnDisk(t, dir, "a.bin")
	m := newManager(t, dir, nil)
	events := make(chan Event, 8)
	m.OnEvent(func(e Event) { events <- e })

	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	// Files found on disk are not a "download that finished".
	select {
	case e := <-events:
		if e.Kind == "completed" {
			t.Fatalf("a torrent found complete on disk must not announce a finished download: %+v", e)
		}
	case <-time.After(1500 * time.Millisecond):
	}

	if err := m.SetSeedTimeLimit(hash, 1); err != nil {
		t.Fatal(err)
	}
	// One second short of the limit: the next ticks push it over.
	_ = m.state.with(func(s *state) { s.Torrents[hash].SeedSeconds = 59 })
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind != "limit" || e.Hash != hash || e.Detail == "" {
				continue
			}
			if s, _ := statusOf(m, hash); !s.Paused {
				t.Fatal("the torrent must be paused once the limit is reached")
			}
			// The paused torrent stops counting.
			before := m.seedSeconds(hash, m.snapshotRecords()[hash])
			time.Sleep(2500 * time.Millisecond)
			if after := m.seedSeconds(hash, m.snapshotRecords()[hash]); after != before {
				t.Fatalf("time kept running while paused: %d -> %d", before, after)
			}
			return
		case <-deadline:
			t.Fatal("no limit event")
		}
	}
}

func TestSeedTimeLimitFallsBackToTheGlobalSetting(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.SeedTimeLimitMinutes = 90 })
	if got := m.seedLimit(record{}); got != 90 {
		t.Fatalf("global limit: %d", got)
	}
	if got := m.seedLimit(record{SeedTimeLimit: 5}); got != 5 {
		t.Fatalf("own limit must win: %d", got)
	}
	if err := m.SetSeedTimeLimit("nope", 5); err == nil {
		t.Fatal("unknown torrent")
	}
	if err := m.SetSeedTimeLimit(makeAndAdd(t, m, dir), -1); err == nil {
		t.Fatal("negative limit must be refused")
	}
}

// putOnDisk copies the source file of makeTorrent into the download folder, so the torrent is complete.
func putOnDisk(t *testing.T, dir, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "src", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "downloads", name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeAndAdd(t *testing.T, m *Manager, dir string) string {
	t.Helper()
	tp := makeTorrent(t, dir, "b.bin", 20<<10)
	putOnDisk(t, dir, "b.bin")
	h, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestSeedTimePersistsAcrossFlush(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	h := makeAndAdd(t, m, dir)
	rec := m.snapshotRecords()[h]
	m.addSeedTime(h, rec, 3500*time.Millisecond)
	m.flushSeedTime()
	if got := m.snapshotRecords()[h].SeedSeconds; got != 3 {
		t.Fatalf("whole seconds are written to the record: %d", got)
	}
	if got := m.seedSeconds(h, m.snapshotRecords()[h]); got != 3 {
		t.Fatalf("the remaining half second must not be counted twice: %d", got)
	}
}
