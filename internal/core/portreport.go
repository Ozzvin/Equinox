package core

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
	"github.com/Ozzvin/equinox/internal/portmap"
)

// Verdicts of the port report.
const (
	PortOpen     = "open"     // a peer from the internet connected to us
	PortMapped   = "mapped"   // router mapping is in place, no inbound peer seen yet
	PortCGNAT    = "cgnat"    // the router itself has a non-public address: forwarding is impossible
	PortClosed   = "closed"   // automatic forwarding is on but the router does not cooperate
	PortManual   = "manual"   // automatic forwarding is off
	PortChecking = "checking" // first check in progress
)

// inboundFresh is how long an inbound connection counts as proof of reachability.
const inboundFresh = 24 * time.Hour

// PortReport combines the router mapping with real inbound evidence.
type PortReport struct {
	portmap.Status
	Verdict string `json:"verdict"`
	Advice  string `json:"advice"`
	// AdviceCode and AdviceArgs say the advice for a client that words it itself (see Coder).
	AdviceCode  string    `json:"adviceCode"`
	AdviceArgs  []string  `json:"adviceArgs,omitempty"`
	Inbound     int64     `json:"inbound"`     // inbound connections from the internet since start
	LastInbound time.Time `json:"lastInbound"` // zero if none yet
	WantedPort  int       `json:"wantedPort"`  // the configured port when the engine had to use another one, else 0
	// PublicIP and PublicIPv6 are our address as the outside sees it: what the peers report, or else what
	// the router says when that is a public one. Empty until something tells.
	PublicIP   string `json:"publicIP,omitempty"`
	PublicIPv6 string `json:"publicIPv6,omitempty"`
}

// PortReport returns the current reachability verdict with advice for the user.
func (m *Manager) PortReport() PortReport {
	_, public, last, unknown := m.inbound.snapshot()
	st := m.PortStatus()
	r := makeReport(st, public, last, unknown, time.Now(), m.wantedPort)
	r.PublicIP, r.PublicIPv6 = m.self.best()
	if r.PublicIP == "" && st.ExternalIP != "" && !externalIPNotPublic(st.ExternalIP) {
		r.PublicIP = st.ExternalIP
	}
	return r
}

func makeReport(st portmap.Status, public int64, last time.Time, unknown bool, now time.Time, wanted int) PortReport {
	r := PortReport{Status: st, Inbound: public, LastInbound: last}
	if wanted != 0 && wanted != st.Port {
		r.WantedPort = wanted
	}

	switch {
	case public > 0 && now.Sub(last) < inboundFresh:
		r.Verdict = PortOpen
		r.Advice, r.AdviceCode = "К вам подключаются пиры из интернета — порт доступен.", "port.open"
	case st.ExternalIP != "" && externalIPNotPublic(st.ExternalIP):
		r.Verdict = PortCGNAT
		r.Advice = fmt.Sprintf("Внешний адрес роутера %s не публичный: провайдер использует общий адрес (CGNAT). "+
			"Проброс порта в таком случае невозможен. Попросите у провайдера «белый» IP или используйте VPN с пробросом порта.", st.ExternalIP)
		r.AdviceCode, r.AdviceArgs = "port.cgnat", []string{st.ExternalIP}
	case !st.Enabled:
		r.Verdict = PortManual
		r.Advice, r.AdviceCode = "Автопроброс выключен. Включите его или пробросьте порт на роутере вручную.", "port.manual"
	case st.Mapped:
		r.Verdict = PortMapped
		r.Advice, r.AdviceCode = "Порт проброшен на роутере. Ждём первого входящего подключения — при малом числе раздач это может занять время.", "port.mapped"
		if unknown {
			r.Advice, r.AdviceCode = "Порт проброшен на роутере (входящие подключения определить не удалось).", "port.mapped_unknown"
		}
	case st.Failures > 0:
		r.Verdict = PortClosed
		r.Advice, r.AdviceCode = "Роутер не отвечает на запросы проброса. Включите UPnP или NAT-PMP в настройках роутера либо пробросьте порт вручную.", "port.closed"
		if st.LastError != "" {
			r.Advice += " Ответ: " + st.LastError
			r.AdviceCode, r.AdviceArgs = "port.closed_answer", []string{st.LastError}
		}
	default:
		r.Verdict = PortChecking
		r.Advice, r.AdviceCode = "Проверяем роутер…", "port.checking"
	}
	return r
}

func externalIPNotPublic(s string) bool {
	ip, err := netip.ParseAddr(s)
	return err == nil && !isPublicIP(ip)
}

// SetPortMapping turns automatic router forwarding on or off without a restart.
func (m *Manager) SetPortMapping(on bool) error {
	if err := m.cfg.Update(func(s *config.Settings) { s.PortMapping = on }); err != nil {
		return err
	}
	m.portCtl.Lock()
	defer m.portCtl.Unlock()
	if !on {
		m.stopPortMappingLocked()
		return nil
	}
	m.portMu.Lock()
	defer m.portMu.Unlock()
	if m.ports == nil {
		m.ports = portmap.New(m.cl.LocalPort(), portmap.Options{})
		m.ports.Start()
	}
	return nil
}

func (m *Manager) stopPortMapping() {
	m.portCtl.Lock()
	defer m.portCtl.Unlock()
	m.stopPortMappingLocked()
}

// stopPortMappingLocked takes the mapping off the router, which can last up to 10 s. The pointer is cleared
// first, under portMu, and Stop runs after it is released: PortStatus and RefreshPort (the interface asks for
// them every 1.5 s) must not wait for the router. portCtl, held by the caller, keeps a new mapping from
// starting while the old one is still being removed.
func (m *Manager) stopPortMappingLocked() {
	m.portMu.Lock()
	p := m.ports
	m.ports = nil
	m.portMu.Unlock()
	if p != nil {
		p.Stop()
	}
}
