//go:build linux

package portmap

import (
	"encoding/binary"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// routerHops finds the first few routers on the way out, the way traceroute does but with no special rights: a UDP
// datagram with a small TTL makes the router where it runs out answer "time exceeded", and with IP_RECVERR Linux
// hands that answer to an ordinary socket, with the router's address in it. In a container the first hop is the
// host (Docker's bridge), the next one the home router, which the container cannot see otherwise: its default
// gateway is the bridge, and NAT-PMP asked there gets no answer (found on Umbrel, 2026-10-10).
func routerHops(max int, wait time.Duration) []net.IP {
	var hops []net.IP
	for ttl := 1; ttl <= max; ttl++ {
		ip := hopAt(ttl, wait)
		if ip == nil {
			break // no answer: further hops would not answer either, or the way is not routed at all
		}
		hops = append(hops, ip)
		if !ip.IsPrivate() {
			break // out of the home network: the routers we can ask are behind
		}
	}
	return hops
}

// probeTarget is any address on the internet; the datagram never gets there, the TTL runs out on the way. A port
// from traceroute's range, where nothing listens.
var probeTarget = [4]byte{1, 1, 1, 1}

func hopAt(ttl int, wait time.Duration) net.IP {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(fd)
	if unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_TTL, ttl) != nil || unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_RECVERR, 1) != nil {
		return nil
	}
	if unix.Sendto(fd, []byte("equinox"), 0, &unix.SockaddrInet4{Port: 33434 + ttl, Addr: probeTarget}) != nil {
		return nil
	}
	buf, oob := make([]byte, 64), make([]byte, 512)
	for end := time.Now().Add(wait); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		_, oobn, _, _, err := unix.Recvmsg(fd, buf, oob, unix.MSG_ERRQUEUE)
		if err != nil {
			continue // nothing in the error queue yet
		}
		if ip := offender(oob[:oobn]); ip != nil {
			return ip
		}
	}
	return nil
}

// offender reads the address of the router that answered "time exceeded" from the control messages of a read from
// the error queue: a sock_extended_err, then the sockaddr_in of whoever sent the ICMP message.
func offender(oob []byte) net.IP {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	for _, m := range msgs {
		if m.Header.Level != unix.IPPROTO_IP || m.Header.Type != unix.IP_RECVERR {
			continue
		}
		return offenderIn(m.Data)
	}
	return nil
}

// offenderIn takes the bytes of one IP_RECVERR message. Only an ICMP "time exceeded" counts: an "unreachable"
// from the target itself would name the target, not a router on the way.
func offenderIn(d []byte) net.IP {
	const errLen = 16 // sizeof(struct sock_extended_err)
	if len(d) < errLen+8 {
		return nil
	}
	origin, icmpType := d[4], d[5]
	if origin != unix.SO_EE_ORIGIN_ICMP || icmpType != 11 { // 11: time exceeded
		return nil
	}
	sa := d[errLen:]
	if binary.NativeEndian.Uint16(sa[0:2]) != unix.AF_INET {
		return nil
	}
	return net.IPv4(sa[4], sa[5], sa[6], sa[7])
}
