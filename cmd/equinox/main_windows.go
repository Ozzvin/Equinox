// Command equinox is the Windows application: the daemon, a window with the web
// interface and a tray icon. Closing the window keeps everything running in the tray.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/energye/systray"
	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"github.com/Ozzvin/equinox/internal/app"
	"github.com/Ozzvin/equinox/internal/buildinfo"
	"github.com/Ozzvin/equinox/internal/core"
	"github.com/Ozzvin/equinox/internal/desktop"
	"github.com/Ozzvin/equinox/internal/lang"
	"github.com/Ozzvin/equinox/internal/update"
)

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	pShowWindow               = user32.NewProc("ShowWindow")
	pIsIconic                 = user32.NewProc("IsIconic")
	pSetForeground            = user32.NewProc("SetForegroundWindow")
	pSendMessage              = user32.NewProc("SendMessageW")
	pCreateIconFromR          = user32.NewProc("CreateIconFromResourceEx")
	pMessageBox               = user32.NewProc("MessageBoxW")
	pIsHungAppWindow          = user32.NewProc("IsHungAppWindow")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pEnumWindows              = user32.NewProc("EnumWindows")
	pGetClassName             = user32.NewProc("GetClassNameW")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

func main() {
	// The webview and its message loop must stay on the main OS thread.
	runtime.LockOSThread()

	stateDir := flag.String("state", "", "state directory (default: next to the executable, or a per-user folder if that is not writable)")
	listen := flag.String("listen", "127.0.0.1:9091", "HTTP address (must be loopback)")
	hidden := flag.Bool("hidden", false, "start in the tray without opening the window")
	addWindow := flag.Bool("addwindow", false, "internal: only open the window for adding torrents (the running program has queued them)")
	trayMenu := flag.Bool("traymenu", false, "internal: the window of the tray icon's menu (started by the program)")
	after := flag.Int("after", 0, "internal: wait for this process to exit first (used when restarting)")
	flag.Parse()
	args := flag.Args() // magnet links and .torrent files passed by Windows
	if *after > 0 {
		waitForExit(*after, 30*time.Second)
	}
	if *stateDir == "" {
		*stateDir = app.DefaultStateDir()
	}

	if err := os.MkdirAll(*stateDir, 0o755); err != nil {
		fatalBox(err)
	}
	// GUI applications have no console: keep a log file instead.
	if err := app.SetupLog(filepath.Join(*stateDir, "equinox.log"), 5<<20); err != nil {
		log.Println("cannot open the log file:", err) // not fatal: run without a log
	}

	if *addWindow {
		if err := runAddWindow(*stateDir); err != nil {
			log.Println("add window:", err)
			// no window of its own: the running program shows its main window, whose dialog picks the queue up
			if ev, oerr := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, mustUTF16(`Local\EquinoxDesktopShow`+instanceSuffix())); oerr == nil {
				_ = windows.SetEvent(ev)
				_ = windows.CloseHandle(ev)
			}
		}
		return
	}

	if *trayMenu {
		if err := runTrayMenu(*stateDir); err != nil {
			log.Println("tray menu:", err)
		}
		return
	}

	// One instance only: a second start asks the running one to show its window.
	// The lock and the events are per channel: a beta ("EquinoxDesktop-beta") runs beside the installed
	// program instead of just asking it to show its window. Releases keep the names the installer knows.
	instance := ""
	if ch := buildinfo.Channel(); ch != "" {
		instance = "-" + ch
	}
	name, _ := windows.UTF16PtrFromString(`Local\EquinoxDesktop` + instance)
	evName, _ := windows.UTF16PtrFromString(`Local\EquinoxDesktopShow` + instance)
	if _, err := windows.CreateMutex(nil, false, name); err == windows.ERROR_ALREADY_EXISTS {
		if len(args) > 0 {
			if err := forward(*stateDir, args); err != nil {
				log.Println("forward to running instance:", err)
			}
		}
		// With files or links the window for adding torrents comes up and the main window is left alone; without
		// them (the user started the program again) the main window is shown.
		if len(args) > 0 {
			if err := runAddWindow(*stateDir); err == nil {
				return
			} else {
				log.Println("add window:", err)
			}
		}
		allowForeground() // the process Explorer started may hand the front to the window it wakes
		if ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, evName); err == nil {
			_ = windows.SetEvent(ev)
			_ = windows.CloseHandle(ev)
		}
		return
	}
	showEvent, err := windows.CreateEvent(nil, 0, 0, evName) // auto-reset
	if err != nil {
		fatalBox(err)
	}
	// The installer asks for a clean exit through this event instead of killing the process.
	// A forced kill skips Manager.Close, which is what flushes the counters and settles a
	// storage move that is halfway through (see installer/equinox.iss).
	quitEvName, _ := windows.UTF16PtrFromString(`Local\EquinoxDesktopQuit` + instance)
	quitEvent, err := windows.CreateEvent(nil, 0, 0, quitEvName)
	if err != nil {
		fatalBox(err)
	}
	settingsEvName, _ := windows.UTF16PtrFromString(`Local\EquinoxDesktopSettings` + instance)
	settingsEvent, err := windows.CreateEvent(nil, 0, 0, settingsEvName)
	if err != nil {
		fatalBox(err)
	}
	log.Println("starting engine")
	a, err := app.Start(*stateDir, *listen)
	if err != nil {
		fatalBox(err)
	}
	lang.SetPreference(func() string { return a.Settings.Get().Language })
	log.Println("engine started, api at", a.URL[:strings.Index(a.URL, "#")])
	// Lets a second launch find this instance; like api-token, the file holds the access key.
	_ = os.WriteFile(filepath.Join(*stateDir, "ui-url"), []byte(a.URL), 0o600)
	// So Equinox shows up as an option for .torrent/magnet (the "Open with" menu, and the
	// picker Windows shows when a file type has no default yet) without the user having to
	// visit Settings first. This does not make it the *default* — Windows only allows that
	// through its own UI (see RegisterHandlers), which is still a separate, explicit step.
	if buildinfo.Channel() == "" { // a test build leaves the installed program's registration alone
		if err := desktop.RegisterHandlers(); err != nil {
			log.Println("register file handlers:", err)
		}
	}
	addArgs(a, args)

	d := &desktop_{app: a, show: make(chan struct{}, 1), quit: make(chan struct{}), stateDir: *stateDir, listen: *listen, place: newPlacer(*stateDir, func() bool { return a.Settings.Get().RememberWindow })}
	go systray.Run(d.onTrayReady, func() {})
	a.Manager.OnEvent(d.notify)
	go func() { // a second launch of the program signals this event
		for {
			if r, _ := windows.WaitForSingleObject(showEvent, windows.INFINITE); r != windows.WAIT_OBJECT_0 {
				return
			}
			d.requestShow()
		}
	}()
	go func() { // the menu of the tray icon asks for the settings
		for {
			if r, _ := windows.WaitForSingleObject(settingsEvent, windows.INFINITE); r != windows.WAIT_OBJECT_0 {
				return
			}
			d.openSettings()
		}
	}()
	go func() { // the installer (or the uninstaller) asks the app to close itself
		if r, _ := windows.WaitForSingleObject(quitEvent, windows.INFINITE); r != windows.WAIT_OBJECT_0 {
			return
		}
		log.Println("quit requested by the installer")
		d.doQuit()
	}()
	switch {
	case len(args) > 0:
		// Opened from outside while the program was not running: it starts in the tray and the window for adding
		// comes up (its own process, see addwindow_windows.go); the main window is not shown.
		if err := spawnAddWindow(*stateDir); err != nil {
			log.Println("add window:", err)
			d.show <- struct{}{} // the main window's dialog takes the queue after PendingGrace
		}
	case !*hidden:
		d.show <- struct{}{} // open the window on start (not when autostarted into the tray)
	}

	for {
		select {
		case <-d.show:
			d.window(filepath.Join(*stateDir, "webview"))
		case <-d.quit:
			a.Close()
			return
		}
	}
}

type desktop_ struct {
	app      *app.App
	show     chan struct{}
	quit     chan struct{}
	quitOnce sync.Once
	stateDir string
	listen   string
	place    *placer

	mu   sync.Mutex
	view webview2.WebView // current window, nil when hidden in the tray

	// checkingUpdate is set while installUpdate's own update.Check call (up to 30s, synchronous on the window
	// thread: see installUpdate) is running, so requestShow knows a window that briefly stops answering
	// IsHungAppWindow then is busy, not actually hung (see requestShow).
	checkingUpdate atomic.Bool

	// settingsOnLoad asks the window that is about to be made to open the settings when its page has loaded.
	settingsOnLoad atomic.Bool
}

// openSettings brings the window forward with the settings dialog open (the tray menu's "Настройки").
func (d *desktop_) openSettings() {
	d.mu.Lock()
	v := d.view
	d.mu.Unlock()
	if v == nil { // no window yet: the new one opens the settings itself
		d.settingsOnLoad.Store(true)
		d.requestShow()
		return
	}
	hwnd := uintptr(v.Window())
	v.Dispatch(func() {
		bringToFront(hwnd)
		v.Eval("window.openSettingsNow && window.openSettingsNow()")
	})
}

// window opens the UI window and blocks until it is closed (or the app quits).
func (d *desktop_) window(dataPath string) {
	d.mu.Lock()
	if d.view != nil { // already open: bring it to the front
		hwnd := uintptr(d.view.Window())
		d.mu.Unlock()
		pShowWindow.Call(hwnd, 9) // SW_RESTORE
		pSetForeground.Call(hwnd)
		return
	}
	log.Println("window: creating")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: dataPath, AutoFocus: true,
		WindowOptions: webview2.WindowOptions{Title: appTitle(), Width: uint(d.place.startSize().W), Height: uint(d.place.startSize().H), Center: !d.place.hasPos()},
	})
	if w == nil {
		d.mu.Unlock()
		msgBox(lang.Tr("Не удалось открыть окно: не найден Microsoft Edge WebView2 Runtime.") + "\n" + lang.Tr("Приложение продолжает работать в трее, интерфейс доступен в браузере:") + "\n" + d.app.URL)
		return
	}
	d.view = w
	d.mu.Unlock()
	log.Println("window: created, navigating")

	hwnd := uintptr(w.Window())
	// The library shows the window itself as soon as it is created, at Windows' default
	// position with a blank white page — before it has been moved to the remembered spot or
	// themed or navigated anywhere. Hide it again right away and reveal it only once all of
	// that is done, so what the user sees is the finished window appearing once, in place,
	// instead of a flash at the wrong spot followed by a jump and a white-to-dark repaint.
	pShowWindow.Call(hwnd, 0)           // SW_HIDE
	_ = w.Bind("restartApp", d.restart) // used by the "restart required" bar
	_ = w.Bind("installUpdate", d.installUpdate)
	_ = w.Bind("getAutostart", desktop.AutostartEnabled)
	_ = w.Bind("setAutostart", func(on bool) string { // used by the Windows section of the settings
		if err := desktop.SetAutostart(on, d.app.Settings.Get().StartHidden); err != nil {
			return err.Error()
		}
		return ""
	})
	_ = w.Bind("registerHandlers", func() string {
		if err := desktop.RegisterHandlers(); err != nil {
			return err.Error()
		}
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", "ms-settings:defaultapps").Start()
		return ""
	})
	_ = w.Bind("windowDensity", d.place.setDensity) // the page reports its density; the window gets that density's size
	_ = w.Bind("resetWindowSize", d.place.resetSize)
	// Closing to the tray hides this same window instead of tearing it down (see below), so this
	// full creation only ever runs once per launch; restoring from the tray afterwards is instant.
	desktop.InterceptClose(hwnd, func() bool {
		if !d.app.Settings.Get().CloseToTray {
			return false // let it close (and quit) as usual
		}
		pShowWindow.Call(hwnd, 0) // SW_HIDE
		return true
	})
	stopWatch := make(chan struct{})
	go d.watchMinimize(w, hwnd, stopWatch)
	go d.watchForHang(stopWatch)
	go d.place.watch(stopWatch)
	setWindowIcon(hwnd)
	// Title bar in the colours of the page: first by the system theme (no white flash), then by what the
	// page reports about its own top bar.
	if desktop.SystemUsesDarkTheme() {
		_ = desktop.TitleBar(hwnd, "#1b1f26", "#e6e9ef")
	} else {
		_ = desktop.TitleBar(hwnd, "#ffffff", "#1a1f29")
	}
	_ = w.Bind("setTitleBar", func(bg, fg string) { _ = desktop.TitleBar(hwnd, bg, fg) })
	// A link to a site (the tracker page of a torrent, the project page) opens in the user's own browser, not in a
	// window of this program. Only web addresses are passed on.
	bindExternal(w)
	// The "Add" button and dropped files open the window for adding torrents instead of a dialog over this one; an
	// error text (empty on success) lets the page fall back to the dialog.
	_ = w.Bind("openAddWindow", func() string {
		allowForeground()
		if err := spawnAddWindow(d.stateDir); err != nil {
			log.Println("add window:", err)
			return err.Error()
		}
		return ""
	})
	// The page gets the access key from here, so the window never depends on the link or on what the
	// web view remembered. The address is then the plain one, without the key.
	//
	// Init runs on every document this window ever shows, not only the program's own: a dropped link that
	// WebView2 navigated to before app.js could stop it (see the page's own "drop" handler) would get the key
	// and every binding along with it, same as the real page. The origin check keeps that from mattering.
	origin := strings.SplitN(d.app.URL, "/#", 2)[0]
	w.Init("if (location.origin === " + strconv.Quote(origin) + ") { window.__equinoxToken = " + strconv.Quote(d.app.Token) + "; window.__equinoxDesktop = true; }")
	d.place.attach(w, hwnd) // the size, position and state the window had last time
	w.Navigate(strings.SplitN(d.app.URL, "/#", 2)[0] + "/" + map[bool]string{true: "#settings"}[d.settingsOnLoad.Swap(false)])
	go func() { // give the local page a moment to paint before the window is shown
		time.Sleep(300 * time.Millisecond)
		pShowWindow.Call(hwnd, 9) // SW_RESTORE
		pSetForeground.Call(hwnd)
	}()
	w.Run()
	log.Println("window: closed")
	close(stopWatch)

	d.mu.Lock()
	d.view = nil
	d.mu.Unlock()
	w.Destroy()
	if !d.app.Settings.Get().CloseToTray {
		go d.doQuit() // closing the window means exit unless the user asked for the tray
	}
}

// watchMinimize hides the window into the tray when it is minimised (if the setting asks for
// it), the same way closing it does: the WebView2 instance stays alive behind it, so a later
// restore is instant instead of a full recreate. Keeps watching afterwards, for the next time.
func (d *desktop_) watchMinimize(w webview2.WebView, hwnd uintptr, stop <-chan struct{}) {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if !d.app.Settings.Get().MinimizeToTray {
				continue
			}
			if r, _, _ := pIsIconic.Call(hwnd); r != 0 {
				w.Dispatch(func() { pShowWindow.Call(hwnd, 0) }) // SW_HIDE
			}
		}
	}
}

// hung reports whether the main window exists and has stopped responding to Windows: WebView2 has been seen to
// wedge like this after running a long time (its render or GPU process deadlocks while the rest of the program,
// including the HTTP API, keeps working fine). Window() is just a stored handle, never a cross-thread call, so
// this is safe to call at any time, hang or not.
func (d *desktop_) hung() bool {
	d.mu.Lock()
	v := d.view
	d.mu.Unlock()
	if v == nil {
		return false
	}
	r, _, _ := pIsHungAppWindow.Call(uintptr(v.Window()))
	return r != 0
}

// trayWindowHandle finds this process's own tray icon window (energye/systray's message-only window, class
// "SystrayClass"): the library keeps no handle of its own to ask for. 0 if it cannot be found (the tray has not
// been created yet, or never will be, e.g. -hidden was not used and this build has none).
func trayWindowHandle() uintptr {
	pid := uint32(os.Getpid())
	var found uintptr
	cb := windows.NewCallback(func(h, _ uintptr) uintptr {
		var owner uint32
		pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&owner)))
		if owner != pid {
			return 1 // keep enumerating
		}
		var cls [64]uint16
		n, _, _ := pGetClassName.Call(h, uintptr(unsafe.Pointer(&cls[0])), uintptr(len(cls)))
		if windows.UTF16ToString(cls[:n]) == "SystrayClass" {
			found = h
			return 0 // stop: found it
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	return found
}

// trayHung reports whether the tray icon's own window has stopped responding to Windows. It runs on the same
// message loop as a click (see main's systray.SetOnClick), all the way inside the window procedure: if that loop
// is stuck, the tray stops reacting to anything — clicks, the right-click menu, icon and tooltip updates — until
// the whole program is restarted, same as the main window hanging (see hung/recoverHungWindow), just a different
// window. EnumWindows/IsHungAppWindow are not cross-thread calls to the tray itself, so this is always safe to call.
func (d *desktop_) trayHung() bool {
	h := trayWindowHandle()
	if h == 0 {
		return false
	}
	r, _, _ := pIsHungAppWindow.Call(h)
	return r != 0
}

// watchForHang checks a few times an hour whether the window, while sitting hidden in the tray, or the tray icon
// itself, has stopped responding, and restarts the program if either has — see recoverHungWindow. Two checks in a
// row have to agree (a few minutes apart) before it acts, so a single busy moment is not mistaken for a real hang.
// The main window's check never touches one that is on screen, so it alone cannot interrupt anyone actually
// looking at it; the tray's check is not conditioned on that (a hung tray is invisible either way), so recovering
// from one can still close a window that was open and fine — there is no way to unstick only the tray.
func (d *desktop_) watchForHang(stop <-chan struct{}) {
	tick := time.NewTicker(2 * time.Minute)
	defer tick.Stop()
	winStrikes, trayStrikes := 0, 0
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if d.trayHung() {
			trayStrikes++
		} else {
			trayStrikes = 0
		}

		d.mu.Lock()
		v := d.view
		d.mu.Unlock()
		if v == nil {
			if trayStrikes >= 2 {
				d.recoverHungWindow()
			}
			return
		}
		if vis, _, _ := pIsWindowVisible.Call(uintptr(v.Window())); vis != 0 {
			winStrikes = 0
		} else if d.hung() {
			winStrikes++
		} else {
			winStrikes = 0
		}
		if winStrikes >= 2 || trayStrikes >= 2 {
			d.recoverHungWindow()
			return
		}
	}
}

// recoverHungWindow restarts the whole program: once the window's own thread has wedged, nothing above this can
// unstick it, including the graceful shutdown restart() itself asks for (it posts the request to that same
// thread, which never gets to it). A fresh copy is started first, exactly as a normal restart does; this one
// then exits at once rather than waiting on a close that will not come. Every write this program makes to disk
// is already crash-safe (internal/atomicfile, the recovery of an interrupted move on the next start), so this
// is no different from the abrupt ends those already answer for.
func (d *desktop_) recoverHungWindow() {
	log.Println("window: not responding, restarting")
	if msg := d.restart(); msg != "" {
		log.Println("window: could not restart:", msg)
		return
	}
	time.Sleep(time.Second)
	os.Exit(1)
}

// doQuit shuts the application down (once, whoever asks first).
func (d *desktop_) doQuit() {
	d.quitOnce.Do(func() {
		systray.Quit()
		close(d.quit)
		d.closeWindow() // unblocks the window loop so main can shut down
	})
}

// restart starts a fresh copy of the application that waits for this one to exit, then
// shuts this one down. It returns an error text (empty on success) for the page.
func (d *desktop_) restart() string {
	exe, err := os.Executable()
	if err != nil {
		return err.Error()
	}
	args := []string{"-after", strconv.Itoa(os.Getpid()), "-state", d.stateDir, "-listen", d.listen}
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err.Error()
	}
	go d.doQuit() // after the reply has been sent to the page
	return ""
}

// installUpdate downloads and verifies the latest release's installer and runs it silently;
// the installer's own InitializeSetup (see installer/equinox.iss) closes this process and,
// since it is passed /autoupdate=1, reopens the app once the update is in place. The download
// and install run in the background (GET /api/update-progress reports how far it got, for the
// page's progress dialog); this only returns an error text (empty on success) once the check
// that finds the release to install is done.
func (d *desktop_) installUpdate() string {
	d.checkingUpdate.Store(true)
	defer d.checkingUpdate.Store(false)
	checkCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := update.Check(checkCtx, false)
	if err != nil {
		return err.Error()
	}
	if info == nil {
		return "" // someone else already updated, or a background check just missed it
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := info.Install(ctx, filepath.Join(d.stateDir, "update"), true); err != nil {
			return // the page reads the reason from GET /api/update-progress
		}
		// The installer is running and waits for this process to go. Closing ourselves here
		// means Manager.Close gets to flush the counters and settle any storage move, which a
		// forced kill from the installer would not.
		log.Println("update: installer started, closing for the update")
		d.doQuit()
	}()
	return ""
}

// waitForExit blocks until the process with the given id has ended (or the time is up).
func waitForExit(pid int, limit time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, uint32(limit/time.Millisecond))
}

// appTitle is the name in the window title and the tray tooltip: with the version in it for a test build,
// so that it can be told from the installed program running beside it.
func appTitle() string {
	if buildinfo.Channel() != "" {
		return "Equinox " + buildinfo.Version
	}
	return "Equinox"
}

// requestShow asks the main loop to open (or raise) the window.
func (d *desktop_) requestShow() {
	d.mu.Lock()
	v := d.view
	d.mu.Unlock()
	if v != nil {
		// installUpdate's own check (see checkingUpdate) blocks the window thread for up to 30s: Windows would
		// call that a hang too, and unlike a real one it is expected to pass, so it must not be mistaken for one
		// here and used as a reason to kill the process mid-update.
		if !d.checkingUpdate.Load() && d.hung() { // asking a dead window to come forward would do nothing visible; restart instead
			d.recoverHungWindow()
			return
		}
		// The window survives being closed or minimised to the tray (see window/watchMinimize):
		// main's loop is blocked inside w.Run() for as long as that is true, so it never gets
		// back to select on d.show to notice a request here. Show the existing window directly.
		//
		// Dispatch, not a direct call: ShowWindow/SetForegroundWindow (inside bringToFront) wait on the target
		// window's own thread the way SendMessage does, so calling them here would block whatever thread asked
		// to show the window — the tray icon's, for a click, and requestShow runs inside its own message
		// handling (systray's wndProc calls the click callback straight from its message loop). A momentary
		// stall in the window (not yet long enough for hung() above to catch) would then wedge that loop for as
		// long as the stall lasts, and it would stop answering clicks until the whole program is restarted, same
		// as a real hang — this happened once already. Dispatch only ever queues the call and returns.
		hwnd := uintptr(v.Window())
		v.Dispatch(func() { bringToFront(hwnd) })
		return
	}
	select {
	case d.show <- struct{}{}:
	default:
	}
}

// closeWindow asks a running window loop to end (safe from any thread).
func (d *desktop_) closeWindow() {
	d.mu.Lock()
	v := d.view
	d.mu.Unlock()
	if v != nil {
		// PostQuitMessage acts on the calling thread, so it has to run on the window's own
		// thread: Dispatch hands the call over (Terminate alone would do nothing from here).
		v.Dispatch(v.Terminate)
	}
}

// keepTrayMenu starts the menu process again when it has died, a few times at most: with no WebView2 it would die
// every time, and the system's menu serves then.
func (d *desktop_) keepTrayMenu() {
	for tries := 0; tries < 3; {
		time.Sleep(5 * time.Second)
		select {
		case <-d.quit:
			return
		default:
		}
		if trayMenuAlive() {
			continue
		}
		tries++
		log.Println("tray menu: not running, starting it again")
		_ = spawnTrayMenu(d.stateDir)
	}
}

func (d *desktop_) onTrayReady() {
	log.Println("tray: ready")
	systray.SetIcon(desktop.Icon())
	systray.SetTitle(appTitle())
	systray.SetTooltip(appTitle())
	desktop.WatchNotificationClicks(d.requestShow) // clicking a notification opens the window

	open := systray.AddMenuItem("", "")
	status := systray.AddMenuItem("", "")
	status.Disable()
	systray.AddSeparator()
	turtle := systray.AddMenuItemCheckbox("", "", d.app.Settings.Get().AltSpeedActive)
	systray.AddSeparator()
	auto := systray.AddMenuItemCheckbox("", "", desktop.AutostartEnabled())
	assoc := systray.AddMenuItem("", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("", "")
	// the words of the menu, in the language of the choice; done again when the choice changes
	shown := ""
	label := func() {
		shown = lang.Current()
		for _, it := range []struct {
			item       *systray.MenuItem
			title, tip string
		}{
			{open, "Открыть Equinox", "Показать окно"},
			{turtle, "Ограничение скорости", "Режим «черепаха»"},
			{auto, "Запускать вместе с Windows", "Запуск в трее при входе в систему"},
			{assoc, "Сделать торрент-клиентом по умолчанию…", "Выбрать Equinox в «Приложения по умолчанию»"},
			{quit, "Выход", "Остановить все раздачи и выйти"},
		} {
			it.item.SetTitle(lang.Tr(it.title))
			it.item.SetTooltip(lang.Tr(it.tip))
		}
	}
	label()
	{
		bits, en := d.app.Settings.Get().SpeedUnit == "bits", lang.English()
		status.SetTitle(fmt.Sprintf("↓ %s   ↑ %s", desktop.FormatRateIn(0, bits, en), desktop.FormatRateIn(0, bits, en)))
	}

	open.Click(d.requestShow)
	systray.SetOnClick(func(systray.IMenu) { d.requestShow() })
	// The menu of the right click is a window of its own (trayhost_windows.go); the system's menu is the way out
	// when that process is not there.
	if err := spawnTrayMenu(d.stateDir); err != nil {
		log.Println("tray menu: cannot start:", err)
	}
	go d.keepTrayMenu()
	systray.SetOnRClick(func(m systray.IMenu) {
		if !signalTrayMenu() {
			_ = m.ShowMenu()
		}
	})
	turtle.Click(func() {
		on := !d.app.Settings.Get().AltSpeedActive
		if err := d.app.Manager.SetAltSpeed(on); err != nil {
			log.Println("turtle:", err)
			return
		}
		if on {
			turtle.Check()
		} else {
			turtle.Uncheck()
		}
	})
	auto.Click(func() {
		want := !auto.Checked()
		if err := desktop.SetAutostart(want, d.app.Settings.Get().StartHidden); err != nil {
			log.Println("autostart:", err)
			msgBox(lang.Tr("Не удалось изменить автозапуск:") + "\n" + err.Error())
			return
		}
		if want {
			auto.Check()
		} else {
			auto.Uncheck()
		}
	})
	assoc.Click(func() {
		if err := desktop.RegisterHandlers(); err != nil {
			log.Println("register handlers:", err)
			msgBox(lang.Tr("Не удалось зарегистрировать приложение:") + "\n" + err.Error())
			return
		}
		// Windows asks the user to confirm the default app itself.
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", "ms-settings:defaultapps").Start()
	})
	quit.Click(d.doQuit)

	// Keep the menu in sync with what the web UI does.
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-d.quit:
				return // the tray icon is gone, stop touching it
			case <-tick.C:
			}
			var down, up int64
			for _, t := range d.app.Manager.List() {
				down += t.DownRate
				up += t.UpRate
			}
			if lang.Current() != shown {
				label()
			}
			bits, en := d.app.Settings.Get().SpeedUnit == "bits", lang.English()
			status.SetTitle(fmt.Sprintf("↓ %s   ↑ %s", desktop.FormatRateIn(down, bits, en), desktop.FormatRateIn(up, bits, en)))
			systray.SetTooltip(fmt.Sprintf("%s  ↓ %s  ↑ %s", appTitle(), desktop.FormatRateIn(down, bits, en), desktop.FormatRateIn(up, bits, en)))
			if d.app.Settings.Get().AltSpeedActive != turtle.Checked() {
				if turtle.Checked() {
					turtle.Uncheck()
				} else {
					turtle.Check()
				}
			}
		}
	}()
}

// setWindowIcon gives the window (title bar, taskbar) the drawn icon.
func setWindowIcon(hwnd uintptr) {
	dib := desktop.DIBFromIcon(desktop.Icon())
	if len(dib) == 0 {
		return
	}
	h, _, _ := pCreateIconFromR.Call(uintptr(unsafe.Pointer(&dib[0])), uintptr(len(dib)), 1, 0x00030000, 32, 32, 0)
	if h == 0 {
		return
	}
	const wmSetIcon = 0x0080
	pSendMessage.Call(hwnd, wmSetIcon, 0, h) // small
	pSendMessage.Call(hwnd, wmSetIcon, 1, h) // big
}

func msgBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Equinox")
	pMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x10)
}

func fatalBox(err error) {
	log.Println("fatal:", err)
	msgBox(lang.Tr("Не удалось запустить Equinox:") + "\n" + err.Error())
	os.Exit(1)
}

// logf writes to the application log.
func logf(format string, args ...any) { log.Printf(format, args...) }

// notify turns what the engine reports into a Windows notification.
func (d *desktop_) notify(e core.Event) {
	if !d.app.Settings.Get().NotifyOnComplete {
		return
	}
	var title, text string
	switch e.Kind {
	case "completed":
		title, text = lang.Tr("Загрузка завершена"), e.Name
	case "limit":
		title, text = lang.Tr("Раздача остановлена"), e.Name+" — "+lang.Tr("достигнут лимит:")+" "+lang.LimitDetail(e.Detail, e.DetailCode, e.DetailArgs)
	default:
		return
	}
	if err := desktop.Notify(title, text); err != nil {
		log.Println("notification:", err)
	}
}

// spawnAddWindow starts the window for adding torrents as a process of its own.
func spawnAddWindow(stateDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return exec.Command(exe, "-addwindow", "-state", stateDir).Start()
}

// openInBrowser hands a web address to the user's default browser; anything else is ignored.
func openInBrowser(raw string) {
	if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u.String()).Start()
	}
}

func mustUTF16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}
