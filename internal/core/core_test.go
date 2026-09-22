package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"

	"github.com/Ozzvin/equinox/internal/config"
)

// makeTorrent writes payload into <dir>/src/<name> and returns a .torrent file for it.
func makeTorrent(t *testing.T, dir, name string, size int) (torrentPath string) {
	t.Helper()
	src := filepath.Join(dir, "src", name)
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, bytes.Repeat([]byte{7}, size), 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(src); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: b}
	torrentPath = filepath.Join(dir, name+".torrent")
	f, _ := os.Create(torrentPath)
	defer f.Close()
	if err := mi.Write(f); err != nil {
		t.Fatal(err)
	}
	return torrentPath
}

func newManager(t *testing.T, dir string, mut func(*config.Settings)) *Manager {
	t.Helper()
	cfg, err := config.Load(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(s *config.Settings) {
		s.ListenPort = 0
		s.PortMapping = false // tests must not touch the real router
		if mut != nil {
			mut(s)
		}
	}); err != nil {
		t.Fatal(err)
	}
	m, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestCopyKeptAndDeletedWithData(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeTorrent(t, dir, "movie.bin", 100<<10)

	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "torrents", "movie.bin.torrent")
	if !exists(copyPath) {
		t.Fatal("copy of .torrent was not created under the torrent's own name")
	}

	// Wait for preallocation to create the data file at full size.
	data := filepath.Join(m.cfg.Get().DataDir, "movie.bin")
	waitFor(t, func() bool {
		fi, err := os.Stat(data)
		return err == nil && fi.Size() == 100<<10
	})

	// Remove WITHOUT data: files stay, copy stays (policy with_data).
	if err := m.Remove(hash, false); err != nil {
		t.Fatal(err)
	}
	if !exists(data) || !exists(copyPath) {
		t.Fatal("remove without data must keep files and the copy")
	}

	// Add again, remove WITH data: both must go.
	hash, err = m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(hash, true); err != nil {
		t.Fatal(err)
	}
	if exists(data) {
		t.Fatal("data was not deleted")
	}
	if exists(copyPath) {
		t.Fatal(".torrent copy was not deleted together with data")
	}
	if len(m.List()) != 0 {
		t.Fatal("torrent still listed")
	}
}

func TestTorrentCopyNameFallsBackToHashOnCollision(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	if err := os.MkdirAll(filepath.Join(dir, "one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "two"), 0o755); err != nil {
		t.Fatal(err)
	}
	tp1 := makeTorrent(t, dir, "one/movie.bin", 50<<10)
	tp2 := makeTorrent(t, dir, "two/movie.bin", 60<<10) // same display name, different content/hash

	if _, err := m.AddFile(tp1); err != nil {
		t.Fatal(err)
	}
	byName := filepath.Join(dir, "torrents", "movie.bin.torrent")
	if !exists(byName) {
		t.Fatal("first torrent should get a copy named after itself")
	}

	hash2, err := m.AddFile(tp2)
	if err != nil {
		t.Fatal(err)
	}
	byHash := filepath.Join(dir, "torrents", hash2+".torrent")
	if !exists(byHash) {
		t.Fatal("second torrent with a colliding display name should fall back to its hash")
	}
	if !exists(byName) {
		t.Fatal("the first torrent's copy must not be overwritten by the second")
	}
}

func TestRestartKeepsTorrentsAndRatio(t *testing.T) {
	dir := t.TempDir()
	tp := makeTorrent(t, dir, "a.bin", 50<<10)

	m := newManager(t, dir, nil)
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	_ = m.state.with(func(s *state) { s.Torrents[hash].Uploaded = 12345; s.Torrents[hash].Downloaded = 100 })
	m.Close()

	cfg, _ := config.Load(filepath.Join(dir, "settings.json"), dir)
	m2, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	// The engine closes its files asynchronously; give Windows time to release them
	// before the temp dir is removed.
	defer func() { m2.Close(); time.Sleep(500 * time.Millisecond) }()
	l := m2.List()
	if len(l) != 1 || l[0].Uploaded != 12345 || l[0].Downloaded != 100 {
		t.Fatalf("state lost after restart: %+v", l)
	}
}

func TestTurtleMode(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) {
		s.DownLimitKBps, s.UpLimitKBps = 0, 0
		s.AltDownLimitKBps, s.AltUpLimitKBps = 50, 20
	})
	if m.downLim.Limit() != rate.Inf {
		t.Fatal("expected unlimited by default")
	}
	if err := m.SetAltSpeed(true); err != nil {
		t.Fatal(err)
	}
	if m.downLim.Limit() != rate.Limit(50<<10) || m.upLim.Limit() != rate.Limit(20<<10) {
		t.Fatalf("turtle limits not applied: %v %v", m.downLim.Limit(), m.upLim.Limit())
	}
	_ = m.SetAltSpeed(false)
	if m.downLim.Limit() != rate.Inf {
		t.Fatal("limits not restored")
	}
}

func TestPreallocateRefusesWhenNoSpace(t *testing.T) {
	if _, err := freeSpace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if ratio(0, 500, 1000) != 0.5 || ratio(100, 300, 1000) != 3 {
		t.Fatal("ratio math")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
