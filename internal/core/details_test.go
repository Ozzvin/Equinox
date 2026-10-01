package core

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/config"
)

func TestDetailsAndTrackers(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeTorrent(t, dir, "info.bin", 64<<10)
	seed(t, m, dir, "info.bin")
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	d, err := m.Details(hash)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "info.bin" || d.TotalSize != 64<<10 || d.Pieces != 4 || d.PieceLength != 16<<10 || d.Files != 1 {
		t.Fatalf("unexpected details: %+v", d)
	}
	if !strings.HasPrefix(d.Magnet, "magnet:?xt=urn:btih:"+hash) {
		t.Fatalf("magnet: %s", d.Magnet)
	}
	if d.SavePath == "" || d.Added.IsZero() {
		t.Fatalf("missing folder/date: %+v", d)
	}

	// Trackers: added by the user, deduplicated, validated, persisted.
	if err := m.AddTracker(hash, "udp://tracker.example.org:6969/announce"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTracker(hash, " udp://tracker.example.org:6969/announce "); err != nil {
		t.Fatal(err) // same tracker again: fine, not duplicated
	}
	for _, bad := range []string{"", "not a url", "ftp://x.example/a", "http://"} {
		if err := m.AddTracker(hash, bad); err == nil {
			t.Errorf("tracker %q must be rejected", bad)
		}
	}
	d, _ = m.Details(hash)
	var found int
	for _, tr := range d.Trackers {
		if tr.URL == "udp://tracker.example.org:6969/announce" {
			found++
			if !tr.Added {
				t.Error("user-added tracker must be flagged")
			}
		}
	}
	if found != 1 {
		t.Fatalf("tracker listed %d times: %+v", found, d.Trackers)
	}

	m.Close()
	cfg, _ := config.Load(filepath.Join(dir, "settings.json"), dir)
	m2, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m2.Close(); waitFor(t, func() bool { return true }) }()
	waitFor(t, func() bool { _, err := m2.Details(hash); return err == nil })
	d, _ = m2.Details(hash)
	found = 0
	for _, tr := range d.Trackers {
		if tr.URL == "udp://tracker.example.org:6969/announce" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("added tracker lost after restart: %+v", d.Trackers)
	}
}

func TestRecheckDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeTorrent(t, dir, "check.bin", 64<<10)
	seed(t, m, dir, "check.bin")
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	// Rot the second half of the file behind the engine's back.
	p := filepath.Join(m.cfg.Get().DataDir, "check.bin")
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(make([]byte, 32<<10), 32<<10); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if s, _ := statusOf(m, hash); s.Progress != 1 {
		t.Fatal("the engine cannot know about the damage before a check")
	}
	if err := m.Recheck(hash); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, _ := statusOf(m, hash); return !s.Checking && s.Progress < 1 })
	if s, _ := statusOf(m, hash); s.Progress != 0.5 {
		t.Fatalf("half of the data is damaged, progress should be 0.5, got %v", s.Progress)
	}
	if err := m.Recheck("00000000000000000000000000000000000000ff"); err == nil {
		t.Fatal("unknown torrent must be rejected")
	}
}

func TestPeersListShowsBothEnds(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	tp := makeTorrent(t, dirA, "peers.bin", 64<<10)
	a, b := newManager(t, dirA, nil), newManager(t, dirB, nil)
	// Neither side has data, so the connection stays open instead of being dropped
	// as "both are seeds".
	ha, err := a.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := b.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	tb := b.torrents[metainfo.Hash(mustHash(t, hb))]
	b.mu.Unlock()
	peer := []torrent.PeerInfo{{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: a.Port()}, Trusted: true}}
	waitFor(t, func() bool {
		tb.AddPeers(peer)
		p, _ := a.Peers(ha)
		return len(p) > 0
	})
	pa, _ := a.Peers(ha)
	if !pa[0].Incoming || !strings.HasPrefix(pa[0].Addr, "127.0.0.1:") {
		t.Fatalf("seeder side must see an incoming peer from loopback: %+v", pa)
	}
	waitFor(t, func() bool { p, _ := b.Peers(hb); return len(p) > 0 })
	pb, _ := b.Peers(hb)
	if pb[0].Incoming {
		t.Fatalf("the leecher dialled out, so its peer is not incoming: %+v", pb)
	}
}

func TestPeerFilesReportsWhatThePeerActuallyHas(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	mi := namedTwoFileTorrent(t, dirA, "pack") // a.bin (32 KiB, pieces 0-1), b.bin (48 KiB, pieces 2-4)
	a, b := newManager(t, dirA, nil), newManager(t, dirB, nil)

	// A seeds the real data.
	var copied atomic.Int64
	if err := copyTree(filepath.Join(dirA, "src", "pack"), filepath.Join(a.cfg.Get().DataDir, "pack"), &copied); err != nil {
		t.Fatal(err)
	}
	ha, err := a.AddMetaInfo(mi)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(a, ha); return ok && s.Progress == 1 })

	// B gets its own copy of the metainfo (AddFile would do this too, loading fresh from bytes;
	// sharing one *metainfo.MetaInfo between two managers confuses the engine).
	raw, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatal(err)
	}
	miB, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	hb, err := b.AddMetaInfo(miB)
	if err != nil {
		t.Fatal(err)
	}
	// B wants only a.bin: b.bin is skipped entirely, so B never has any of its pieces.
	if err := b.SetFilePriorities(hb, []int{1}, PrioSkip); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	tb := b.torrents[metainfo.Hash(mustHash(t, hb))]
	b.mu.Unlock()
	peer := []torrent.PeerInfo{{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: a.Port()}, Trusted: true}}
	tb.AddPeers(peer)
	waitFor(t, func() bool { p, _ := a.Peers(ha); return len(p) > 0 })
	pa, _ := a.Peers(ha)
	if len(pa) == 0 {
		t.Fatal("A must see B connected")
	}
	waitFor(t, func() bool { s, ok := statusOf(b, hb); return ok && s.Progress == 1 })

	waitFor(t, func() bool {
		fs, err := a.PeerFiles(ha, pa[0].Addr)
		return err == nil && len(fs) == 2 && fs[0].Progress > 0.99
	})
	fs, err := a.PeerFiles(ha, pa[0].Addr)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Path != "pack/a.bin" || fs[1].Path != "pack/b.bin" {
		t.Fatalf("unexpected files: %+v", fs)
	}
	if fs[0].Progress < 0.99 {
		t.Fatalf("B has all of a.bin: %+v", fs[0])
	}
	if fs[1].Progress > 0.01 {
		t.Fatalf("B skipped b.bin entirely, must show as not had: %+v", fs[1])
	}

	if _, err := a.PeerFiles(ha, "203.0.113.9:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an address that is not connected must be rejected: %v", err)
	}
}

func mustHash(t *testing.T, s string) metainfo.Hash {
	t.Helper()
	h, ok := parseHash(s)
	if !ok {
		t.Fatalf("bad hash %q", s)
	}
	return h
}
