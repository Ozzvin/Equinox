package core

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
)

func TestResolvePeerAddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, c := range []struct {
		in     string
		want   string // the single address expected, "" if none
		reason string
	}{
		{"203.0.113.5:51413", "203.0.113.5:51413", ""},
		{" 203.0.113.5:6881 ", "203.0.113.5:6881", ""},
		{"[2001:db8::1]:6881", "[2001:db8::1]:6881", ""},
		{"[::ffff:192.168.1.7]:6881", "192.168.1.7:6881", ""}, // the same peer, written the IPv4 way
		{"127.0.0.1:6881", "127.0.0.1:6881", ""},              // the local machine and network are allowed
		{"192.168.1.7:6881", "192.168.1.7:6881", ""},
		{"203.0.113.5", "", "format"},
		{"203.0.113.5:", "", "port"},
		{":6881", "", "format"},
		{"", "", "format"},
		{"not an address", "", "format"},
		{"203.0.113.5:0", "", "port"},
		{"203.0.113.5:65536", "", "port"},
		{"203.0.113.5:abc", "", "port"},
		{"0.0.0.0:6881", "", "address"},
		{"[::]:6881", "", "address"},
		{"224.0.0.1:6881", "", "address"},
		{"[fe80::1%eth0]:6881", "", "address"}, // a zone means nothing to another machine
		{"bad host name:6881", "", "format"},
		{"bad/host:6881", "", "format"},
	} {
		addrs, reason := resolvePeerAddr(ctx, c.in)
		if reason != c.reason || (c.want == "") != (len(addrs) == 0) || (c.want != "" && addrs[0] != c.want) {
			t.Errorf("%q: got %v, %q; want %q, %q", c.in, addrs, reason, c.want, c.reason)
		}
	}
	// A name is looked up now; localhost needs no network.
	addrs, reason := resolvePeerAddr(ctx, "localhost:6881")
	if reason != "" || len(addrs) == 0 || len(addrs) > maxPerHostname {
		t.Errorf("localhost: %v, %q", addrs, reason)
	}
	// (A made-up name is no good for this: some providers answer every name.)
	gone, stop := context.WithCancel(context.Background())
	stop()
	if _, reason := resolvePeerAddr(gone, "localhost:6881"); reason != "resolve" {
		t.Errorf("a lookup that cannot be done must say resolve, got %q", reason)
	}
	if sourceName(torrent.PeerSourceDirect) != "manual" {
		t.Errorf("a peer added by hand must show as manual, got %q", sourceName(torrent.PeerSourceDirect))
	}
}

func TestAddPeersRefusesWhatIsNotAPeerRequest(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "p.bin", 20<<10), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddPeers(hash, nil); err == nil {
		t.Error("an empty list must be refused")
	}
	tooMany := make([]string, maxManualPeers+1)
	for i := range tooMany {
		tooMany[i] = "203.0.113.5:6881"
	}
	if _, err := m.AddPeers(hash, tooMany); err == nil {
		t.Error("too many addresses must be refused")
	}
	if _, err := m.AddPeers("0123456789012345678901234567890123456789", []string{"203.0.113.5:6881"}); err == nil {
		t.Error("an unknown torrent must be refused")
	}
	res, err := m.AddPeers(hash, []string{"203.0.113.5:6881", "203.0.113.5:6881", "nonsense", "203.0.113.6:6881"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Addr != "nonsense" || res.Errors[0].Reason != "format" {
		t.Errorf("errors: %+v", res.Errors)
	}
	if res.Added+res.Known != 2 { // the repeated address counts once
		t.Errorf("two different good addresses expected, got added %d + known %d", res.Added, res.Known)
	}
	again, _ := m.AddPeers(hash, []string{"203.0.113.5:6881"})
	if again.Added != 0 || again.Known != 1 {
		t.Errorf("an address given before is known, got %+v", again)
	}
}

// A peer added by hand is really used: a second client that has nothing but the torrent file downloads it from the
// first one, which it can only know about through the address it was given.
func TestAddedPeerIsUsed(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a := newManager(t, dirA, nil)
	tp := makeTorrent(t, dirA, "shared.bin", 200<<10)
	seed(t, a, dirA, "shared.bin")
	hashA, err := a.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(a, hashA); return ok && s.Progress == 1 })

	b := newManager(t, dirB, nil)
	hashB, err := b.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := statusOf(b, hashB); s.Progress != 0 {
		t.Fatalf("the second client must start empty, got %v", s.Progress)
	}
	res, err := b.AddPeers(hashB, []string{"127.0.0.1:" + strconv.Itoa(a.Port())})
	if err != nil || res.Added != 1 {
		t.Fatalf("add peer: %+v, %v", res, err)
	}
	waitFor(t, func() bool { s, ok := statusOf(b, hashB); return ok && s.Progress == 1 })
}
