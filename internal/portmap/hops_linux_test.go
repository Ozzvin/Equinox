//go:build linux

package portmap

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// An IP_RECVERR message: a sock_extended_err, then the address of whoever sent the ICMP message.
func recvErr(origin, icmpType byte, family uint16, addr [4]byte) []byte {
	d := make([]byte, 16+16)
	d[4], d[5] = origin, icmpType
	binary.NativeEndian.PutUint16(d[16:], family)
	copy(d[20:], addr[:])
	return d
}

func TestOffenderIn(t *testing.T) {
	if ip := offenderIn(recvErr(unix.SO_EE_ORIGIN_ICMP, 11, unix.AF_INET, [4]byte{192, 168, 50, 1})); !ip.Equal(net.IPv4(192, 168, 50, 1)) {
		t.Fatalf("time exceeded from the router: got %v", ip)
	}
	if ip := offenderIn(recvErr(unix.SO_EE_ORIGIN_ICMP, 3, unix.AF_INET, [4]byte{1, 1, 1, 1})); ip != nil {
		t.Fatalf("an unreachable from the target is not a router on the way: got %v", ip)
	}
	if ip := offenderIn(recvErr(unix.SO_EE_ORIGIN_LOCAL, 11, unix.AF_INET, [4]byte{10, 0, 0, 1})); ip != nil {
		t.Fatalf("a local error names no router: got %v", ip)
	}
	if ip := offenderIn([]byte{1, 2, 3}); ip != nil {
		t.Fatal("a short message must give nothing")
	}
}

// The search for routers needs no rights and does not hang; what it finds depends on the network the test runs in.
func TestRouterHopsRuns(t *testing.T) {
	start := time.Now()
	hops := routerHops(3, 300*time.Millisecond)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	t.Logf("hops: %v", hops)
}
