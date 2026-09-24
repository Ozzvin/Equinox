package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostAllowed(t *testing.T) {
	closed := &Server{}
	open := &Server{}
	open.SetOpen([]string{"Box.Example.org"})

	for host, want := range map[string][2]bool{ // {closed, open}
		"127.0.0.1:9091":         {true, true},
		"localhost":              {true, true},
		"[::1]:80":               {true, true},
		"192.168.1.5:8080":       {false, true}, // an IP address cannot be rebound
		"[fd00::5]:8080":         {false, true},
		"umbrel:8080":            {false, true}, // one label: a machine or a container in the network
		"umbrel.local":           {false, true},
		"UMBREL.LOCAL:80":        {false, true},
		"nas.lan":                {false, true},
		"abc.onion":              {false, true},
		"umbrel.tail1234.ts.net": {false, true},
		"box.example.org":        {false, true}, // given with SetOpen
		"evil.example.com":       {false, false},
		"127.0.0.1.evil.com":     {false, false},
		"localhost.evil.com":     {false, false},
		"":                       {false, false},
	} {
		if got := closed.hostAllowed(host); got != want[0] {
			t.Errorf("closed server, host %q: %v, want %v", host, got, want[0])
		}
		if got := open.hostAllowed(host); got != want[1] {
			t.Errorf("open server, host %q: %v, want %v", host, got, want[1])
		}
	}
}

// In open mode a page is handed the open key, an outside page still cannot call the API without sending the
// Authorization header, and a server that is not open gives out nothing.
func TestOpenMode(t *testing.T) {
	get := func(s *Server, path, host string, hdr map[string]string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		if host != "" {
			req.Host = host
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		b, _ := io.ReadAll(w.Result().Body)
		return w.Result().StatusCode, string(b)
	}

	closed := New(nil, nil, tok, nil)
	if code, body := get(closed, "/session.js", "127.0.0.1:9091", nil); code != 200 || strings.Contains(body, "open") || strings.Contains(body, tok) {
		t.Errorf("a closed server must give an empty session.js, got %d %q", code, body)
	}
	if code, _ := get(closed, "/session.js", "umbrel.local", nil); code != http.StatusForbidden {
		t.Errorf("a closed server must refuse a foreign host, got %d", code)
	}

	open := New(nil, nil, tok, nil)
	open.SetOpen(nil)
	if code, body := get(open, "/session.js", "umbrel.local:8080", nil); code != 200 || !strings.Contains(body, `"`+OpenToken+`"`) || strings.Contains(body, tok) {
		t.Errorf("an open server hands out the open key only, got %d %q", code, body)
	}
	if code, _ := get(open, "/api/update", "umbrel.local", nil); code != http.StatusUnauthorized {
		t.Errorf("without the Authorization header even an open server refuses, got %d", code)
	}
	if code, _ := get(open, "/api/update", "umbrel.local", map[string]string{"Authorization": "Bearer " + tok}); code != http.StatusUnauthorized {
		t.Errorf("the personal key means nothing to an open server, got %d", code)
	}
	if code, body := get(open, "/api/update", "umbrel.local", map[string]string{"Authorization": "Bearer " + OpenToken}); code != 200 || strings.Contains(body, `"available":true`) {
		t.Errorf("an open server offers no update, got %d %q", code, body)
	}
	if code, _ := get(open, "/session.js", "evil.example.com", nil); code != http.StatusForbidden {
		t.Errorf("an open server still refuses a rebindable name, got %d", code)
	}
}
