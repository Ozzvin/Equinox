package core

import (
	"math/rand/v2"
	"net"
	"strconv"
)

// portFree reports whether both TCP and UDP can be bound on the port right now.
func portFree(port int) bool {
	addr := ":" + strconv.Itoa(port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	l.Close()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	pc.Close()
	return true
}

// sparePorts are the ports to try when the wanted one cannot be had, or none is set: up to n random ones, each found
// free for TCP and UDP first. Port 0 (any) is not used for this. On Windows the system hands out the next free TCP
// port in order, the engine then binds UDP on the same number, and a range that Windows keeps for itself (Hyper-V,
// WSL) refuses UDP: try after try lands on the next port of the same range and fails the same way (seen on the CI
// runner, where every engine of a test run failed to start from then on).
func sparePorts(n int) []int {
	var out []int
	for tries := 0; len(out) < n && tries < 20*n; tries++ {
		p := 20000 + rand.IntN(40000) // away from the well-known ports and the top of the range
		if portFree(p) {
			out = append(out, p)
		}
	}
	return out
}
