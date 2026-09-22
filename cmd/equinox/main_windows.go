// Command equinox is the Windows application: the daemon, a window with the web
// interface and a tray icon. Closing the window keeps everything running in the tray.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/energye/systray"
	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"github.com/Ozzvin/equinox/internal/app"
	"github.com/Ozzvin/equinox/internal/core"
	"github.com/Ozzvin/equinox/internal/desktop"
	"github.com/Ozzvin/equinox/internal/update"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	pShowWindow      = user32.NewProc("ShowWindow")
	pIsIconic        = user32.NewProc("IsIconic")
	pSetForeground   = user32.NewProc("SetForegroundWindow")
	pSendMessage     = user32.NewProc("SendMessageW")
	pCreateIconFromR = user32.NewProc("CreateIconFromResourceEx")
	pMessageBox      = user32.NewProc("MessageBoxW")
)

func main() {
	// The webview and its message loop must stay on the main OS thread.
	runtime.LockOSThread()

	stateDir := flag.String("state", "", "state directory (default: next to the executable, or a per-user folder if that is not writable)")
	listen := flag.String("listen", "127.0.0.1:9091", "HTTP address (must be loopback)")
	hidden := flag.Bool("hidden", false, "start in the tray without opening the window")
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

	// One instance only: a second start asks the running one to show its window.
	name, _ := windows.UTF16PtrFromString(`Local\EquinoxDesktop`)
	evName, _ := windows.UTF16PtrFromString(`Local\EquinoxDesktopShow`)
	if _, err := windows.CreateMutex(nil, false, name); err == windows.ERROR_ALREADY_EXISTS {
		if len(args) > 0 {
			if err := forward(*stateDir, args); err != nil {
				log.Println("forward to running instance:", err)
			}
		}
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
	log.Println("starting engine")
	a, err := app.Start(*stateDir, *listen)
	if err != nil {
		fatalBox(err)
	}
	log.Println("engine started, api at", a.URL[:strings.Index(a.URL, "#")])
	// Lets a second launch find this instance; like api-token, the file holds the access key.
	_ = os.WriteFile(filepath.Join(*stateDir, "ui-url"), []byte(a.URL), 0o600)
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
	if !*hidden || len(args) > 0 {
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
		WindowOptions: webview2.WindowOptions{Title: "Equinox", Width: uint(d.place.startSize().W), Height: uint(d.place.startSize().H), Center: !d.place.hasPos()},
	})
	if w == nil {
		d.mu.Unlock()
		msgBox("Не удалось открыть окно: не найден Microsoft Edge WebView2 Runtime.\nПриложение продолжает работать в трее, интерфейс доступен в браузере:\n" + d.app.URL)
		return
	}
	d.view = w
	d.mu.Unlock()
	log.Println("window: created, navigating")

	hwnd := uintptr(w.Window())
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
	stopWatch := make(chan struct{})
	go d.watchMinimize(w, hwnd, stopWatch)
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
	// The page gets the access key from here, so the window never depends on the link or on what the
	// web view remembered. The address is then the plain one, without the key.
	w.Init("window.__equinoxToken = " + strconv.Quote(d.app.Token) + "; window.__equinoxDesktop = true;")
	d.place.attach(w, hwnd) // the size, position and state the window had last time
	w.Navigate(strings.SplitN(d.app.URL, "/#", 2)[0] + "/")
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

// watchMinimize closes the window into the tray when it is minimised (if the setting asks for it).
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
				w.Dispatch(w.Terminate)
				return
			}
		}
	}
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
// since it is passed /autoupdate=1, reopens the app once the update is in place. It returns
// an error text (empty on success) for the page.
func (d *desktop_) installUpdate() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	info, err := update.Check(ctx, false)
	if err != nil {
		return err.Error()
	}
	if info == nil {
		return "" // someone else already updated, or a background check just missed it
	}
	if err := info.Install(ctx, filepath.Join(d.stateDir, "update"), true); err != nil {
		return err.Error()
	}
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

// requestShow asks the main loop to open (or raise) the window.
func (d *desktop_) requestShow() {
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

func (d *desktop_) onTrayReady() {
	log.Println("tray: ready")
	systray.SetIcon(desktop.Icon())
	systray.SetTitle("Equinox")
	systray.SetTooltip("Equinox")
	desktop.WatchNotificationClicks(d.requestShow) // clicking a notification opens the window

	open := systray.AddMenuItem("Открыть Equinox", "Показать окно")
	status := systray.AddMenuItem("↓ 0 Б/с   ↑ 0 Б/с", "")
	status.Disable()
	systray.AddSeparator()
	turtle := systray.AddMenuItemCheckbox("Ограничение скорости", "Режим «черепаха»", d.app.Settings.Get().AltSpeedActive)
	systray.AddSeparator()
	auto := systray.AddMenuItemCheckbox("Запускать вместе с Windows", "Запуск в трее при входе в систему", desktop.AutostartEnabled())
	assoc := systray.AddMenuItem("Открывать magnet и .torrent в Equinox…", "Выбрать Equinox в «Приложения по умолчанию»")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Выход", "Остановить все раздачи и выйти")

	open.Click(d.requestShow)
	systray.SetOnClick(func(systray.IMenu) { d.requestShow() })
	systray.SetOnRClick(func(m systray.IMenu) { _ = m.ShowMenu() })
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
			msgBox("Не удалось изменить автозапуск:\n" + err.Error())
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
			msgBox("Не удалось зарегистрировать приложение:\n" + err.Error())
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
			bits := d.app.Settings.Get().SpeedUnit == "bits"
			status.SetTitle(fmt.Sprintf("↓ %s   ↑ %s", desktop.FormatRate(down, bits), desktop.FormatRate(up, bits)))
			systray.SetTooltip(fmt.Sprintf("Equinox  ↓ %s  ↑ %s", desktop.FormatRate(down, bits), desktop.FormatRate(up, bits)))
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
	msgBox("Не удалось запустить Equinox:\n" + err.Error())
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
		title, text = "Загрузка завершена", e.Name
	case "limit":
		title, text = "Раздача остановлена", e.Name+" — достигнут лимит: "+e.Detail
	default:
		return
	}
	if err := desktop.Notify(title, text); err != nil {
		log.Println("notification:", err)
	}
}
