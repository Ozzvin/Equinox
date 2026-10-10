//go:build !linux

package portmap

import (
	"net"
	"time"
)

// routerHops finds routers on the way out only on Linux (see hops_linux.go), where the program runs in a container
// whose default gateway is not the home router. Elsewhere the default gateway is the router.
func routerHops(max int, wait time.Duration) []net.IP { return nil }
