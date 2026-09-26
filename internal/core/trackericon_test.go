package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

// the first bytes of a PNG file: enough for the type to be told, and small
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func TestSplitTracker(t *testing.T) {
	for announce, want := range map[string][2]string{
		"http://bt.t-ru.org/ann?magnet":              {"rutracker", "rutracker.org"}, // the announce is on another site than the tracker
		"http://tapochek.net/announce.php?uk=SECRET": {"tapochek", "tapochek.net"},
		"udp://tracker.opentrackr.org:1337/announce": {"opentrackr", "opentrackr.org"},
		"udp://tracker.torrent.eu.org:451/announce":  {"torrent", "torrent.eu.org"},
		"udp://93.184.216.34:6969/announce":          {"93.184.216.34", ""}, // an address has no site
		"http://localhost:8080/announce":             {"localhost", ""},
	} {
		if name, site := splitTracker(announce); name != want[0] || site != want[1] {
			t.Errorf("splitTracker(%q) = %q, %q; want %q, %q", announce, name, site, want[0], want[1])
		}
	}
}

func TestIconType(t *testing.T) {
	if IconType(pngBytes) != "image/png" {
		t.Error("a PNG is an icon")
	}
	if IconType([]byte("\x00\x00\x01\x00\x01\x00\x10\x10\x00\x00\x01\x00\x20\x00\x68\x04\x00\x00\x16\x00\x00\x00")) != "image/x-icon" {
		t.Error("an ICO is an icon")
	}
	for name, b := range map[string][]byte{
		"a page":  []byte("<!doctype html><html><body>log in</body></html>"),
		"an SVG":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"nothing": nil,
		"too big": append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxIconBytes)...),
	} {
		if IconType(b) != "" {
			t.Errorf("%s must not be an icon", name)
		}
	}
}

func TestFetchSiteIcon(t *testing.T) {
	defer AllowLocalFetches(false)
	AllowLocalFetches(true)
	old := iconScheme
	iconScheme = "http"
	defer func() { iconScheme = old }()

	ctx := context.Background()
	// one server that answers as the case says
	root := http.NewServeMux()
	var current http.Handler
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { current.ServeHTTP(w, r) })
	srv2 := httptest.NewServer(root)
	defer srv2.Close()
	host2 := strings.TrimPrefix(srv2.URL, "http://")

	current = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // /favicon.ico
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write(pngBytes)
			return
		}
		http.NotFound(w, r)
	})
	if b := fetchSiteIcon(ctx, host2); IconType(b) != "image/png" {
		t.Errorf("/favicon.ico: %d bytes, type %q", len(b), IconType(b))
	}

	current = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // named by the front page, relative address
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><link rel="stylesheet" href="/x.css"><LINK REL="shortcut icon" HREF="static/mark.png"></head></html>`))
		case "/static/mark.png":
			_, _ = w.Write(pngBytes)
		default:
			http.NotFound(w, r)
		}
	})
	if b := fetchSiteIcon(ctx, host2); IconType(b) != "image/png" {
		t.Errorf("the icon the page names: %d bytes", len(b))
	}

	current = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // an answer that is a page, not an image
		_, _ = w.Write([]byte("<html><body>Not found, sign in</body></html>"))
	})
	if b := fetchSiteIcon(ctx, host2); b != nil {
		t.Errorf("a page must not pass for an icon: %d bytes", len(b))
	}
	current = http.NotFoundHandler()
	if b := fetchSiteIcon(ctx, host2); b != nil {
		t.Errorf("no icon: %d bytes", len(b))
	}
}

func TestTrackerIconCacheAndSwitch(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	defer m.Close()
	icons := filepath.Join(dir, iconDirName)
	if err := os.MkdirAll(icons, 0o755); err != nil {
		t.Fatal(err)
	}
	// kept icon: served without any request
	if err := os.WriteFile(filepath.Join(icons, "kept.example.img"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if b, ct, err := m.TrackerIcon(context.Background(), "Kept.Example"); err != nil || ct != "image/png" || len(b) != len(pngBytes) {
		t.Fatalf("the kept icon: %d bytes, %q, %v", len(b), ct, err)
	}
	// a site that was found to have none, lately: not asked again
	if err := os.WriteFile(filepath.Join(icons, "none.example.none"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.TrackerIcon(context.Background(), "none.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a site with no icon: %v", err)
	}
	// what is not a site
	for _, bad := range []string{"", "localhost", "a b.example", "../x.example", "x.example/../y", "127.0.0.1:80", strings.Repeat("a", 120) + ".org"} {
		if _, _, err := m.TrackerIcon(context.Background(), bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%q must be refused: %v", bad, err)
		}
	}
	// switched off: not even a kept icon is given
	if err := m.cfg.Update(func(s *config.Settings) { s.TrackerIcons = false }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.TrackerIcon(context.Background(), "kept.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("with the icons off: %v", err)
	}
}
