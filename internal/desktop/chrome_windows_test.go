package desktop

import "testing"

func TestParseCSSColor(t *testing.T) {
	for in, want := range map[string][3]uint8{
		"rgb(27, 31, 38)":       {27, 31, 38},
		"rgba(27, 31, 38, 0.5)": {27, 31, 38},
		"rgb(27 31 38 / 50%)":   {27, 31, 38},
		"#1b1f26":               {27, 31, 38},
		" RGB(255,255,255) ":    {255, 255, 255},
		"rgb(10.4, 20.6, 30.5)": {10, 21, 31},
	} {
		r, g, b, err := ParseCSSColor(in)
		if err != nil || [3]uint8{r, g, b} != want {
			t.Errorf("%q: %d %d %d %v, want %v", in, r, g, b, err, want)
		}
	}
	for _, bad := range []string{"", "red", "#fff", "rgb(1,2)", "rgb(300,0,0)", "rgb(a,b,c)", "#gggggg"} {
		if _, _, _, err := ParseCSSColor(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if colorref(1, 2, 3) != 0x030201 {
		t.Errorf("COLORREF is BGR: %x", colorref(1, 2, 3))
	}
}
