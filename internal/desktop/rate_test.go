package desktop

import "testing"

func TestFormatRate(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		bits bool
		want string
	}{
		{0, false, "0 Б/с"}, {500, false, "500 Б/с"}, {2048, false, "2 КБ/с"}, {5 << 20, false, "5.0 МБ/с"},
		{0, true, "0 бит/с"}, {100, true, "800 бит/с"}, {125, true, "1 Кбит/с"}, {1_250_000, true, "10.0 Мбит/с"},
		{125_000_000, true, "1.0 Гбит/с"},
	} {
		if got := FormatRate(tc.n, tc.bits); got != tc.want {
			t.Errorf("FormatRate(%d, %v) = %q, want %q", tc.n, tc.bits, got, tc.want)
		}
	}
}
