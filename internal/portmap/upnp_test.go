package portmap

import (
	"context"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/internetgateway2"
	"github.com/huin/goupnp/soap"
)

// A router that takes the connection and never answers must not hold the mapping, nor turning it off: every request
// gives up with its context.
func TestSilentUPnPRouterDoesNotHang(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // read nothing, answer nothing
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	}()

	ctl, _ := url.Parse("http://" + ln.Addr().String() + "/ctl")
	dev := &internetgateway2.WANIPConnection2{ServiceClient: goupnp.ServiceClient{SOAPClient: soap.NewSOAPClient(*ctl), Location: ctl}}
	u := &UPnP{dev: dev, host: ln.Addr().String(), foundAt: time.Now()}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := u.Map(ctx, "TCP", 51413, time.Hour); err == nil {
		t.Fatal("a silent router cannot have mapped the port")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Map waited %v for a silent router", d)
	}

	u.dev, u.foundAt = dev, time.Now() // Map forgets the router after a failure
	m := New(51413, Options{CheckInterval: time.Hour}, u)
	m.Start()
	time.Sleep(200 * time.Millisecond) // the loop is in its request now
	start = time.Now()
	m.Stop()
	if d := time.Since(start); d > 3*time.Second { // the request stops with the loop's context, not by Stop's own limit
		t.Fatalf("Stop waited %v", d)
	}
}
