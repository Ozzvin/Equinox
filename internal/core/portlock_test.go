package core

import (
	"testing"
	"time"
)

// Taking the mapping off the router can last seconds, and the interface asks for the port state every 1.5 s:
// while a stop is in progress (portCtl held) the status calls must still answer at once.
func TestPortStatusDoesNotWaitForAStoppingMapping(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	m.portCtl.Lock()
	defer m.portCtl.Unlock()

	done := make(chan struct{})
	go func() {
		m.PortStatus()
		m.RefreshPort()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PortStatus/RefreshPort wait for a mapping that is being stopped")
	}
}
