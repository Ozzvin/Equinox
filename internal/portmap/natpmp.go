package portmap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackpal/gateway"
	natpmp "github.com/jackpal/go-nat-pmp"
)

// NATPMP implements Mapper on top of NAT-PMP (also answered by PCP-capable routers).
type NATPMP struct{}

func NewNATPMP() *NATPMP { return &NATPMP{} }

func (n *NATPMP) Name() string { return "NAT-PMP" }

func (n *NATPMP) client() (*natpmp.Client, error) {
	gw, err := gateway.DiscoverGateway() // re-discovered every time: cheap, survives router changes
	if err != nil {
		return nil, fmt.Errorf("default gateway: %w", err)
	}
	c := natpmp.NewClientWithTimeout(gw, 3*time.Second)
	return c, nil
}

func (n *NATPMP) Map(ctx context.Context, proto string, port int, lease time.Duration) (string, error) {
	c, err := n.client()
	if err != nil {
		return "", err
	}
	res, err := c.AddPortMapping(strings.ToLower(proto), port, port, int(lease/time.Second))
	if err != nil {
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
