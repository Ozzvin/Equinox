package core

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
)

const (
	maxManualPeers  = 100              // addresses in one request
	maxPerHostname  = 4                // addresses taken from one name
	resolveDeadline = 10 * time.Second // for all the names of one request together
)

// PeerAddrError says why one of the given addresses was not used. Reason is one of "format" (not host:port),
// "port" (not 1..65535), "address" (unusable, such as 0.0.0.0 or a multicast address) and "resolve" (the name
// could not be looked up); the interface words it.
type PeerAddrError struct {
	Addr   string `json:"addr"`
	Reason string `json:"reason"`
}

// PeerAddResult is what AddPeers did with the addresses it was given.
type PeerAddResult struct {
	Added  int             `json:"added"`  // new to the torrent: the engine will try to connect to them
	Known  int             `json:"known"`  // fine, but the torrent has them already (or the engine dropped them)
	Errors []PeerAddrError `json:"errors"` // the ones that could not be used
}

// AddPeers hands peers the user knows about to a torrent: "ip:port", "[ipv6]:port" or "name:port" (the name is
// looked up now). They are only tried for the running session; nothing is remembered across a restart. Peers
// of a private torrent are fine too: the user asked for these ones, they are not found by DHT or PEX.
func (m *Manager) AddPeers(hash string, raw []string) (PeerAddResult, error) {
	t, err := m.get(hash)
	if err != nil {
		return PeerAddResult{}, err
	}
	if len(raw) == 0 || len(raw) > maxManualPeers {
		return PeerAddResult{}, fmt.Errorf("%w: give from 1 to %d peer addresses", ErrInvalidInput, maxManualPeers)
	}
	res := PeerAddResult{Errors: []PeerAddrError{}}
	ctx, cancel := context.WithTimeout(context.Background(), resolveDeadline)
	defer cancel()
	seen := map[string]bool{}
	var infos []torrent.PeerInfo
	for _, r := range raw {
		addrs, reason := resolvePeerAddr(ctx, r)
		if reason != "" {
			res.Errors = append(res.Errors, PeerAddrError{Addr: strings.TrimSpace(r), Reason: reason})
			continue
		}
		for _, a := range addrs {
			if seen[a] {
				continue
			}
			seen[a] = true
			infos = append(infos, torrent.PeerInfo{Addr: torrent.StringAddr(a), Source: torrent.PeerSourceDirect, Trusted: true})
		}
	}
	if len(infos) > 0 {
		res.Added = t.AddPeers(infos)
		res.Known = len(infos) - res.Added
	}
	return res, nil
}

// resolvePeerAddr turns what the user typed into one or more "ip:port" strings, or says why not.
func resolvePeerAddr(ctx context.Context, raw string) (addrs []string, reason string) {
	raw = strings.TrimSpace(raw)
	host, portStr, err := net.SplitHostPort(raw)
	if err != nil || host == "" {
		return nil, "format"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, "port"
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.Zone() != "" || !usablePeerIP(ip) {
			return nil, "address"
		}
		return []string{netip.AddrPortFrom(ip, uint16(port)).String()}, ""
	}
	if !validHostname(host) {
		return nil, "format"
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, "resolve"
	}
	for _, ip := range ips {
		if ip = ip.Unmap(); usablePeerIP(ip) {
			addrs = append(addrs, netip.AddrPortFrom(ip, uint16(port)).String())
			if len(addrs) == maxPerHostname {
				break
			}
		}
	}
	if len(addrs) == 0 {
		return nil, "resolve"
	}
	return addrs, ""
}

// usablePeerIP allows any address a peer can really have, loopback and the local network included (a seedbox
// at home is the usual reason to add a peer by hand), but not the ones that cannot be a peer.
func usablePeerIP(ip netip.Addr) bool {
	return ip.IsValid() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalMulticast()
}

func validHostname(h string) bool {
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_':
		default:
			return false
		}
	}
	return true
}
