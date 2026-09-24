package core

import (
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

func manualAddrs(t *testing.T, m *Manager, hash string) []string {
	t.Helper()
	list, err := m.ManualPeers(hash)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range list {
		out = append(out, p.Addr)
	}
	return out
}

// The peers added by hand come back after a restart, can be removed one by one, and are limited in number.
func TestManualPeersSurviveARestartAndCanBeRemoved(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "k.bin", 20<<10), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddPeers(hash, []string{"203.0.113.5:6881", "Localhost:6882", "[2001:db8::1]:6883", "203.0.113.5:6881"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.5:6881", "localhost:6882", "[2001:db8::1]:6883"} // a name is kept as a name, in lower case
	if got := manualAddrs(t, m, hash); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("saved peers: %v, want %v", got, want)
	}
	m.Close()

	cfg, _ := config.Load(filepath.Join(dir, "settings.json"), dir)
	m2, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAndSettle(m2, dir)
	if got := manualAddrs(t, m2, hash); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after the restart: %v, want %v", got, want)
	}

	n, err := m2.RemoveManualPeers(hash, []string{"LOCALHOST:6882", "198.51.100.1:1"})
	if err != nil || n != 1 {
		t.Fatalf("remove: %d, %v", n, err)
	}
	if got := manualAddrs(t, m2, hash); fmt.Sprint(got) != fmt.Sprint([]string{"203.0.113.5:6881", "[2001:db8::1]:6883"}) {
		t.Fatalf("after removing one: %v", got)
	}

	// At most maxSavedPeers; the refused request adds nothing at all.
	var many []string
	for i := 0; i < maxSavedPeers; i++ {
		many = append(many, "203.0.113.9:"+strconv.Itoa(2000+i))
	}
	if _, err := m2.AddPeers(hash, many); err == nil {
		t.Fatal("more than the limit must be refused")
	}
	if got := manualAddrs(t, m2, hash); len(got) != 2 {
		t.Fatalf("a refused request must add nothing: %v", got)
	}
}

// Only a peer that has not been connected for a long time is reported stale, and it is not removed by itself.
func TestManualPeerGoesStaleButStays(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "s.bin", 20<<10), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddPeers(hash, []string{"203.0.113.5:6881", "203.0.113.6:6881"}); err != nil {
		t.Fatal(err)
	}
	set := func(addr string, added, seen time.Time) {
		_ = m.state.with(func(s *state) {
			for i := range s.Torrents[hash].ManualPeers {
				if p := &s.Torrents[hash].ManualPeers[i]; p.Addr == addr {
					p.Added, p.LastSeen = added, seen
				}
			}
		})
	}
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d) }
	day := 24 * time.Hour
	set("203.0.113.5:6881", ago(40*day), ago(20*day)) // last seen three weeks ago
	set("203.0.113.6:6881", ago(40*day), ago(2*day))  // seen two days ago
	list, err := m.ManualPeers(hash)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	if !list[0].Stale || list[1].Stale {
		t.Errorf("stale flags: %+v", list)
	}
	// Never seen since it was added long ago: stale as well (LastSeen is zero).
	set("203.0.113.6:6881", ago(30*day), time.Time{})
	if list, _ = m.ManualPeers(hash); !list[1].Stale {
		t.Errorf("a peer never seen since it was added a month ago must be stale: %+v", list[1])
	}
	m.serviceManualPeers(m.cl.Torrents(), m.snapshotRecords(), manualSeenEvery)
	if got := manualAddrs(t, m, hash); len(got) != 2 {
		t.Errorf("stale peers must stay until the user removes them: %v", got)
	}
	// Adding it again counts as a fresh start for it.
	if _, err := m.AddPeers(hash, []string{"203.0.113.5:6881"}); err != nil {
		t.Fatal(err)
	}
	if list, _ = m.ManualPeers(hash); list[0].Stale {
		t.Errorf("a peer added again must not be stale: %+v", list[0])
	}
}

// A saved peer that is really connected shows as connected, and being seen renews it.
func TestConnectedManualPeerIsSeen(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a := newManager(t, dirA, nil)
	tp := makeTorrent(t, dirA, "live.bin", 400<<10)
	seed(t, a, dirA, "live.bin")
	hashA, err := a.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(a, hashA); return ok && s.Progress == 1 })

	// Slow, so that the connection lasts long enough to look at.
	b := newManager(t, dirB, func(s *config.Settings) { s.DownLimitKBps = 60 })
	hashB, err := b.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddPeers(hashB, []string{"127.0.0.1:" + strconv.Itoa(a.Port())}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-20 * 24 * time.Hour)
	_ = b.state.with(func(s *state) {
		s.Torrents[hashB].ManualPeers[0].Added, s.Torrents[hashB].ManualPeers[0].LastSeen = old, old
	})

	waitFor(t, func() bool {
		l, _ := b.ManualPeers(hashB)
		return len(l) == 1 && l[0].Connected
	})
	l, _ := b.ManualPeers(hashB)
	if l[0].Stale {
		t.Errorf("a connected peer is not stale: %+v", l[0])
	}
	b.serviceManualPeers(b.cl.Torrents(), b.snapshotRecords(), manualSeenEvery)
	l, _ = b.ManualPeers(hashB)
	if time.Since(l[0].LastSeen) > time.Minute {
		t.Errorf("a look at the connected peer must renew its last-seen time: %+v", l[0])
	}
	// Removing it drops the connection.
	if _, err := b.RemoveManualPeers(hashB, []string{"127.0.0.1:" + strconv.Itoa(a.Port())}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		conns, _ := b.Peers(hashB)
		return len(conns) == 0
	})
}
