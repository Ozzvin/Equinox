package core

import (
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
