package app

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A server behind a signing-in proxy listens on any address and hands out the open key; without the option
// only loopback addresses are accepted. A first start saves into the folder it is told to.
func TestOpenServer(t *testing.T) {
	dir := t.TempDir()
	if a, err := Start(dir, "0.0.0.0:0"); err == nil {
		a.Close()
		t.Fatal("a non-loopback address must be refused without the open option")
	}

	dl := filepath.Join(dir, "dl")
	a, err := StartWith(dir, "127.0.0.1:0", Options{Open: true, Downloads: dl})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); time.Sleep(500 * time.Millisecond) })

	if strings.Contains(a.URL, "token=") {
		t.Errorf("an open server has no key in its link: %s", a.URL)
	}
	if got := a.Settings.Get().DataDir; got != dl {
		t.Errorf("a first start must save into %s, got %s", dl, got)
	}
	if !a.Settings.Get().PortMapping {
		t.Error("an open server keeps the port mapping on: in a container NAT-PMP finds the router on the way out")
	}
	res, err := http.Get(strings.TrimSuffix(a.URL, "/") + "/session.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), "open") {
		t.Errorf("session.js: %d %q", res.StatusCode, b)
	}
}

// Umbrel's start: the port given at start wins over the setting on every start, and a first start moves finished
// torrents into the folder it is told to, both folders made at once.
func TestFixedPortAndCompletedFolder(t *testing.T) {
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	dl, done := filepath.Join(dir, "Equinox", "incomplete"), filepath.Join(dir, "Equinox", "complete")
	opts := Options{Open: true, Downloads: dl, Completed: done, TorrentPort: port}

	a, err := StartWith(dir, "127.0.0.1:0", opts)
	if err != nil {
		t.Fatal(err)
	}
	if s := a.Settings.Get(); s.MoveCompletedDir != done || s.ListenPort != port {
		t.Errorf("first start: finished torrents to %q, port %d; want %q, %d", s.MoveCompletedDir, s.ListenPort, done, port)
	}
	for _, d := range []string{dl, done} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("%s was not made: %v", d, err)
		}
	}
	// the setting changed by hand (or by the button for a random port, as on Umbrel): the next start puts it back
	if err := a.Manager.SetListenPort(51757); err != nil {
		t.Fatal(err)
	}
	a.Close()
	time.Sleep(500 * time.Millisecond)

	a, err = StartWith(dir, "127.0.0.1:0", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); time.Sleep(500 * time.Millisecond) })
	if got := a.Settings.Get().ListenPort; got != port {
		t.Errorf("after a restart the port is %d, want the fixed %d", got, port)
	}
	if got := a.Manager.Port(); got != port {
		t.Errorf("the engine listens on %d, want the fixed %d", got, port)
	}
}
