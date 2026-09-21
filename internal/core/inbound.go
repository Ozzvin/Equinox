package core

import (
	"net/netip"
	"reflect"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

// inboundTracker records peers that connected TO us. A single such connection from a
// public address is the only real proof that the port is reachable from the internet.
type inboundTracker struct {
	mu         sync.Mutex
	total      int64     // all inbound connections, including LAN and loopback
	public     int64     // inbound connections from public addresses
	lastPublic time.Time // when the last public inbound connection happened
	unknown    bool      // the engine no longer exposes the direction of a connection
}

// onHandshake is the engine callback fired for every completed peer handshake.
func (t *inboundTracker) onHandshake(pc *torrent.PeerConn, _ torrent.InfoHash) {
	out, ok := isOutgoing(pc)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !ok {
		t.unknown = true
		return
	}
	if out {
		return
	}
	t.total++
	if isPublic(pc.RemoteAddr.String()) {
		t.public++
		t.lastPublic = time.Now()
	}
}

func (t *inboundTracker) snapshot() (total, public int64, last time.Time, unknown bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total, t.public, t.lastPublic, t.unknown
}

// isOutgoing reads the engine's unexported "outgoing" flag: the library offers no public
// accessor for the direction of a connection. A test pins this to the library version;
// if the field disappears the tracker reports "unknown" instead of guessing.
func isOutgoing(pc *torrent.PeerConn) (outgoing, ok bool) {
	f := reflect.ValueOf(pc).Elem().FieldByName("outgoing")
	if !f.IsValid() || f.Kind() != reflect.Bool {
		return false, false
	}
	return f.Bool(), true
}

// isPublic reports whether addr ("ip:port") is an address on the public internet.
func isPublic(addr string) bool {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return false
	}
	return isPublicIP(ap.Addr())
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func isPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsUnspecified() && !ip.IsMulticast() && !cgnat.Contains(ip)
}
