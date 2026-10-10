package portmap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackpal/gateway"
	natpmp "github.com/jackpal/go-nat-pmp"
)

// NATPMP implements Mapper on top of NAT-PMP (also answered by PCP-capable routers).
type NATPMP struct {
	router func() string // the router's address from the settings, "" = find it

	mu sync.Mutex
	gw net.IP // the router that answered last, asked first
}

func NewNATPMP() *NATPMP { return &NATPMP{} }

// NewNATPMPWith asks the router at the address router returns first (read each time, so a change in the settings
// applies at once); with "" it finds the router itself.
func NewNATPMPWith(router func() string) *NATPMP { return &NATPMP{router: router} }

func (n *NATPMP) Name() string { return "NAT-PMP" }

// client returns a client for the router that answers. It is the default gateway on a PC; in a container (Umbrel,
// Docker) the default gateway is the host's bridge, which does not answer, so the routers on the way out are tried
// as well (see routerHops). The address given in the settings comes before both.
func (n *NATPMP) client() (*natpmp.Client, error) {
	n.mu.Lock()
	gw := n.gw
	n.mu.Unlock()
	if gw != nil {
		return natpmp.NewClientWithTimeout(gw, 3*time.Second), nil
	}
	var tried []string
	for _, c := range n.candidates() {
		cl := natpmp.NewClientWithTimeout(c, 2*time.Second)
		if _, err := cl.GetExternalAddress(); err != nil {
			tried = append(tried, c.String())
			continue
		}
		n.mu.Lock()
		n.gw = c
		n.mu.Unlock()
		return natpmp.NewClientWithTimeout(c, 3*time.Second), nil
	}
	if len(tried) == 0 {
		return nil, errors.New("no router found")
	}
	return nil, fmt.Errorf("no NAT-PMP answer from %s", strings.Join(tried, ", "))
}

// forget drops the router that answered before, so the next request looks for it again (after a failure: the
// router may have changed, or got another address).
func (n *NATPMP) forget() {
	n.mu.Lock()
	n.gw = nil
	n.mu.Unlock()
}

func (n *NATPMP) candidates() []net.IP {
	var out []net.IP
	add := func(ip net.IP) {
		if ip = ip.To4(); ip == nil {
			return
		}
		for _, o := range out {
			if o.Equal(ip) {
				return
			}
		}
		out = append(out, ip)
	}
	if n.router != nil {
		add(net.ParseIP(strings.TrimSpace(n.router())))
	}
	gw, _ := gateway.DiscoverGateway() // re-discovered every time: cheap, survives router changes
	add(gw)
	for _, h := range routerHops(4, 700*time.Millisecond) {
		if h.IsPrivate() { // a router of the home network; past it is the provider's
			add(h)
		}
	}
	return out
}

func (n *NATPMP) Map(ctx context.Context, proto string, port int, lease time.Duration) (string, error) {
	c, err := n.client()
	if err != nil {
		return "", err
	}
	res, err := c.AddPortMapping(strings.ToLower(proto), port, port, int(lease/time.Second))
	if err != nil {
		n.forget()
		return "", err
	}
	if int(res.MappedExternalPort) != port {
		// BitTorrent peers must reach exactly the announced port.
		return "", fmt.Errorf("router mapped external port %d instead of %d", res.MappedExternalPort, port)
	}
	ext, err := c.GetExternalAddress()
	if err != nil {
		return "", err
	}
	b := ext.ExternalIPAddress
	return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]), nil
}

// NAT-PMP has no query operation; regular lease renewal keeps the mapping alive.
func (n *NATPMP) Exists(ctx context.Context, proto string, port int) (bool, error) {
	return true, nil
}

func (n *NATPMP) Unmap(ctx context.Context, proto string, port int) error {
	c, err := n.client()
	if err != nil {
		return err
	}
	_, err = c.AddPortMapping(strings.ToLower(proto), port, 0, 0)
	return err
}
