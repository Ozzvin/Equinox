package core

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

// If the configured port is taken the client must still start, on another port, and say so.
func TestStartsOnAnotherPortWhenTheWantedOneIsTaken(t *testing.T) {
	blocker, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	taken := blocker.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	cfg, err := config.Load(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.Update(func(s *config.Settings) { s.ListenPort, s.PortMapping = taken, false })
	m, err := New(cfg, dir)
	if err != nil {
		t.Fatalf("the client must start although its port is taken: %v", err)
	}
	defer func() { m.Close(); waitFor(t, func() bool { return true }) }()

	if m.Port() == taken || m.Port() == 0 {
		t.Fatalf("expected another port, got %d (taken: %d)", m.Port(), taken)
	}
	if rep := m.PortReport(); rep.WantedPort != taken || rep.Port != m.Port() {
		t.Fatalf("the report must say which port was wanted: %+v", rep)
	}
	if ri := m.RestartInfo(); ri.Required {
		t.Fatalf("this is not something a restart would fix by itself: %+v", ri)
	}
}
