package desktop

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The title bar of the window (name, minimise, maximise, close) is Windows' own, but Windows 11 lets
// the application choose its colours, so that it looks like a part of the page: the same background as
// the top bar of the interface, the same text colour, dark or light buttons to match. On Windows 10 only
// the dark or light look is applied.

var (
	dwmapi     = windows.NewLazySystemDLL("dwmapi.dll")
	pDwmSetAtt = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	dwmUseImmersiveDarkMode = 20
	dwmBorderColor          = 34
	dwmCaptionColor         = 35
	dwmTextColor            = 36
)

func setDwm(hwnd uintptr, attr uint32, value uint32) {
	_, _, _ = pDwmSetAtt.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&value)), 4)
}

// ParseCSSColor reads "rgb(r, g, b)", "rgba(r, g, b, a)" or "#rrggbb".
func ParseCSSColor(s string) (r, g, b uint8, err error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch {
	case strings.HasPrefix(s, "#") && len(s) == 7:
		v, e := strconv.ParseUint(s[1:], 16, 32)
		if e != nil {
			return 0, 0, 0, e
		}
		return uint8(v >> 16), uint8(v >> 8), uint8(v), nil
	case strings.HasPrefix(s, "rgb"):
		open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || closing < open {
			break
		}
		parts := strings.FieldsFunc(s[open+1:closing], func(c rune) bool { return c == ',' || c == ' ' || c == '/' })
		if len(parts) < 3 {
			break
		}
		var v [3]uint8
		for i := 0; i < 3; i++ {
			f, e := strconv.ParseFloat(parts[i], 64)
			if e != nil || f < 0 || f > 255 {
				return 0, 0, 0, fmt.Errorf("bad colour %q", s)
			}
			v[i] = uint8(f + 0.5)
		}
		return v[0], v[1], v[2], nil
	}
	return 0, 0, 0, fmt.Errorf("bad colour %q", s)
}

func colorref(r, g, b uint8) uint32 { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }

// TitleBar paints the title bar of the window with the given background and text colours (CSS colour
// strings) and picks the dark or light style of its buttons from the background.
func TitleBar(hwnd uintptr, bg, fg string) error {
	br, bgc, bb, err := ParseCSSColor(bg)
	if err != nil {
		return err
	}
	dark := 0.2126*float64(br)+0.7152*float64(bgc)+0.0722*float64(bb) < 128
	var d uint32
	if dark {
		d = 1
	}
	setDwm(hwnd, dwmUseImmersiveDarkMode, d)
	setDwm(hwnd, dwmCaptionColor, colorref(br, bgc, bb))
	setDwm(hwnd, dwmBorderColor, colorref(br, bgc, bb))
	if tr, tg, tb, err := ParseCSSColor(fg); err == nil {
		setDwm(hwnd, dwmTextColor, colorref(tr, tg, tb))
	}
	return nil
}

// SystemUsesDarkTheme tells whether Windows apps are set to the dark theme; it is used before the page
// has told its own colours, so that the window does not flash white.
func SystemUsesDarkTheme() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}
