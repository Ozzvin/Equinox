package core

import "time"

// Traffic by period, for the statistics window: the engine's counters only say how much a torrent moved in this
// session, and the records keep its lifetime total, so the bytes moved are also put into hourly buckets kept in the
// state file for trafficKeep. The day, week and month are the last 24 hours, 7 and 30 days of those buckets.

const trafficKeep = 32 * 24 // hours of buckets kept, a little over the longest period shown

// trafficHour is the traffic of all torrents in one hour (Hour is the Unix time divided by 3600).
type trafficHour struct {
	Hour int64 `json:"hour"`
	Down int64 `json:"down"`
	Up   int64 `json:"up"`
}

// Traffic is the payload moved in one period.
type Traffic struct {
	Down int64 `json:"down"`
	Up   int64 `json:"up"`
}

// TrafficStats is the traffic of the periods the statistics window offers.
type TrafficStats struct {
	Session Traffic   `json:"session"`
	Day     Traffic   `json:"day"`
	Week    Traffic   `json:"week"`
	Month   Traffic   `json:"month"`
	Since   time.Time `json:"since"`        // the first hour on record: a period that reaches further back is partly unknown
	Started time.Time `json:"sessionStart"` // when this session began
}

// addTraffic records the bytes moved since the last tick.
func (m *Manager) addTraffic(now time.Time, down, up int64) {
	if down <= 0 && up <= 0 {
		return
	}
	down, up = max(down, 0), max(up, 0)
	m.mu.Lock()
	m.session.Down += down
	m.session.Up += up
	m.mu.Unlock()
	hour := now.Unix() / 3600
	m.state.touch(func(s *state) {
		// the buckets stay sorted by hour; a clock set back may bring an earlier hour
		i := len(s.Traffic)
		for i > 0 && s.Traffic[i-1].Hour > hour {
			i--
		}
		if i > 0 && s.Traffic[i-1].Hour == hour {
			s.Traffic[i-1].Down += down
			s.Traffic[i-1].Up += up
		} else {
			s.Traffic = append(s.Traffic, trafficHour{})
			copy(s.Traffic[i+1:], s.Traffic[i:])
			s.Traffic[i] = trafficHour{Hour: hour, Down: down, Up: up}
		}
		newest := s.Traffic[len(s.Traffic)-1].Hour
		kept := s.Traffic[:0]
		for _, b := range s.Traffic {
			if b.Hour > newest-trafficKeep {
				kept = append(kept, b)
			}
		}
		s.Traffic = kept
	})
}

// TrafficStats sums the buckets up for the session, the last day, week and month.
func (m *Manager) TrafficStats(now time.Time) TrafficStats {
	m.mu.Lock()
	out := TrafficStats{Session: m.session, Started: m.started}
	m.mu.Unlock()
	hour := now.Unix() / 3600
	m.state.view(func(s *state) {
		if len(s.Traffic) > 0 {
			out.Since = time.Unix(s.Traffic[0].Hour*3600, 0)
		}
		for _, b := range s.Traffic {
			age := hour - b.Hour // 0 = this hour
			if age < 0 {
				continue // a clock that went back
			}
			if age < 24 {
				out.Day.Down += b.Down
				out.Day.Up += b.Up
			}
			if age < 7*24 {
				out.Week.Down += b.Down
				out.Week.Up += b.Up
			}
			if age < 30*24 {
				out.Month.Down += b.Down
				out.Month.Up += b.Up
			}
		}
	})
	return out
}
