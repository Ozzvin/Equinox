package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDesktop struct {
	on, hidden  bool
	madeDefault int
	fail        error
}

func (f *fakeDesktop) AutostartEnabled() bool { return f.on }
func (f *fakeDesktop) SetAutostart(on, hidden bool) error {
	if f.fail != nil {
		return f.fail
	}
	f.on, f.hidden = on, hidden
	return nil
}
func (f *fakeDesktop) MakeDefault() error { f.madeDefault++; return f.fail }

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

// The Linux program's page is in a browser, with nothing bound to it: the system section of the settings asks over
// the API, and the autostart starts hidden in the tray as the settings say.
func TestBrowserAppSystemSection(t *testing.T) {
	d := &fakeDesktop{}
	e := setup(t, func(s *Server) { s.SetBrowserApp(d) })

	if body := readBody(t, e.do(t, "GET", "/session.js", nil, nil)); !strings.Contains(body, "__equinoxBrowserApp = true") {
		t.Errorf("session.js must tell the page it is the browser app, got %q", body)
	}
	if body := readBody(t, e.do(t, "GET", "/api/desktop/autostart", nil, nil)); !strings.Contains(body, `"on":false`) {
		t.Errorf("autostart off, got %s", body)
	}
	// StartHidden is on by default
	res := e.do(t, "PUT", "/api/desktop/autostart", strings.NewReader(`{"on":true}`), map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 200 || !d.on || !d.hidden {
		t.Errorf("turning autostart on: %d %s, on=%v hidden=%v", res.StatusCode, readBody(t, res), d.on, d.hidden)
	}
	if res := e.do(t, "POST", "/api/desktop/default-app", nil, nil); res.StatusCode != 200 || d.madeDefault != 1 {
		t.Errorf("default app: %d %s, made %d times", res.StatusCode, readBody(t, res), d.madeDefault)
	}
	d.fail = errors.New("xdg-mime: not found")
	if res := e.do(t, "POST", "/api/desktop/default-app", nil, nil); res.StatusCode != 500 || !strings.Contains(readBody(t, res), "xdg-mime") {
		t.Errorf("a failure must reach the page with its reason")
	}
}

func TestNoSystemSectionWithoutBrowserApp(t *testing.T) {
	e := setup(t)
	if body := readBody(t, e.do(t, "GET", "/session.js", nil, nil)); strings.Contains(body, "BrowserApp") {
		t.Errorf("an ordinary server is not the browser app, session.js says %q", body)
	}
	for _, c := range []struct{ method, path string }{{"GET", "/api/desktop/autostart"}, {"PUT", "/api/desktop/autostart"}, {"POST", "/api/desktop/default-app"}} {
		if res := e.do(t, c.method, c.path, strings.NewReader(`{"on":true}`), nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s without the desktop program: %d, want 404", c.method, c.path, res.StatusCode)
		}
	}
}
