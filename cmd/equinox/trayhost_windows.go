package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// The menu of the tray icon. A right click on the icon does not open the system's menu (which cannot take the look of
// the program) but a small window with the page tray.html, drawn by a process of its own (Equinox.exe -traymenu).
// That process starts with the program, keeps its window hidden and shows it when the program signals the event
// trayShowEvent; the window goes away when it loses the focus or on Escape. The window is a process of its own, as
// the window for adding torrents is, so that the message loops stay apart and the right click is answered at once
// (a web view made on demand takes a second to start). If the process is not there (no WebView2, it has crashed),
// the program shows the system's menu instead, see desktop_.onTrayReady.

var (
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	pGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	pGetAncestor         = user32.NewProc("GetAncestor")
	pSetFocus            = user32.NewProc("SetFocus")
	pAsyncKeyState       = user32.NewProc("GetAsyncKeyState")
	pSetWindowLongPtr    = user32.NewProc("SetWindowLongPtrW")
	pDwmSetWindowAttr    = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

const (
	gwlStyle   = -16
	gwlExStyle = -20

	wsPopup         = 0x80000000
	wsExToolWindow  = 0x00000080
	wsExTopmost     = 0x00000008
	swpFrameChanged = 0x0020
	gaRoot          = 2
	monitorNearest  = 2

	dwmCornerPreference = 33
	dwmCornerRound      = 2

	trayMenuMargin = 8 // pixels between the menu and the edge of the work area
)

type winPoint struct{ X, Y int32 }

// trayShowEventName is the event the program sets for the menu to appear; the menu process creates it.
func trayShowEventName() *uint16 { return mustUTF16(`Local\EquinoxTrayMenuShow` + instanceSuffix()) }

// signalTrayMenu asks the menu process to show its window. It reports false when there is no such process.
func signalTrayMenu() bool {
	ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, trayShowEventName())
	if err != nil {
		return false
	}
	defer windows.CloseHandle(ev)
	allowForeground() // the menu process takes the keyboard focus, so that a click elsewhere can close it
	return windows.SetEvent(ev) == nil
}

// spawnTrayMenu starts the menu process.
func spawnTrayMenu(stateDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return exec.Command(exe, "-traymenu", "-state", stateDir).Start()
}

// trayMenuAlive tells whether a menu process is running (it holds the event).
func trayMenuAlive() bool {
	ev, err := windows.OpenEvent(windows.SYNCHRONIZE, false, trayShowEventName())
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(ev)
	return true
}

// signalDesktop sets one of the main program's events (show the window, quit, open the settings).
func signalDesktop(name string) {
	ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, mustUTF16(`Local\EquinoxDesktop`+name+instanceSuffix()))
	if err != nil {
		return
	}
	_ = windows.SetEvent(ev)
	_ = windows.CloseHandle(ev)
}

// runTrayMenu is the menu process: it returns when the program it belongs to has gone.
func runTrayMenu(stateDir string) error {
	suffix := instanceSuffix()
	lock := mustUTF16(`Local\EquinoxTrayMenu` + suffix)
	if _, err := windows.CreateMutex(nil, false, lock); err == windows.ERROR_ALREADY_EXISTS {
		return nil // one is running already
	}
	base, token, err := uiAddress(stateDir, 20*time.Second)
	if err != nil {
		return err
	}
	w := newWebViewOffscreen(webview2.WebViewOptions{
		// its own profile folder, for the reason given in runAddWindow
		DataPath: filepath.Join(stateDir, "webview-tray"), AutoFocus: true,
		WindowOptions: webview2.WindowOptions{Title: appTitle(), Width: 280, Height: 320},
	})
	if w == nil {
		return errors.New("Microsoft Edge WebView2 Runtime was not found")
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	pShowWindow.Call(hwnd, 0)
	// a window of the menu's kind: no frame, no button in the taskbar, above the others, round corners where the
	// system has them
	style, exStyle := int32(gwlStyle), int32(gwlExStyle) // negative indexes: converted from variables
	pSetWindowLongPtr.Call(hwnd, uintptr(style), wsPopup)
	pSetWindowLongPtr.Call(hwnd, uintptr(exStyle), wsExToolWindow|wsExTopmost)
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged|swpNoActivate)
	corner := uint32(dwmCornerRound)
	pDwmSetWindowAttr.Call(hwnd, dwmCornerPreference, uintptr(unsafe.Pointer(&corner)), 4)

	var (
		width, height int32 = 280, 320 // what the page last measured
		visible       atomic.Bool
		pending       atomic.Bool // shown once the page has drawn its fresh numbers
	)
	var anchor winPoint // where the cursor was when the menu appeared: the menu stays there while it is open
	var placedW, placedH int32
	place := func() {
		cur := anchor
		mon, _, _ := pMonitorFromPoint.Call(uintptr(*(*int64)(unsafe.Pointer(&cur))), monitorNearest)
		mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
		pMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
		wa := mi.Work
		x, y := cur.X-width/2, cur.Y-height/2
		switch {
		case cur.Y >= wa.Bottom: // the taskbar is at the bottom: the menu stands on it
			y = wa.Bottom - height - trayMenuMargin
		case cur.Y <= wa.Top: // at the top
			y = wa.Top + trayMenuMargin
		}
		switch {
		case cur.X >= wa.Right: // at the right
			x = wa.Right - width - trayMenuMargin
		case cur.X <= wa.Left: // at the left
			x = wa.Left + trayMenuMargin
		}
		x = max(wa.Left+trayMenuMargin, min(x, wa.Right-width-trayMenuMargin))
		y = max(wa.Top+trayMenuMargin, min(y, wa.Bottom-height-trayMenuMargin))
		placedW, placedH = width, height
		pSetWindowPos.Call(hwnd, hwndTopmost, uintptr(x), uintptr(y), uintptr(width), uintptr(height), swpShowWindow)
	}
	var shownAt atomic.Int64
	reveal := func() {
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&anchor)))
		place()
		pSetForeground.Call(hwnd)
		pSetFocus.Call(hwnd) // the window takes the focus, and with AutoFocus the page does: Escape and the arrows reach it
		visible.Store(true)
		pending.Store(false)
		shownAt.Store(time.Now().UnixMilli())
	}
	hide := func() {
		if visible.Swap(false) {
			pShowWindow.Call(hwnd, 0)
			w.Eval("window.trayHidden && window.trayHidden()")
		}
	}

	_ = w.Bind("trayMeasure", func(wd, ht int) {
		if wd <= 0 || ht <= 0 {
			return
		}
		width, height = int32(wd), int32(ht)
		if pending.Load() {
			w.Dispatch(reveal)
		} else if visible.Load() && (width != placedW || height != placedH) {
			w.Dispatch(place)
		}
	})
	_ = w.Bind("trayHide", func() { w.Dispatch(hide) })
	_ = w.Bind("trayAction", func(name string) {
		switch {
		case name == "open":
			signalDesktop("Show")
		case name == "settings":
			signalDesktop("Settings")
		case name == "quit":
			signalDesktop("Quit")
		case name == "add":
			if err := spawnAddWindow(stateDir); err != nil {
				log.Println("tray menu: add window:", err)
			}
		case strings.HasPrefix(name, "folder:"):
			if dir := strings.TrimPrefix(name, "folder:"); dir != "" {
				_ = os.MkdirAll(dir, 0o755)
				_ = exec.Command("explorer.exe", dir).Start()
			}
		}
	})
	w.Init("if (location.origin === " + strconv.Quote(base) + ") { window.__equinoxToken = " + strconv.Quote(token) + "; }")
	w.Navigate(base + "/tray.html")

	// created only now, once the window can answer: a program that finds the event knows the menu is ready
	show, err := windows.CreateEvent(nil, 0, 0, trayShowEventName())
	if err != nil {
		return err
	}
	go func() {
		for {
			if r, _ := windows.WaitForSingleObject(show, windows.INFINITE); r != windows.WAIT_OBJECT_0 {
				return
			}
			w.Dispatch(func() {
				pending.Store(true)
				w.Eval("window.trayShown && window.trayShown()")
				go func() { // the page did not answer in time: show what it has
					time.Sleep(500 * time.Millisecond)
					if pending.Load() {
						w.Dispatch(reveal)
					}
				}()
			})
		}
	}()
	go func() { // Escape closes the menu even when the page has not got the keyboard focus (it often has not: the
		// window was brought up by a click on another program's icon); a press (50 ms and more) is caught by asking often
		for {
			time.Sleep(15 * time.Millisecond)
			if !visible.Load() || time.Now().UnixMilli()-shownAt.Load() < 400 {
				continue
			}
			if k, _, _ := pAsyncKeyState.Call(0x1B); k&0x8000 != 0 { // VK_ESCAPE is down now
				w.Dispatch(hide)
			}
		}
	}()
	go func() { // a click anywhere else, or another window coming forward, closes the menu
		for {
			time.Sleep(80 * time.Millisecond)
			if !visible.Load() || time.Now().UnixMilli()-shownAt.Load() < 400 {
				continue
			}
			fg, _, _ := pGetForegroundWindow.Call()
			root, _, _ := pGetAncestor.Call(fg, gaRoot)
			if fg == 0 || (fg != hwnd && root != hwnd) {
				w.Dispatch(hide)
			}
		}
	}()
	go func() { // the program has gone (quit from the menu or the installer): so does this process
		client := &http.Client{Timeout: 2 * time.Second}
		fails := 0
		for {
			time.Sleep(3 * time.Second)
			if res, err := client.Get(base + "/"); err != nil {
				fails++
			} else {
				res.Body.Close()
				fails = 0
			}
			if fails >= 3 {
				log.Println("tray menu: the program is gone, closing")
				w.Dispatch(w.Terminate)
				return
			}
		}
	}()
	log.Println("tray menu: ready")
	w.Run()
	log.Println("tray menu: closed")
	return nil
}
