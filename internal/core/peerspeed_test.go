package core

import (
	"fmt"
	"testing"
	"time"
)

// The gone connections are dropped in a sweep that runs twice per forgetting period, not on every call: the
// peers tab calls peerSpeed once per connection, so a full walk each time grows with the square of their number.
func TestPeerSpeedSweepsGoneConnectionsPeriodically(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	old := time.Now().Add(-2 * peerForget)
	m.peerMu.Lock()
	m.peerRates["h|old"], m.peerSeen["h|old"] = &peerRate{}, old
	m.peerSweep = time.Now() // a sweep has just been done
	m.peerMu.Unlock()

	m.peerSpeed("h", "live", 0, 0)
	m.peerMu.Lock()
	_, kept := m.peerSeen["h|old"]
	m.peerMu.Unlock()
	if !kept {
		t.Fatal("swept again right after a sweep")
	}

	m.peerMu.Lock()
	m.peerSweep = time.Now().Add(-peerForget)
	m.peerMu.Unlock()
	m.peerSpeed("h", "live", 0, 0)
	m.peerMu.Lock()
	defer m.peerMu.Unlock()
	if _, ok := m.peerSeen["h|old"]; ok {
		t.Fatal("a gone connection stayed in peerSeen")
	}
	if _, ok := m.peerRates["h|old"]; ok {
		t.Fatal("a gone connection stayed in peerRates")
	}
	if _, ok := m.peerSeen["h|live"]; !ok {
		t.Fatal("a live connection was dropped")
	}
}

func BenchmarkPeerSpeed500(b *testing.B) {
	m := &Manager{peerRates: map[string]*peerRate{}, peerSeen: map[string]time.Time{}}
	for i := 0; i < b.N; i++ {
		for p := 0; p < 500; p++ {
			m.peerSpeed("h", fmt.Sprint(p), int64(i), int64(i))
		}
	}
}
