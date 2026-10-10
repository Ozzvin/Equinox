package portmap

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// fakeNATPMP answers NAT-PMP on 127.0.0.1:5351 like a router would: the external address, and every mapping
// granted as asked.
func fakeNATPMP(t *testing.T) (requests func() int) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5351})
	if err != nil {
		t.Skip("NAT-PMP port 5351 is taken here:", err)
	}
	n := make(chan int, 1)
	n <- 0
	go func() {
		buf := make([]byte, 64)
		for {
			k, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			v := <-n
			n <- v + 1
			if k < 2 || buf[0] != 0 {
				continue
			}
			switch op := buf[1]; {
			case op == 0 && k == 2:
				r := []byte{0, 128, 0, 0, 0, 0, 0, 1, 203, 0, 113, 5}
				_, _ = c.WriteToUDP(r, from)
			case (op == 1 || op == 2) && k == 12:
				r := make([]byte, 16)
				r[1] = 128 + op
				binary.BigEndian.PutUint32(r[4:], 1)
				copy(r[8:12], buf[4:8])   // internal and external port, as asked
				copy(r[12:16], buf[8:12]) // lifetime
				_, _ = c.WriteToUDP(r, from)
			}
		}
	}()
	t.Cleanup(func() { c.Close() })
	return func() int { v := <-n; n <- v; return v }
}

// The address given in the settings is asked first, before the default gateway (which in a container is the
// bridge of the host and does not answer), and the router that answered is kept for the next requests.
func TestNATPMPAsksTheRouterFromTheSettings(t *testing.T) {
	requests := fakeNATPMP(t)
	n := NewNATPMPWith(func() string { return " 127.0.0.1 " })
	ctx := context.Background()
	ip, err := n.Map(ctx, "TCP", 51420, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.5" {
		t.Fatalf("external address %q, want 203.0.113.5", ip)
	}
	if n.gw == nil || !n.gw.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("the router that answered is not kept: %v", n.gw)
	}
	before := requests()
	if _, err := n.Map(ctx, "UDP", 51420, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := requests() - before; got != 2 { // the mapping and the external address; no new search
		t.Fatalf("second mapping took %d requests, want 2", got)
	}
}

func TestNATPMPCandidates(t *testing.T) {
	n := NewNATPMPWith(func() string { return "192.168.50.1" })
	c := n.candidates()
	if len(c) == 0 || !c[0].Equal(net.IPv4(192, 168, 50, 1)) {
		t.Fatalf("the address from the settings must come first: %v", c)
	}
	for i := range c {
		for j := i + 1; j < len(c); j++ {
			if c[i].Equal(c[j]) {
				t.Fatalf("%v is tried twice: %v", c[i], c)
			}
		}
	}
	if c := NewNATPMPWith(func() string { return "not an address" }).candidates(); len(c) > 0 && c[0].String() == "not an address" {
		t.Fatal("a wrong address must be skipped")
	}
}
