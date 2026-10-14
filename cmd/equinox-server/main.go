// Command equinox-server is the server variant: the engine and the web interface with no
// window and no tray. It is started once and opened from any browser; the access key travels in
// the link that is opened for you, so it never has to be typed. The PC variant with its own
// window is cmd/equinox.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Ozzvin/equinox/internal/app"
)

func main() {
	stateDir := flag.String("state", "", "state directory (default: next to the executable, or a per-user folder if that is not writable)")
	listen := flag.String("listen", "127.0.0.1:9091", "HTTP address (must be loopback on Windows; a non-loopback address needs -open where that flag exists)")
	open, allowHosts := openFlags() // see open_windows.go: not registered at all on a Windows build
	downloads := flag.String("downloads", "", "where a first start saves torrents (default: the Downloads folder)")
	add := flag.String("add", "", ".torrent file or magnet link to add at startup")
	noBrowser := flag.Bool("no-browser", false, "do not open the web interface in the browser")
	flag.Parse()
	if *stateDir == "" {
		*stateDir = app.DefaultStateDir()
	}

	if err := os.MkdirAll(*stateDir, 0o755); err != nil {
		fatal(err)
	}
	// One server per state directory: a second start opens the page of the one running instead.
	lock, err := lockInstance(*stateDir)
	if errors.Is(err, errRunning) {
		url, ok := runningURL(*stateDir, 20*time.Second)
		if !ok {
			fatal(errors.New("another server runs on this state directory, but its page does not answer"))
		}
		fmt.Println("Already running:", withoutKey(url))
		if !*noBrowser {
			openBrowser(url)
		}
		return
	}
	if err != nil {
		fatal(err)
	}
	defer lock.Close()
	uiFile := filepath.Join(*stateDir, "ui-url")
	_ = os.Remove(uiFile) // the last run's, if it did not end cleanly: its key must not go to whoever answers there now

	var hosts []string
	for _, h := range strings.Split(*allowHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	a, err := app.StartWith(*stateDir, *listen, app.Options{Open: *open, AllowedHosts: hosts, Downloads: *downloads})
	if err != nil {
		fatal(err)
	}
	defer a.Close()
	_ = os.WriteFile(uiFile, []byte(a.URL), 0o600) // holds the key, like api-token
	defer os.Remove(uiFile)

	fmt.Println("torrent port :", a.Manager.Port())
	shown := withoutKey(a.URL)
	if *noBrowser {
		shown = a.URL // nobody opens the page for you: the link must carry the key
	}
	fmt.Println("Web interface:", shown)
	if *open {
		fmt.Println("Open mode    : no access key; the proxy in front must sign users in")
	} else {
		fmt.Println("Access key   :", filepath.Join(*stateDir, "api-token"), "(the opened link already contains it)")
	}
	fmt.Println("Ctrl+C stops the server.")

	if *add != "" {
		var h string
		if strings.HasPrefix(*add, "magnet:") {
			h, err = a.Manager.AddMagnet(*add)
		} else {
			h, err = a.Manager.AddFile(*add)
		}
		if err != nil {
			fatal(err)
		}
		fmt.Println("added", h)
	}
	if !*noBrowser {
		openBrowser(a.URL)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}

// runningURL returns the link of the server that holds the lock on this state directory, once its page answers: it
// may be starting still, and writes ui-url only once it is up (the one of a run before is gone by then).
func runningURL(stateDir string, wait time.Duration) (string, bool) {
	deadline := time.Now().Add(wait)
	c := &http.Client{Timeout: 2 * time.Second}
	for {
		if raw, err := os.ReadFile(filepath.Join(stateDir, "ui-url")); err == nil {
			url := strings.TrimSpace(string(raw))
			if base, _, ok := strings.Cut(url, "/#token="); ok {
				if res, err := c.Get(base + "/"); err == nil {
					res.Body.Close()
					return url, true
				}
			}
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// errRunning means another server holds the state directory.
var errRunning = errors.New("another server runs on this state directory")

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println("could not open the browser:", err, "- open the link above yourself")
		return
	}
	go cmd.Wait()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// withoutKey drops the access key from a link so it is not printed where others may read it.
func withoutKey(url string) string {
	base, _, _ := strings.Cut(url, "/#token=")
	return base
}
