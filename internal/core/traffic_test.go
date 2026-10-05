package core

import (
	"testing"
	"time"
)

func TestTrafficStatsByPeriod(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	now := time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC)
	add := func(ago time.Duration, down, up int64) { m.addTraffic(now.Add(-ago), down, up) }
	add(0, 100, 10)                // this hour
	add(time.Hour, 50, 5)          // an hour ago: still the day
	add(30*time.Hour, 1000, 100)   // yesterday: the week, not the day
	add(10*24*time.Hour, 7000, 70) // ten days ago: the month only
	add(40*24*time.Hour, 9, 9)     // beyond what is kept: dropped once newer traffic comes
	add(0, 1, 1)                   // newer traffic again (it is what prunes the old)

	st := m.TrafficStats(now)
	if st.Day != (Traffic{151, 16}) {
		t.Errorf("day %+v", st.Day)
	}
	if st.Week != (Traffic{1151, 116}) {
		t.Errorf("week %+v", st.Week)
	}
	if st.Month != (Traffic{8151, 186}) {
		t.Errorf("month %+v", st.Month)
	}
	if st.Session != (Traffic{8160, 195}) { // everything recorded in this session, kept or not
		t.Errorf("session %+v", st.Session)
	}
	var kept int
	m.state.view(func(s *state) { kept = len(s.Traffic) })
	if kept != 4 {
		t.Errorf("%d hourly buckets kept, want 4 (the 40-day-old one dropped)", kept)
	}
	if want := now.Add(-10 * 24 * time.Hour).Truncate(time.Hour); !st.Since.Equal(want) {
		t.Errorf("since %v, want %v", st.Since, want)
	}
}
