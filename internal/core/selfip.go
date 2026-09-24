package core

import (
	"net"
	"net/netip"
	"sync"
)

// selfIPWindow is how many of the latest reports the address is voted from.
const selfIPWindow = 30

// selfIPs learns our own address from the peers: the extended handshake of BitTorrent carries "yourip",
// the address the peer sees us at. That is the real public address whatever the router does, unlike the
// router's own answer, which only exists while port forwarding is on and can be a private address behind
// the provider's NAT. A single peer may be wrong, so the address most often reported lately wins.
type selfIPs struct {
	mu     sync.Mutex
	recent []netip.Addr
}

// note takes one report; addresses that cannot be ours from the outside (private, loopback...) are dropped.
func (s *selfIPs) note(ip net.IP) {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return
	}
	a = a.Unmap()
	if !isPublicIP(a) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recent = append(s.recent, a)
	if len(s.recent) > selfIPWindow {
		s.recent = s.recent[len(s.recent)-selfIPWindow:]
	}
}

// best returns the most reported IPv4 and IPv6 address ("" when none was reported).
func (s *selfIPs) best() (v4, v6 string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := map[netip.Addr]int{}
	for _, a := range s.recent {
		count[a]++
	}
	var n4, n6 int
	for a, n := range count {
		if a.Is4() {
			if n > n4 || (n == n4 && a.String() < v4) {
				n4, v4 = n, a.String()
			}
		} else if n > n6 || (n == n6 && a.String() < v6) {
			n6, v6 = n, a.String()
		}
	}
	return v4, v6
}
