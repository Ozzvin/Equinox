// Package portmap keeps the listening port reachable from the internet through the
// router (UPnP IGD, NAT-PMP). Unlike fire-and-forget mapping it renews the lease,
// verifies that the mapping still exists and re-creates it after a router reboot or an
// IP change, which is the usual reason a forwarded port "breaks after a while".
package portmap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Mapper is one port-forwarding protocol.
type Mapper interface {
	Name() string
	// Map creates or refreshes a mapping and returns the external IP.
	Map(ctx context.Context, proto string, port int, lease time.Duration) (extIP string, err error)
	// Exists reports whether the mapping is still present on the router. Protocols that
	// cannot be queried return (true, nil) and rely on regular renewal.
	Exists(ctx context.Context, proto string, port int) (bool, error)
	Unmap(ctx context.Context, proto string, port int) error
}

// Status describes the current state for the UI.
type Status struct {
	Enabled    bool      `json:"enabled"`
	Mapped     bool      `json:"mapped"`
	Method     string    `json:"method,omitempty"`
	Port       int       `json:"port"`
	ExternalIP string    `json:"externalIP,omitempty"`
	LastOK     time.Time `json:"lastOk,omitempty"`
	LastError  string    `json:"lastError,omitempty"`
	Failures   int       `json:"failures"` // consecutive failed cycles
	Remaps     int       `json:"remaps"`   // times a lost mapping had to be recreated
}

// Options tune timings; zero values pick defaults.
type Options struct {
	Lease         time.Duration // requested lease, default 1h
	CheckInterval time.Duration // verify/renew period, default 5m
	MinBackoff    time.Duration // after a failure, default 15s
	MaxBackoff    time.Duration // default 5m
}

func (o *Options) defaults() {
	if o.Lease == 0 {
		o.Lease = time.Hour
	}
	if o.CheckInterval == 0 {
		o.CheckInterval = 5 * time.Minute
	}
	if o.MinBackoff == 0 {
		o.MinBackoff = 15 * time.Second
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = 5 * time.Minute
	}
}

var protos = []string{"TCP", "UDP"}

// Manager runs the mapping loop.
type Manager struct {
	opts    Options
	mappers []Mapper
	port    int

	mu     sync.Mutex
	st     Status
	active Mapper // mapper that worked last, tried first

	kick   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a manager for port. Pass nil mappers for the default UPnP + NAT-PMP set.
func New(port int, opts Options, mappers ...Mapper) *Manager {
	opts.defaults()
	if len(mappers) == 0 {
		mappers = []Mapper{NewUPnP(), NewNATPMP()}
	}
	return &Manager{
		opts: opts, mappers: mappers, port: port,
		st:   Status{Enabled: true, Port: port},
		kick: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

// Start launches the background loop.
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.run(ctx)
}

// Stop ends the loop and removes the mapping from the router.
func (m *Manager) Stop() {
	m.cancel()
	<-m.done
	m.mu.Lock()
	a := m.active
	m.mu.Unlock()
	if a != nil {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		for _, p := range protos {
			_ = a.Unmap(ctx, p, m.port)
		}
	}
}

// Refresh asks for an immediate check (e.g. after a network change).
func (m *Manager) Refresh() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Status returns a snapshot.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *Manager) run(ctx context.Context) {
	defer close(m.done)
	backoff := time.Duration(0)
	for {
		err := m.cycle(ctx)

		var wait time.Duration
		m.mu.Lock()
		if err != nil {
			m.st.Mapped = false
			m.st.LastError = err.Error()
			m.st.Failures++
			if backoff == 0 {
				backoff = m.opts.MinBackoff
			} else if backoff *= 2; backoff > m.opts.MaxBackoff {
				backoff = m.opts.MaxBackoff
			}
			wait = backoff
		} else {
			m.st.Mapped, m.st.LastError, m.st.Failures = true, "", 0
			m.st.LastOK = time.Now()
			backoff = 0
			wait = m.opts.CheckInterval
		}
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-m.kick:
		case <-time.After(wait):
		}
	}
}

// cycle verifies the existing mapping and (re)creates it when needed. The lease is
// refreshed on every cycle, so it never runs out while the daemon is alive.
func (m *Manager) cycle(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	m.mu.Lock()
	order := append([]Mapper(nil), m.mappers...)
	if a := m.active; a != nil { // try the last working method first
		for i, x := range order {
			if x == a {
				order[0], order[i] = order[i], order[0]
			}
		}
	}
	m.mu.Unlock()

	var errs []error
	for _, mp := range order {
		ip, remapped, err := m.tryMapper(cctx, mp)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", mp.Name(), err))
			continue
		}
		m.mu.Lock()
		m.active = mp
		m.st.Method, m.st.ExternalIP = mp.Name(), ip
		if remapped {
			m.st.Remaps++
		}
		m.mu.Unlock()
		return nil
	}
	return errors.Join(errs...)
}

func (m *Manager) tryMapper(ctx context.Context, mp Mapper) (ip string, remapped bool, err error) {
	for _, proto := range protos {
		if ok, err := mp.Exists(ctx, proto, m.port); err == nil && !ok {
			remapped = true // it was there before and is gone now
		}
		ip, err = mp.Map(ctx, proto, m.port, m.opts.Lease)
		if err != nil {
			return "", false, fmt.Errorf("%s %d: %w", proto, m.port, err)
		}
	}
	m.mu.Lock()
	first := m.st.LastOK.IsZero()
	m.mu.Unlock()
	return ip, remapped && !first, nil
}
