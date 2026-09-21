package core

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/portmap"
)

func TestPortVerdicts(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		st     portmap.Status
		public int64
		last   time.Time
		want   string
	}{
		{"inbound wins", portmap.Status{Enabled: true}, 3, now.Add(-time.Hour), PortOpen},
		{"stale inbound ignored", portmap.Status{Enabled: true, Mapped: true, ExternalIP: "203.0.113.7"}, 3, now.Add(-48 * time.Hour), PortMapped},
		{"cgnat range", portmap.Status{Enabled: true, Mapped: true, ExternalIP: "100.72.1.9"}, 0, time.Time{}, PortCGNAT},
		{"private router ip", portmap.Status{Enabled: true, Mapped: true, ExternalIP: "192.168.0.1"}, 0, time.Time{}, PortCGNAT},
		{"manual", portmap.Status{Enabled: false}, 0, time.Time{}, PortManual},
		{"mapped, waiting", portmap.Status{Enabled: true, Mapped: true, ExternalIP: "203.0.113.7"}, 0, time.Time{}, PortMapped},
		{"router silent", portmap.Status{Enabled: true, Failures: 2, LastError: "no UPnP gateway found"}, 0, time.Time{}, PortClosed},
		{"first check", portmap.Status{Enabled: true}, 0, time.Time{}, PortChecking},
	}
	for _, c := range cases {
		r := makeReport(c.st, c.public, c.last, false, now, 0)
		if r.Verdict != c.want || r.Advice == "" {
			t.Errorf("%s: verdict %q (want %q), advice %q", c.name, r.Verdict, c.want, r.Advice)
		}
	}
}

func TestPublicAddressClassification(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8:6881": true, "203.0.113.5:1": true, "[2001:4860:4860::8888]:6881": true,
		"127.0.0.1:6881": false, "192.168.1.5:6881": false, "10.0.0.2:1": false,
		"172.16.4.4:1": false, "100.64.0.1:1": false, "169.254.1.1:1": false, "[::1]:1": false, "junk": false,
	} {
		if got := isPublic(addr); got != want {
			t.Errorf("isPublic(%q) = %v, want %v", addr, got, want)
		}
	}
}

// Two real engines on loopback: the seeder must see B's connection as inbound. This also
// pins the reflection on the engine's private "outgoing" field to the library version.
func TestInboundConnectionDetected(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	tp := makeTorrent(t, dirA, "shared.bin", 64<<10)
	a := newManager(t, dirA, nil)
	// Seeder already has the data, so it is verified and becomes complete.
	mi, err := metainfo.LoadFromFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os_copy(dirA+"/src/shared.bin", a.cfg.Get().DataDir+"/shared.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddMetaInfo(mi); err != nil {
		t.Fatal(err)
	}
	b := newManager(t, dirB, nil)
	hash, err := b.AddMetaInfo(mi)
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	tb := b.torrents[metainfo.NewHashFromHex(hash)]
	b.mu.Unlock()
	// ListenAddrs would give an unspecified address ([::]); dial loopback explicitly.
	peer := []torrent.PeerInfo{{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: a.Port()}, Trusted: true}}
	added := tb.AddPeers(peer)

	defer func() {
		if t.Failed() {
			st := tb.Stats()
			ta, _, _, _ := a.inbound.snapshot()
			tbt, _, _, _ := b.inbound.snapshot()
			t.Logf("A port %d list=%+v inbound=%d", a.Port(), a.List(), ta)
			t.Logf("B port %d stats total=%d active=%d pending=%d halfopen=%d inbound=%d", b.Port(), st.TotalPeers, st.ActivePeers, st.PendingPeers, st.HalfOpenPeers, tbt)
		}
	}()
	waitFor(t, func() bool {
		total, _, _, _ := a.inbound.snapshot()
		if total < 1 {
			added += tb.AddPeers(peer) // early additions can be dropped while the leecher is still hashing
		}
		return total >= 1
	})
	t.Logf("peer additions accepted: %d", added)
	total, public, _, unknown := a.inbound.snapshot()
	if unknown {
		t.Fatal("engine no longer exposes the connection direction: update isOutgoing")
	}
	if total < 1 || public != 0 {
		t.Fatalf("loopback peer must count as inbound but not as public: total=%d public=%d", total, public)
	}
	if _, _, _, unk := b.inbound.snapshot(); unk {
		t.Fatal("unexpected unknown direction on the leecher")
	}
}

func os_copy(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}
