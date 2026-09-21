package portmap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fake is a router whose mappings we can wipe, like after a reboot.
type fake struct {
	name string
	mu   sync.Mutex
	maps map[string]bool
	fail bool
	adds int
}

func (f *fake) Name() string { return f.name }
func (f *fake) Map(_ context.Context, proto string, port int, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", errors.New("router unreachable")
	}
	f.maps[proto] = true
	f.adds++
	return "203.0.113.5", nil
}
func (f *fake) Exists(_ context.Context, proto string, _ int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maps[proto], nil
}
func (f *fake) Unmap(_ context.Context, proto string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.maps, proto)
	return nil
}

func waitStatus(t *testing.T, m *Manager, cond func(Status) bool) Status {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s := m.Status(); cond(s) {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met, status: %+v", m.Status())
	return Status{}
}

func TestRecoversAfterRouterReboot(t *testing.T) {
	r := &fake{name: "fake", maps: map[string]bool{}}
	m := New(51413, Options{CheckInterval: 20 * time.Millisecond, MinBackoff: 10 * time.Millisecond}, r)
	m.Start()
	defer m.Stop()

	s := waitStatus(t, m, func(s Status) bool { return s.Mapped })
	if s.ExternalIP != "203.0.113.5" || s.Method != "fake" {
		t.Fatalf("unexpected status %+v", s)
	}

	// Router reboots: all mappings vanish. The manager must notice and re-create them.
	r.mu.Lock()
	r.maps = map[string]bool{}
	r.mu.Unlock()
	waitStatus(t, m, func(s Status) bool { return s.Remaps >= 1 })
	r.mu.Lock()
	ok := r.maps["TCP"] && r.maps["UDP"]
	r.mu.Unlock()
	if !ok {
		t.Fatal("mapping was not restored")
	}
}

func TestFailureThenRecovery(t *testing.T) {
	r := &fake{name: "fake", maps: map[string]bool{}, fail: true}
	m := New(51413, Options{CheckInterval: 20 * time.Millisecond, MinBackoff: 10 * time.Millisecond}, r)
	m.Start()
	defer m.Stop()

	s := waitStatus(t, m, func(s Status) bool { return s.Failures >= 2 })
	if s.Mapped || s.LastError == "" {
		t.Fatalf("failure not reported: %+v", s)
	}
	r.mu.Lock()
	r.fail = false
	r.mu.Unlock()
	waitStatus(t, m, func(s Status) bool { return s.Mapped && s.Failures == 0 })
}

func TestFallsBackToSecondMapper(t *testing.T) {
	bad := &fake{name: "bad", maps: map[string]bool{}, fail: true}
	good := &fake{name: "good", maps: map[string]bool{}}
	m := New(51413, Options{CheckInterval: time.Hour}, bad, good)
	m.Start()
	defer m.Stop()
	s := waitStatus(t, m, func(s Status) bool { return s.Mapped })
	if s.Method != "good" {
		t.Fatalf("expected fallback to good, got %s", s.Method)
	}
}

func TestStopRemovesMapping(t *testing.T) {
	r := &fake{name: "fake", maps: map[string]bool{}}
	m := New(51413, Options{CheckInterval: time.Hour}, r)
	m.Start()
	waitStatus(t, m, func(s Status) bool { return s.Mapped })
	m.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.maps) != 0 {
		t.Fatalf("mappings left on router: %v", r.maps)
	}
}
