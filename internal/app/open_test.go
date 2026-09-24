package app

import (
	"io"
	"net/http"
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
	if a.Settings.Get().PortMapping {
		t.Error("a first start of an open server must not try UPnP")
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
