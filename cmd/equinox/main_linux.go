// Command equinox on Linux: the engine and a tray icon, with the web interface in the user's own browser (there is
// no window of its own). Opening a .torrent file or a magnet link while the program runs hands it to the running
// copy, and a browser tab with the dialog for adding comes up.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/energye/systray"

	"github.com/Ozzvin/equinox/internal/app"
	"github.com/Ozzvin/equinox/internal/buildinfo"
	"github.com/Ozzvin/equinox/internal/core"
	"github.com/Ozzvin/equinox/internal/desktop"
	"github.com/Ozzvin/equinox/internal/lang"
)

func main() {
	stateDir := flag.String("state", "", "state directory (default: next to the executable, or ~/.config/Equinox if that is not writable)")
	listen := flag.String("listen", "127.0.0.1:9091", "HTTP address (must be loopback)")
	hidden := flag.Bool("hidden", false, "start in the tray without opening the browser")
	flag.Parse()
	args := fileArgs(flag.Args()) // magnet links and .torrent files, from the .desktop file's %U
	if *stateDir == "" {
		*stateDir = app.DefaultStateDir()
	}
	if err := os.MkdirAll(*stateDir, 0o755); err != nil {
		fatal(err)
	}

	// One copy per state directory. A second start hands its links to the running one and opens its page.
	lock, err := lockInstance(*stateDir)
	if errors.Is(err, errRunning) {
		base, token, err := waitForUI(*stateDir, 20*time.Second)
		if err != nil {
			fatal(err)
		}
		if len(args) > 0 {
			if err := forward(*stateDir, args); err != nil {
				fmt.Fprintln(os.Stderr, "could not hand over to the running Equinox:", err)
			}
			openBrowser(addURL(base, token))
			return
		}
		openBrowser(base + "/#token=" + token)
		return
	}
	if err != nil {
		fatal(err)
	}
	defer lock.Close()
	uiFile := filepath.Join(*stateDir, "ui-url")
	_ = os.Remove(uiFile) // the last run's, if it did not end cleanly: see waitForUI

	// A log file, as on Windows: started from the menu, the program has nobody reading its output.
	if err := app.SetupLog(filepath.Join(*stateDir, "equinox.log"), 5<<20); err != nil {
		log.Println("cannot open the log file:", err)
	}
	log.Println("starting engine")
	a, err := app.StartWith(*stateDir, *listen, app.Options{Desktop: desktopHooks{}})
	if err != nil {
		fatal(err)
	}
	lang.SetPreference(func() string { return a.Settings.Get().Language })
	base, _, _ := strings.Cut(a.URL, "/#token=")
	log.Println("engine started, api at", base)
	_ = os.WriteFile(uiFile, []byte(a.URL), 0o600) // holds the key, like api-token
	addArgs(a, args)

	d := &desktopApp{app: a, base: base, quit: make(chan struct{})}
	a.Manager.OnEvent(d.notify)
	desktop.WatchNotificationClicks(d.openMain)
	if desktop.HaveSessionBus() {
		d.tray = true
		go systray.Run(d.onTrayReady, func() {})
	} else {
		log.Println("tray: no D-Bus session bus, running without the tray icon")
	}

	switch {
	case len(args) > 0:
		openBrowser(addURL(base, a.Token))
	case !*hidden:
		d.openMain()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		s := <-sig
		log.Println("quit on", s)
		d.doQuit()
	}()
	<-d.quit
	a.Close()
	_ = os.Remove(uiFile)
	log.Println("stopped")
}

type desktopApp struct {
	app      *app.App
	base     string // the page's address without the key
	quit     chan struct{}
	quitOnce sync.Once
	tray     bool // the tray icon was started
}

func (d *desktopApp) openMain()     { openBrowser(d.base + "/#token=" + d.app.Token) }
func (d *desktopApp) openSettings() { openBrowser(d.base + "/#token=" + d.app.Token + "&settings") }
func (d *desktopApp) openAdd()      { openBrowser(addURL(d.base, d.app.Token)) }

func (d *desktopApp) doQuit() {
	d.quitOnce.Do(func() {
		if d.tray {
			systray.Quit()
		}
		close(d.quit)
	})
}

func (d *desktopApp) onTrayReady() {
	log.Println("tray: ready")
	systray.SetIcon(desktop.PNG(64))
	systray.SetTitle(appTitle())
	systray.SetTooltip(appTitle())

	open := systray.AddMenuItem("", "")
	status := systray.AddMenuItem("", "")
	status.Disable()
	systray.AddSeparator()
	add := systray.AddMenuItem("", "")
	settings := systray.AddMenuItem("", "")
	systray.AddSeparator()
	turtle := systray.AddMenuItemCheckbox("", "", d.app.Settings.Get().AltSpeedActive)
	auto := systray.AddMenuItemCheckbox("", "", desktop.AutostartEnabled())
	systray.AddSeparator()
	quit := systray.AddMenuItem("", "")
	// the words of the menu, in the language of the choice; done again when the choice changes
	shown := ""
	label := func() {
		shown = lang.Current()
		for _, it := range []struct {
			item  *systray.MenuItem
			title string
		}{
			{open, "Открыть Equinox"},
			{add, "Добавить раздачу…"},
			{settings, "Настройки"},
			{turtle, "Ограничение скорости"},
			{auto, "Запускать при входе в систему"},
			{quit, "Выход"},
		} {
			it.item.SetTitle(lang.Tr(it.title))
		}
	}
	label()
	rates := func(down, up int64) string {
		bits, en := d.app.Settings.Get().SpeedUnit == "bits", lang.English()
		return fmt.Sprintf("↓ %s   ↑ %s", desktop.FormatRateIn(down, bits, en), desktop.FormatRateIn(up, bits, en))
	}
	status.SetTitle(rates(0, 0))

	systray.SetOnClick(func(systray.IMenu) { d.openMain() })
	open.Click(d.openMain)
	add.Click(d.openAdd)
	settings.Click(d.openSettings)
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
			return
		}
		if want {
			auto.Check()
		} else {
			auto.Uncheck()
		}
	})
	quit.Click(d.doQuit)

	// Keep the menu in sync with what the web page does.
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-d.quit:
				return
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
			status.SetTitle(rates(down, up))
			systray.SetTooltip(appTitle() + "  " + rates(down, up))
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

// desktopHooks answer the system section of the settings (see api.Desktop).
type desktopHooks struct{}

func (desktopHooks) AutostartEnabled() bool             { return desktop.AutostartEnabled() }
func (desktopHooks) SetAutostart(on, hidden bool) error { return desktop.SetAutostart(on, hidden) }
func (desktopHooks) MakeDefault() error                 { return desktop.MakeDefault() }

// notify turns what the engine reports into a desktop notification.
func (d *desktopApp) notify(e core.Event) {
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

// appTitle is the name in the tray: with the version in it for a test build.
func appTitle() string {
	if buildinfo.Channel() != "" {
		return "Equinox " + buildinfo.Version
	}
	return "Equinox"
}

// addURL is the page that is just the dialog for adding torrents; it takes what was queued.
func addURL(base, token string) string { return base + "/?window=add#token=" + token }

// fileArgs turns the file:// addresses a file manager may pass (%U in the .desktop file) into paths.
func fileArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "file://") {
			if u, err := url.Parse(a); err == nil && u.Path != "" {
				a = u.Path
			}
		}
		out = append(out, a)
	}
	return out
}

var errRunning = errors.New("Equinox is already running")

// lockInstance takes the lock that makes this the one running copy for the state directory. The lock goes with the
// process, so a copy that crashed leaves nothing behind that would stop the next one.
func lockInstance(stateDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(stateDir, "equinox.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errRunning
		}
		return nil, err
	}
	return f, nil
}

// openBrowser opens a page of the program in the user's default browser.
func openBrowser(u string) {
	cmd := exec.Command("xdg-open", u)
	if err := cmd.Start(); err != nil {
		log.Println("could not open the browser:", err)
		return
	}
	go cmd.Wait()
}

func fatal(err error) {
	log.Println("fatal:", err)
	fmt.Fprintln(os.Stderr, "error:", err)
	_ = desktop.Notify(lang.Tr("Не удалось запустить Equinox:"), err.Error()) // started from the menu, nobody sees stderr
	os.Exit(1)
}

// logf writes to the application log.
func logf(format string, args ...any) { log.Printf(format, args...) }
