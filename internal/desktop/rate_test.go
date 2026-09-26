package desktop

import "testing"

func TestFormatRate(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		bits bool
		want string
	}{
		{0, false, "0 Б/с"}, {500, false, "500 Б/с"}, {2048, false, "2 КБ/с"}, {5 << 20, false, "5.0 МБ/с"},
		{0, true, "0 б/с"}, {100, true, "800 б/с"}, {125, true, "1 Кб/с"}, {1_250_000, true, "10.0 Мб/с"},
		{125_000_000, true, "1.0 Гб/с"},
	} {
		if got := FormatRate(tc.n, tc.bits); got != tc.want {
			t.Errorf("FormatRate(%d, %v) = %q, want %q", tc.n, tc.bits, got, tc.want)
		}
	}
}

func TestFormatRateInEnglish(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		bits bool
		want string
	}{
		{0, false, "0 B/s"}, {2048, false, "2 KB/s"}, {5 << 20, false, "5.0 MB/s"},
		{100, true, "800 b/s"}, {125, true, "1 Kb/s"}, {1_250_000, true, "10.0 Mb/s"}, {125_000_000, true, "1.0 Gb/s"},
	} {
		if got := FormatRateIn(tc.n, tc.bits, true); got != tc.want {
			t.Errorf("FormatRateIn(%d, %v, en) = %q, want %q", tc.n, tc.bits, got, tc.want)
		}
	}
}
