package core

import (
	"fmt"
	"time"
)

// Seeding time: how long a finished torrent has been shared. Like the ratio, it can stop the
// seeding when a limit is reached; whichever limit comes first wins. The time only runs while
// the torrent is complete and not paused, and it adds up across restarts.

// SetSeedTimeLimit sets the per-torrent limit in minutes (0 = use the global default).
func (m *Manager) SetSeedTimeLimit(hash string, minutes int) error {
	if minutes < 0 || minutes > 60*24*3650 {
		return errInvalid("the seeding time must be between 0 and 3650 days")
	}
	if _, err := m.get(hash); err != nil {
		return err
	}
	return m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.SeedTimeLimit = minutes
		}
	})
}

// seedLimit is the limit in force for a torrent, in minutes (0 = none).
func (m *Manager) seedLimit(r record) int {
	if r.SeedTimeLimit > 0 {
		return r.SeedTimeLimit
	}
	return m.cfg.Get().SeedTimeLimitMinutes
}

// seedSeconds is the seeding time of a torrent including what is not persisted yet.
func (m *Manager) seedSeconds(hash string, r record) int64 {
	m.mu.Lock()
	pend := m.seedPend[hash]
	m.mu.Unlock()
	return r.SeedSeconds + int64(pend/time.Second)
}

// addSeedTime counts dt of seeding time for a torrent and returns the total.
func (m *Manager) addSeedTime(hash string, r record, dt time.Duration) int64 {
	m.mu.Lock()
	m.seedPend[hash] += dt
	pend := m.seedPend[hash]
	m.mu.Unlock()
	return r.SeedSeconds + int64(pend/time.Second)
}

// flushSeedTime moves the counted time into the persisted records.
func (m *Manager) flushSeedTime() {
	m.mu.Lock()
	whole := map[string]int64{}
	for h, d := range m.seedPend {
		if s := int64(d / time.Second); s > 0 {
			whole[h] = s
			m.seedPend[h] = d - time.Duration(s)*time.Second
		}
	}
	m.mu.Unlock()
	if len(whole) == 0 {
		return
	}
	_ = m.state.with(func(s *state) {
		for h, secs := range whole {
			if r := s.Torrents[h]; r != nil {
				r.SeedSeconds += secs
			}
		}
	})
}

// forgetSeedTime drops the uncounted remainder of a removed torrent.
func (m *Manager) forgetSeedTime(hash string) {
	m.mu.Lock()
	delete(m.seedPend, hash)
	m.mu.Unlock()
}

func limitReason(kind string, v float64) string {
	if kind == "time" {
		return fmt.Sprintf("время раздачи %s", fmtMinutes(int(v)))
	}
	return fmt.Sprintf("рейтинг %.2f", v)
}

func fmtMinutes(min int) string {
	switch {
	case min%1440 == 0:
		return fmt.Sprintf("%d дн.", min/1440)
	case min%60 == 0:
		return fmt.Sprintf("%d ч", min/60)
	}
	return fmt.Sprintf("%d мин", min)
}
