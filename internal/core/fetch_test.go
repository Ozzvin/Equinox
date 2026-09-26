package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func codeOf(err error) string {
	var c Coder
	if errors.As(err, &c) {
		code, _ := c.ErrCode()
		return code
	}
	return ""
}

func TestFetchTorrent(t *testing.T) {
	defer AllowLocalFetches(false)
	mux := http.NewServeMux()
	mux.HandleFunc("/ok.torrent", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("d4:infod4:name1:xee")) })
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxFetchedTorrent+10)))
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// this computer is not allowed as the place of a link
	AllowLocalFetches(false)
	if _, err := FetchTorrent(context.Background(), srv.URL+"/ok.torrent"); codeOf(err) != "url.blocked" || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a link to this computer must be refused: %v", err)
	}

	AllowLocalFetches(true)
	raw, err := FetchTorrent(context.Background(), " "+srv.URL+"/ok.torrent ")
	if err != nil || string(raw) != "d4:infod4:name1:xee" {
		t.Fatalf("fetch: %q %v", raw, err)
	}
	for name, tc := range map[string]struct{ link, code string }{
		"not found":     {srv.URL + "/missing", "url.status"},
		"too big":       {srv.URL + "/big", "url.too_large"},
		"endless":       {srv.URL + "/loop", "url.redirects"},
		"redirect away": {srv.URL + "/to-file", "url.invalid"},
		"ftp":           {"ftp://example.org/a.torrent", "url.invalid"},
		"a file":        {"file:///etc/passwd", "url.invalid"},
		"no host":       {"http:///a.torrent", "url.invalid"},
		"nothing":       {"", "url.invalid"},
		"nobody home":   {"http://127.0.0.1:1/a.torrent", "url.fetch"},
	} {
		if _, err := FetchTorrent(context.Background(), tc.link); codeOf(err) != tc.code || !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: code %q (%v), want %q", name, codeOf(err), err, tc.code)
		}
	}
}

func TestRefuseAddress(t *testing.T) {
	defer AllowLocalFetches(false)
	AllowLocalFetches(false)
	for addr, refused := range map[string]bool{
		"127.0.0.1:80":             true,
		"[::1]:80":                 true,
		"0.0.0.0:80":               true,
		"169.254.169.254:80":       true, // where the cloud services keep their secrets
		"[fe80::1]:80":             true,
		"[::ffff:127.0.0.1]:80":    true, // the same address written the other way
		"224.0.0.1:80":             true,
		"192.168.1.5:80":           false, // the home network is a usual place for a tracker
		"10.0.0.7:8080":            false,
		"93.184.216.34:443":        false,
		"[2606:2800:220:1::1]:443": false,
	} {
		if err := refuseAddress("tcp", addr, nil); (err != nil) != refused {
			t.Errorf("%s: refused = %v, want %v", addr, err != nil, refused)
		}
	}
}
