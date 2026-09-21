package core

import (
	"fmt"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

// parseClock turns "HH:MM" into minutes since midnight.
func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%w: time %q must look like 07:30", ErrInvalidInput, s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// ValidateSchedule checks a schedule coming from the user.
func ValidateSchedule(sc config.AltSchedule) error {
	if _, err := parseClock(sc.From); err != nil {
		return err
	}
	if _, err := parseClock(sc.To); err != nil {
		return err
	}
	for _, d := range sc.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("%w: weekday %d out of range", ErrInvalidInput, d)
		}
	}
	return nil
}

// inScheduleWindow reports whether now falls inside the schedule's turtle window.
func inScheduleWindow(sc config.AltSchedule, now time.Time) bool {
	if !sc.Enabled {
		return false
	}
	from, err1 := parseClock(sc.From)
	to, err2 := parseClock(sc.To)
	if err1 != nil || err2 != nil || from == to {
		return false
	}
	dayOK := func(d time.Weekday) bool {
		if len(sc.Days) == 0 {
			return true
		}
		for _, x := range sc.Days {
			if x == int(d) {
				return true
			}
		}
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if from < to {
		return cur >= from && cur < to && dayOK(now.Weekday())
	}
	// Crosses midnight: the evening part belongs to today, the morning part to yesterday.
	return (cur >= from && dayOK(now.Weekday())) || (cur < to && dayOK((now.Weekday()+6)%7))
}

// applySchedule turns the turtle mode on or off when the schedule window starts or ends.
// Between those moments the user can still toggle it by hand; the schedule only acts on
// the boundaries (and once at startup).
func (m *Manager) applySchedule(now time.Time) {
	sc := m.cfg.Get().AltSchedule
	in := inScheduleWindow(sc, now)

	m.mu.Lock()
	prev, known := m.schedIn, m.schedKnown
	m.schedIn, m.schedKnown = in, true
	m.mu.Unlock()

	if !sc.Enabled {
		return
	}
	if !known || in != prev {
		if m.cfg.Get().AltSpeedActive != in {
			_ = m.SetAltSpeed(in)
		}
	}
}

// SetAltSchedule stores a new schedule and re-evaluates it right away.
func (m *Manager) SetAltSchedule(sc config.AltSchedule) error {
	if err := ValidateSchedule(sc); err != nil {
		return err
	}
	if err := m.cfg.Update(func(s *config.Settings) { s.AltSchedule = sc }); err != nil {
		return err
	}
	m.mu.Lock()
	m.schedKnown = false // treat as a fresh start: apply the current state of the window
	m.mu.Unlock()
	m.applySchedule(time.Now())
	return nil
}
