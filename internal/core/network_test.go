package core

import (
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

func TestNetworkValidation(t *testing.T) {
	ok := config.Default("x").Network
	if err := ValidateNetwork(ok); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	bad := map[string]func(*config.Network){
		"zero connections":        func(n *config.Network) { n.MaxConnsPerTorrent = 0 },
		"too many connections":    func(n *config.Network) { n.MaxConnsPerTorrent = 5000 },
		"zero attempts":           func(n *config.Network) { n.MaxHalfOpenPerTorrent = 0 },
		"no transport at all":     func(n *config.Network) { n.TCP, n.UTP = false, false },
		"unknown encryption mode": func(n *config.Network) { n.Encryption = "maybe" },
	}
	for name, mut := range bad {
		n := ok
		mut(&n)
		if ValidateNetwork(n) == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	n := ok
	n.TCP = false // uTP alone is a legitimate choice
	if err := ValidateNetwork(n); err != nil {
		t.Errorf("uTP-only must be allowed: %v", err)
	}
}

func TestConnectionLimitsApplyLive(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "conn.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	tor, _ := m.get(hash)
	// SetMaxEstablishedConns returns the previous limit, which is the only way to read it.
	limit := func() int { return tor.SetMaxEstablishedConns(1000) }
	put := func(n int) { tor.SetMaxEstablishedConns(n) }

	if got := limit(); got != 50 {
		t.Fatalf("new torrents must start with the default limit, got %d", got)
	}
	put(50)

	n := m.cfg.Get().Network
	n.MaxConnsPerTorrent = 7
	if err := m.SetNetwork(n); err != nil {
		t.Fatal(err)
	}
	if got := limit(); got != 7 {
		t.Fatalf("global limit not applied live: %d", got)
	}
	put(7)

	if err := m.SetTorrentMaxConns(hash, 3); err != nil {
		t.Fatal(err)
	}
	if got := limit(); got != 3 {
		t.Fatalf("own limit must win: %d", got)
	}
	put(3)
	if d, _ := m.Details(hash); d.MaxConns != 3 || d.ConnLimit != 3 {
		t.Fatalf("details must report both limits: %+v", d)
	}
	if err := m.SetTorrentMaxConns(hash, 0); err != nil { // back to the global one
		t.Fatal(err)
	}
	if got := limit(); got != 7 {
		t.Fatalf("0 must mean the global setting: %d", got)
	}
	if err := m.SetTorrentMaxConns(hash, 5000); err == nil {
		t.Fatal("absurd limit must be rejected")
	}
}

func TestRestartInfoTracksStartTimeSettings(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	if ri := m.RestartInfo(); ri.Required {
		t.Fatalf("nothing changed yet: %+v", ri)
	}
	n := m.cfg.Get().Network
	n.MaxConnsPerTorrent = 9 // a live setting: no restart
	if err := m.SetNetwork(n); err != nil {
		t.Fatal(err)
	}
	if ri := m.RestartInfo(); ri.Required {
		t.Fatalf("a live setting must not ask for a restart: %+v", ri)
	}
	n.DHT, n.Encryption = false, config.EncryptionRequire
	if err := m.SetNetwork(n); err != nil {
		t.Fatal(err)
	}
	ri := m.RestartInfo()
	if !ri.Required || len(ri.Reasons) != 2 || ri.Reasons[0] != "dht" || ri.Reasons[1] != "encryption" {
		t.Fatalf("expected dht+encryption: %+v", ri)
	}
	n.DHT, n.Encryption = true, config.EncryptionPrefer // undoing the changes clears the request
	_ = m.SetNetwork(n)
	if ri := m.RestartInfo(); ri.Required {
		t.Fatalf("back to the running values: %+v", ri)
	}
	if err := m.SetListenPort(m.Port() + 1); err != nil {
		t.Fatal(err)
	}
	if ri := m.RestartInfo(); !ri.Required || ri.Reasons[0] != "port" {
		t.Fatalf("a new listen port needs a restart: %+v", ri)
	}
}

func TestNetworkSettingsReachTheEngine(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) {
		s.Network.DHT = false
		s.Network.Encryption = config.EncryptionRequire
	})
	if n := len(m.cl.DhtServers()); n != 0 {
		t.Fatalf("DHT is off but the engine runs %d DHT server(s)", n)
	}
	if !headerPolicy(config.EncryptionRequire).RequirePreferred || headerPolicy(config.EncryptionOff).Preferred {
		t.Fatal("encryption modes map to the wrong policy")
	}
}

// A settings file edited by hand into nonsense must not stop the engine from starting.
func TestInvalidNetworkSettingsFallBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.Network.MaxConnsPerTorrent = 0 })
	if got := m.cfg.Get().Network.MaxConnsPerTorrent; got != 50 {
		t.Fatalf("invalid value must be replaced by the default, got %d", got)
	}
}
