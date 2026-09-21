package portmap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

// igd is the subset of WANIPConnection/WANPPPConnection that we use.
type igd interface {
	AddPortMapping(remoteHost string, extPort uint16, proto string, intPort uint16,
		intClient string, enabled bool, desc string, lease uint32) error
	GetExternalIPAddress() (string, error)
	GetSpecificPortMappingEntry(remoteHost string, extPort uint16, proto string) (
		intPort uint16, intClient string, enabled bool, desc string, lease uint32, err error)
	DeletePortMapping(remoteHost string, extPort uint16, proto string) error
}

// UPnP implements Mapper on top of UPnP Internet Gateway Device.
type UPnP struct {
	dev     igd
	host    string // router address, used to pick our local IP
	foundAt time.Time
}

func NewUPnP() *UPnP { return &UPnP{} }

func (u *UPnP) Name() string { return "UPnP" }

// discover finds the router. The result is cached for a while, but dropped after any
// error so a rebooted router with a new control URL is picked up again.
func (u *UPnP) discover() (igd, error) {
	if u.dev != nil && time.Since(u.foundAt) < 10*time.Minute {
		return u.dev, nil
	}
	u.dev = nil
	if c, _, _ := internetgateway2.NewWANIPConnection2Clients(); len(c) > 0 {
		u.dev, u.host = c[0], c[0].Location.Host
	} else if c, _, _ := internetgateway2.NewWANIPConnection1Clients(); len(c) > 0 {
		u.dev, u.host = c[0], c[0].Location.Host
	} else if c, _, _ := internetgateway2.NewWANPPPConnection1Clients(); len(c) > 0 {
		u.dev, u.host = c[0], c[0].Location.Host
	}
	if u.dev == nil {
		return nil, errors.New("no UPnP gateway found")
	}
	u.foundAt = time.Now()
	return u.dev, nil
}

func (u *UPnP) Map(ctx context.Context, proto string, port int, lease time.Duration) (string, error) {
	d, err := u.discover()
	if err != nil {
		return "", err
	}
	local, err := localIPFor(u.host)
	if err != nil {
		u.dev = nil
		return "", err
	}
	desc := "equinox"
	secs := uint32(lease / time.Second)
	err = d.AddPortMapping("", uint16(port), proto, uint16(port), local, true, desc, secs)
	if err != nil && strings.Contains(err.Error(), "725") { // OnlyPermanentLeasesSupported
		err = d.AddPortMapping("", uint16(port), proto, uint16(port), local, true, desc, 0)
	}
	if err != nil {
		u.dev = nil
		return "", err
	}
	ip, err := d.GetExternalIPAddress()
	if err != nil {
		u.dev = nil
		return "", err
	}
	return ip, nil
}

func (u *UPnP) Exists(ctx context.Context, proto string, port int) (bool, error) {
	d, err := u.discover()
	if err != nil {
		return false, err
	}
	_, _, enabled, _, _, err := d.GetSpecificPortMappingEntry("", uint16(port), proto)
	if err != nil {
		return false, nil // 714 NoSuchEntryInArray etc.: treat as missing
	}
	return enabled, nil
}

func (u *UPnP) Unmap(ctx context.Context, proto string, port int) error {
	d, err := u.discover()
	if err != nil {
		return err
	}
	return d.DeletePortMapping("", uint16(port), proto)
}

// localIPFor returns our address on the interface that routes to host (host:port).
func localIPFor(hostport string) (string, error) {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	c, err := net.DialTimeout("udp", net.JoinHostPort(h, "1900"), 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("local address: %w", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}
