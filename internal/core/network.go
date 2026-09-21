package core

import (
	"fmt"
	"strings"

	"github.com/anacrolix/torrent"

	"github.com/Ozzvin/equinox/internal/config"
)

// ValidateNetwork checks connection settings coming from the user.
func ValidateNetwork(n config.Network) error {
	switch {
	case n.MaxConnsPerTorrent < 1 || n.MaxConnsPerTorrent > 1000:
		return fmt.Errorf("%w: connections per torrent must be between 1 and 1000", ErrInvalidInput)
	case n.MaxHalfOpenPerTorrent < 1 || n.MaxHalfOpenPerTorrent > 500:
		return fmt.Errorf("%w: simultaneous attempts must be between 1 and 500", ErrInvalidInput)
	case !n.TCP && !n.UTP:
		return fmt.Errorf("%w: at least one of TCP and uTP must stay on", ErrInvalidInput)
	}
	switch n.Encryption {
	case config.EncryptionPrefer, config.EncryptionRequire, config.EncryptionOff:
	default:
		return fmt.Errorf("%w: unknown encryption mode %q", ErrInvalidInput, n.Encryption)
	}
	return nil
}

// headerPolicy maps the encryption setting to the engine's header obfuscation policy.
func headerPolicy(mode string) torrent.HeaderObfuscationPolicy {
	switch mode {
	case config.EncryptionRequire:
		return torrent.HeaderObfuscationPolicy{Preferred: true, RequirePreferred: true}
	case config.EncryptionOff:
		return torrent.HeaderObfuscationPolicy{Preferred: false, RequirePreferred: false}
	}
	return torrent.HeaderObfuscationPolicy{Preferred: true, RequirePreferred: false}
}

// applyNetwork copies the start-time connection settings into the engine config.
func applyNetwork(cc *torrent.ClientConfig, n config.Network) {
	cc.EstablishedConnsPerTorrent = n.MaxConnsPerTorrent
	cc.HalfOpenConnsPerTorrent = n.MaxHalfOpenPerTorrent
	cc.NoDHT = !n.DHT
	cc.DisablePEX = !n.PEX
	cc.DisableUTP = !n.UTP
	cc.DisableTCP = !n.TCP
	cc.DisableIPv6 = !n.IPv6
	cc.DisableWebseeds = !n.Webseeds
	cc.AcceptPeerConnections = n.AcceptIncoming
	cc.HeaderObfuscationPolicy = headerPolicy(n.Encryption)
}

// SetNetwork stores new connection settings. The per-torrent connection limit takes
// effect immediately; everything else after the next start (see RestartInfo).
func (m *Manager) SetNetwork(n config.Network) error {
	if err := ValidateNetwork(n); err != nil {
		return err
	}
	if err := m.cfg.Update(func(s *config.Settings) { s.Network = n }); err != nil {
		return err
	}
	m.applyConnLimits()
	return nil
}

// connLimit is the effective connection limit of a torrent.
func (m *Manager) connLimit(rec record) int {
	if rec.MaxConns > 0 {
		return rec.MaxConns
	}
	return m.cfg.Get().Network.MaxConnsPerTorrent
}

// applyConnLimits pushes the connection limits to every torrent in the engine.
func (m *Manager) applyConnLimits() {
	recs := m.snapshotRecords()
	m.mu.Lock()
	ts := make([]*torrent.Torrent, 0, len(m.torrents))
	for _, t := range m.torrents {
		ts = append(ts, t)
	}
	m.mu.Unlock()
	for _, t := range ts {
		m.applyConnLimit(t, t.InfoHash().HexString(), recs[t.InfoHash().HexString()])
	}
}

// SetTorrentMaxConns limits the connections of one torrent (0 = use the global setting).
func (m *Manager) SetTorrentMaxConns(hash string, n int) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	if n < 0 || n > 1000 {
		return fmt.Errorf("%w: connection limit must be between 0 and 1000", ErrInvalidInput)
	}
	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.MaxConns = n
		}
	}); err != nil {
		return err
	}
	m.applyConnLimit(t, hash, m.snapshotRecords()[hash])
	return nil
}

// RestartInfo tells whether saved settings differ from what the running engine uses.
type RestartInfo struct {
	Required bool     `json:"required"`
	Reasons  []string `json:"reasons"` // dht, pex, utp, tcp, ipv6, webseeds, incoming, encryption, halfopen, port
}

// RestartInfo compares the saved start-time settings with the ones the engine started with.
func (m *Manager) RestartInfo() RestartInfo {
	s := m.cfg.Get()
	now, was := s.Network, m.startNet
	var why []string
	add := func(diff bool, key string) {
		if diff {
			why = append(why, key)
		}
	}
	add(now.DHT != was.DHT, "dht")
	add(now.PEX != was.PEX, "pex")
	add(now.UTP != was.UTP, "utp")
	add(now.TCP != was.TCP, "tcp")
	add(now.IPv6 != was.IPv6, "ipv6")
	add(now.Webseeds != was.Webseeds, "webseeds")
	add(now.AcceptIncoming != was.AcceptIncoming, "incoming")
	add(strings.TrimSpace(now.Encryption) != was.Encryption, "encryption")
	add(now.MaxHalfOpenPerTorrent != was.MaxHalfOpenPerTorrent, "halfopen")
	add(s.ListenPort != 0 && s.ListenPort != m.wantedPort, "port")
	return RestartInfo{Required: len(why) > 0, Reasons: why}
}
