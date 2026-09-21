package core

import (
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

func at(day time.Weekday, hh, mm int) time.Time {
	// 2026-09-20 is a Sunday; shift to the wanted weekday.
	base := time.Date(2026, 9, 20, hh, mm, 0, 0, time.UTC)
	return base.AddDate(0, 0, int(day))
}

func TestScheduleWindows(t *testing.T) {
	night := config.AltSchedule{Enabled: true, From: "23:00", To: "07:00"}
	work := config.AltSchedule{Enabled: true, From: "09:00", To: "18:00", Days: []int{1, 2, 3, 4, 5}}
	weekendNight := config.AltSchedule{Enabled: true, From: "22:00", To: "06:00", Days: []int{6}} // Saturday night

	cases := []struct {
		name string
		sc   config.AltSchedule
		now  time.Time
		want bool
	}{
		{"night: evening inside", night, at(time.Wednesday, 23, 30), true},
		{"night: after midnight inside", night, at(time.Thursday, 2, 0), true},
		{"night: morning boundary excluded", night, at(time.Thursday, 7, 0), false},
		{"night: daytime outside", night, at(time.Thursday, 12, 0), false},
		{"work hours on weekday", work, at(time.Tuesday, 10, 0), true},
		{"work hours end excluded", work, at(time.Tuesday, 18, 0), false},
		{"work hours on weekend", work, at(time.Sunday, 10, 0), false},
		{"sat night: starts Saturday", weekendNight, at(time.Saturday, 23, 0), true},
		{"sat night: continues Sunday morning", weekendNight, at(time.Sunday, 3, 0), true},
		{"sat night: Friday evening is not it", weekendNight, at(time.Friday, 23, 0), false},
		{"sat night: Monday morning is not it", weekendNight, at(time.Monday, 3, 0), false},
		{"disabled", config.AltSchedule{From: "00:00", To: "23:59"}, at(time.Monday, 12, 0), false},
		{"empty window", config.AltSchedule{Enabled: true, From: "10:00", To: "10:00"}, at(time.Monday, 10, 0), false},
	}
	for _, c := range cases {
		if got := inScheduleWindow(c.sc, c.now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestScheduleValidation(t *testing.T) {
	if err := ValidateSchedule(config.AltSchedule{From: "7:30", To: "25:00"}); err == nil {
		t.Fatal("bad time must be rejected")
	}
	if err := ValidateSchedule(config.AltSchedule{From: "08:00", To: "09:00", Days: []int{7}}); err == nil {
		t.Fatal("weekday 7 must be rejected")
	}
	if err := ValidateSchedule(config.AltSchedule{From: "08:00", To: "09:00", Days: []int{0, 6}}); err != nil {
		t.Fatal(err)
	}
}

// The schedule acts on window boundaries only: a manual toggle inside the window sticks
// until the window ends.
func TestScheduleTogglesOnBoundariesOnly(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	sc := config.AltSchedule{Enabled: true, From: "23:00", To: "07:00"}
	if err := m.cfg.Update(func(s *config.Settings) { s.AltSchedule = sc }); err != nil {
		t.Fatal(err)
	}
	active := func() bool { return m.cfg.Get().AltSpeedActive }

	m.applySchedule(at(time.Monday, 12, 0)) // first evaluation, outside the window
	if active() {
		t.Fatal("must be off outside the window")
	}
	m.applySchedule(at(time.Monday, 23, 0)) // window starts
	if !active() {
		t.Fatal("must switch on when the window starts")
	}
	_ = m.SetAltSpeed(false)                 // user turns it off by hand
	m.applySchedule(at(time.Monday, 23, 30)) // still inside: no change
	if active() {
		t.Fatal("manual override must stick inside the window")
	}
	m.applySchedule(at(time.Tuesday, 7, 0)) // window ends
	if active() {
		t.Fatal("must be off after the window")
	}
	m.applySchedule(at(time.Tuesday, 23, 0)) // next night
	if !active() {
		t.Fatal("must switch on again the next night")
	}
	m.applySchedule(at(time.Wednesday, 7, 0))
	if active() {
		t.Fatal("must switch off when the window ends")
	}
}
