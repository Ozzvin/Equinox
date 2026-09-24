// Command equinox-server is the server variant: the engine and the web interface with no
// window and no tray. It is started once and opened from any browser; the access key travels in
// the link that is opened for you, so it never has to be typed. The PC variant with its own
// window is cmd/equinox.
package main

import (
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
	listen := flag.String("listen", "127.0.0.1:9091", "HTTP address (must be loopback, unless -open)")
	open := flag.Bool("open", false, "run behind a proxy that signs users in (Umbrel): listen on any address and ask for no key. Never expose this port to a network that is not trusted")
	downloads := flag.String("downloads", "", "where a first start saves torrents (default: the Downloads folder)")
	allowHosts := flag.String("allow-host", "", "with -open: extra host names to accept, comma separated (IP addresses, short names and .local names always pass)")
	add := flag.String("add", "", ".torrent file or magnet link to add at startup")
	noBrowser := flag.Bool("no-browser", false, "do not open the web interface in the browser")
	flag.Parse()
	if *stateDir == "" {
		*stateDir = app.DefaultStateDir()
	}

	// Already running on this state directory: open its page instead of starting a second one.
	if url, ok := runningURL(*stateDir); ok {
		fmt.Println("Already running:", withoutKey(url))
		if !*noBrowser {
			openBrowser(url)
		}
		return
	}

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
	uiFile := filepath.Join(*stateDir, "ui-url")
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

// runningURL returns the link of a server that already works on this state directory.
func runningURL(stateDir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(stateDir, "ui-url"))
	if err != nil {
		return "", false
	}
	url := strings.TrimSpace(string(raw))
	base, _, ok := strings.Cut(url, "/#token=")
	if !ok {
		return "", false
	}
	c := &http.Client{Timeout: 2 * time.Second}
	res, err := c.Get(base + "/")
	if err != nil {
		return "", false // a stale file left by a server that did not exit cleanly
	}
	res.Body.Close()
	return url, true
}

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
