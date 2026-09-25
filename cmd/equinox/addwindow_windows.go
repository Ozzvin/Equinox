package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"github.com/Ozzvin/equinox/internal/buildinfo"
	"github.com/Ozzvin/equinox/internal/desktop"
)

// The window for adding torrents. Opening a .torrent file or a magnet link from outside (Explorer, a browser)
// does not bring up the whole application: a small window of its own comes up over whatever the user is doing,
// with the same "Add torrents" dialog, and the main window, if it is hidden in the tray, stays hidden.
//
// It is a process of its own (Equinox.exe -addwindow, or the second launch that Explorer started when the
// program was already running), showing the same page as the main window with ?window=add. One process to a
// window keeps the message loops apart, and the process Explorer starts is the one that is allowed to come to the
// front. There is one such window at a time: a later launch hands its files to the open one.

var pAllowSetForeground = user32.NewProc("AllowSetForegroundWindow")

const (
	hwndTopmost   = ^uintptr(0) // HWND_TOPMOST (-1)
	hwndNoTopmost = ^uintptr(1) // HWND_NOTOPMOST (-2)
	swpShowWindow = 0x0040
	asfwAny       = 0xFFFFFFFF // ASFW_ANY
)

// instanceSuffix tells the release from a test build: the lock and the events of a test build have names of their
// own, so it runs beside the installed program.
func instanceSuffix() string {
	if ch := buildinfo.Channel(); ch != "" {
		return "-" + ch
	}
	return ""
}

// allowForeground lets any process take the foreground. The process Explorer started may, and it hands that on to
// the window it wakes, which windows otherwise refuses (it only flashes its button in the taskbar).
func allowForeground() { pAllowSetForeground.Call(asfwAny) }

// bringToFront shows a window above the others even when Windows will not give it the keyboard focus: a
// topmost step and back puts it on top without staying there.
func bringToFront(hwnd uintptr) {
	pShowWindow.Call(hwnd, 9) // SW_RESTORE
	pSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWindow)
	pSetWindowPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWindow)
	pSetForeground.Call(hwnd)
}

// uiAddress reads where the running program's interface is and the key for it (ui-url in the data folder), waiting
// for it to come up: a program that has just been started needs a moment.
func uiAddress(stateDir string, wait time.Duration) (base, token string, err error) {
	deadline := time.Now().Add(wait)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		if raw, rerr := os.ReadFile(filepath.Join(stateDir, "ui-url")); rerr == nil {
			if b, t, ok := strings.Cut(strings.TrimSpace(string(raw)), "/#token="); ok {
				if res, gerr := client.Get(b + "/"); gerr == nil {
					res.Body.Close()
					return b, t, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return "", "", errors.New("the program's interface did not come up")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// bindExternal lets the page open a link in the user's own browser (only http and https are passed on).
func bindExternal(w webview2.WebView) {
	_ = w.Bind("openExternal", openInBrowser)
}

// runAddWindow makes this process the window for adding torrents and returns when it is closed. When one is open
// already it wakes that one (which picks up what was queued) and returns at once. An error means no window could
// be made; the caller then falls back to the main window.
func runAddWindow(stateDir string) error {
	suffix := instanceSuffix()
	lock, _ := windows.UTF16PtrFromString(`Local\EquinoxAddWindow` + suffix)
	kickName, _ := windows.UTF16PtrFromString(`Local\EquinoxAddWindowKick` + suffix)
	if _, err := windows.CreateMutex(nil, false, lock); err == windows.ERROR_ALREADY_EXISTS {
		allowForeground()
		if ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, kickName); err == nil {
			_ = windows.SetEvent(ev)
			_ = windows.CloseHandle(ev)
		}
		return nil
	}
	kick, err := windows.CreateEvent(nil, 0, 0, kickName) // auto-reset
	if err != nil {
		return err
	}

	base, token, err := uiAddress(stateDir, 20*time.Second)
	if err != nil {
		return err
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: filepath.Join(stateDir, "webview"), AutoFocus: true,
		WindowOptions: webview2.WindowOptions{Title: "Добавить раздачу — " + appTitle(), Width: 900, Height: 720, Center: true},
	})
	if w == nil {
		return errors.New("Microsoft Edge WebView2 Runtime was not found")
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	pShowWindow.Call(hwnd, 0) // shown once it has been themed and has its page (see window in main_windows.go)

	_ = w.Bind("closeAddWindow", func() { w.Dispatch(w.Terminate) })
	_ = w.Bind("setTitleBar", func(bg, fg string) { _ = desktop.TitleBar(hwnd, bg, fg) })
	bindExternal(w)
	setWindowIcon(hwnd)
	if desktop.SystemUsesDarkTheme() {
		_ = desktop.TitleBar(hwnd, "#1b1f26", "#e6e9ef")
	} else {
		_ = desktop.TitleBar(hwnd, "#ffffff", "#1a1f29")
	}
	w.Init("window.__equinoxToken = " + strconv.Quote(token) + "; window.__equinoxDesktop = true;")
	w.Navigate(base + "/?window=add")

	go func() { // a moment for the page to paint, then the window comes to the front
		time.Sleep(300 * time.Millisecond)
		w.Dispatch(func() { bringToFront(hwnd) })
	}()
	go func() { // a later launch with more files wakes this window
		for {
			if r, _ := windows.WaitForSingleObject(kick, windows.INFINITE); r != windows.WAIT_OBJECT_0 {
				return
			}
			w.Dispatch(func() {
				bringToFront(hwnd)
				w.Eval("window.checkPendingAddNow && window.checkPendingAddNow()")
			})
		}
	}()
	go func() { // the program this window belongs to has gone (quit from the tray): nothing is left to add to
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
				log.Println("add window: the program is gone, closing")
				w.Dispatch(w.Terminate)
				return
			}
		}
	}()
	log.Println("add window: open")
	w.Run()
	log.Println("add window: closed")
	return nil
}
