//go:build windows

package main

import (
	"log"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"

	"github.com/Ozzvin/equinox/internal/desktop"
)

// The window remembers where it was and how large, separately for each density of the interface: the
// page tells which density it is in (windowDensity), and switching it resizes the window to what that
// density had last time (or to its default size).

var (
	pIsZoomed      = user32.NewProc("IsZoomed")
	pGetWindowRect = user32.NewProc("GetWindowRect")
	pSetWindowPos  = user32.NewProc("SetWindowPos")
	pGetMetrics    = user32.NewProc("GetSystemMetrics")
	pMonitorFrom   = user32.NewProc("MonitorFromWindow")
	pMonitorInfo   = user32.NewProc("GetMonitorInfoW")
)

type monitorInfo struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
}

type winRect struct{ Left, Top, Right, Bottom int32 }

const (
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	swMaximize    = 3
)

type placer struct {
	mu    sync.Mutex
	path  string
	prefs desktop.WindowPrefs
	dirty bool
	at    time.Time // last change of the window's own size or position
	w     webview2.WebView
	hwnd  uintptr
}

func newPlacer(stateDir string) *placer {
	path := filepath.Join(stateDir, "window.json")
	return &placer{path: path, prefs: desktop.LoadWindowPrefs(path)}
}

// startSize is the client size the window is created with: the default of the density it had last.
func (p *placer) startSize() desktop.Size {
	p.mu.Lock()
	defer p.mu.Unlock()
	return desktop.DefaultSize(p.prefs.Density)
}

// hasPos tells whether a position is remembered and still on a screen.
func (p *placer) hasPos() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prefs.HasPos && p.visible(p.prefs.X, p.prefs.Y)
}

func (p *placer) visible(x, y int) bool {
	vx, _, _ := pGetMetrics.Call(76) // SM_XVIRTUALSCREEN
	vy, _, _ := pGetMetrics.Call(77)
	vw, _, _ := pGetMetrics.Call(78)
	vh, _, _ := pGetMetrics.Call(79)
	return desktop.PosVisible(x, y, int(int32(vx)), int(int32(vy)), int(vw), int(vh))
}

// attach applies the remembered size, position and state to a freshly created window.
func (p *placer) attach(w webview2.WebView, hwnd uintptr) {
	p.mu.Lock()
	p.w, p.hwnd = w, hwnd
	d := p.prefs.Density
	w.SetSize(desktop.MinSize(d).W, desktop.MinSize(d).H, webview2.HintMin)
	if s, ok := p.prefs.Saved(d); ok {
		p.setOuter(s)
	} else {
		ds := desktop.DefaultSize(d)
		w.SetSize(ds.W, ds.H, webview2.HintNone)
	}
	if p.prefs.HasPos && p.visible(p.prefs.X, p.prefs.Y) {
		pSetWindowPos.Call(hwnd, 0, uintptr(p.prefs.X), uintptr(p.prefs.Y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	}
	p.keepOnScreen()
	max := p.prefs.Max
	p.mu.Unlock()
	if max {
		pShowWindow.Call(hwnd, swMaximize)
	}
}

// setOuter gives the window an outer size, keeping its position. The caller holds the lock.
func (p *placer) setOuter(s desktop.Size) {
	pSetWindowPos.Call(p.hwnd, 0, 0, 0, uintptr(s.W), uintptr(s.H), swpNoMove|swpNoZOrder|swpNoActivate)
	p.keepOnScreen()
}

// keepOnScreen moves the window back into the work area of its monitor when a new size pushed it out.
// The caller holds the lock.
func (p *placer) keepOnScreen() {
	mon, _, _ := pMonitorFrom.Call(p.hwnd, 2) // MONITOR_DEFAULTTONEAREST
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := pMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return
	}
	var r winRect
	if ok, _, _ := pGetWindowRect.Call(p.hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	x, y := r.Left, r.Top
	if r.Right > mi.Work.Right {
		x -= r.Right - mi.Work.Right
	}
	if r.Bottom > mi.Work.Bottom {
		y -= r.Bottom - mi.Work.Bottom
	}
	if x < mi.Work.Left {
		x = mi.Work.Left
	}
	if y < mi.Work.Top {
		y = mi.Work.Top
	}
	if x != r.Left || y != r.Top {
		pSetWindowPos.Call(p.hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	}
}

// setDensity is called by the page when the interface density is set or changed.
func (p *placer) setDensity(d string) string {
	if !desktop.ValidDensity(d) {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.w == nil || d == p.prefs.Density {
		return ""
	}
	p.snapshot() // what the old density looked like
	p.prefs.Density = d
	p.w.SetSize(desktop.MinSize(d).W, desktop.MinSize(d).H, webview2.HintMin)
	if zoomed, _, _ := pIsZoomed.Call(p.hwnd); zoomed == 0 {
		if s, ok := p.prefs.Saved(d); ok {
			p.setOuter(s)
		} else {
			ds := desktop.DefaultSize(d)
			p.w.SetSize(ds.W, ds.H, webview2.HintNone)
			p.keepOnScreen()
		}
	}
	p.dirty, p.at = true, time.Now().Add(-time.Second)
	return ""
}

// resetSize brings the window of the current density back to its default size.
func (p *placer) resetSize() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.w == nil {
		return ""
	}
	d := p.prefs.Density
	p.prefs.ForgetSize(d)
	if zoomed, _, _ := pIsZoomed.Call(p.hwnd); zoomed != 0 {
		return ""
	}
	ds := desktop.DefaultSize(d)
	p.w.SetSize(ds.W, ds.H, webview2.HintNone)
	p.keepOnScreen()
	p.dirty, p.at = true, time.Now().Add(-time.Second)
	return ""
}

// snapshot copies the window's current outer size and position into the preferences. The caller
// holds the lock. It reports whether anything changed.
func (p *placer) snapshot() bool {
	if p.hwnd == 0 {
		return false
	}
	if iconic, _, _ := pIsIconic.Call(p.hwnd); iconic != 0 {
		return false
	}
	if zoomed, _, _ := pIsZoomed.Call(p.hwnd); zoomed != 0 {
		if p.prefs.Max {
			return false
		}
		p.prefs.Max = true // the size and position of the restored window stay as they were
		return true
	}
	var r winRect
	if ok, _, _ := pGetWindowRect.Call(p.hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return false
	}
	s := desktop.Size{W: int(r.Right - r.Left), H: int(r.Bottom - r.Top)}
	old, had := p.prefs.Saved(p.prefs.Density)
	changed := p.prefs.Max || !had || old != s || !p.prefs.HasPos || p.prefs.X != int(r.Left) || p.prefs.Y != int(r.Top)
	p.prefs.Max = false
	p.prefs.SetSize(p.prefs.Density, s)
	p.prefs.HasPos, p.prefs.X, p.prefs.Y = true, int(r.Left), int(r.Top)
	return changed
}

// watch follows the window until stop is closed and writes the file once the window has been left alone.
func (p *placer) watch(stop <-chan struct{}) {
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			p.mu.Lock()
			p.hwnd = 0
			p.w = nil
			p.save()
			p.mu.Unlock()
			return
		case <-tick.C:
			p.mu.Lock()
			if p.snapshot() {
				p.dirty, p.at = true, time.Now()
			}
			if p.dirty && time.Since(p.at) > 800*time.Millisecond {
				p.save()
			}
			p.mu.Unlock()
		}
	}
}

// save writes the file. The caller holds the lock.
func (p *placer) save() {
	if !p.dirty {
		return
	}
	p.dirty = false
	if err := p.prefs.Save(p.path); err != nil {
		log.Println("window: cannot save its place:", err)
	}
}
