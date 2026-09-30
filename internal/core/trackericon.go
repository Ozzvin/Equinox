package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The icon of a tracker is the favicon of its site, shown next to its name. The program fetches it once (a request to the
// site of the tracker, made by the program, not by the page) and keeps it in the state folder: a site that has none is
// remembered for a week, so that it is not asked again and again. The switch is Settings.TrackerIcons.

const (
	maxIconBytes  = 128 << 10 // a favicon is small; a bigger answer is something else
	maxIconPage   = 256 << 10 // how much of the front page is read to find the icon it names
	iconKeep      = 60 * 24 * time.Hour
	iconMissKeep  = 7 * 24 * time.Hour
	iconDirName   = "icons"
	iconFetchWait = 25 * time.Second
)

// ErrIconPending is ErrNotFound with the added meaning that a fetch of the icon was just started (or was already
// running) in the background: unlike a confirmed absence, a caller must not remember this answer for long, since
// the icon (or the confirmed absence) may be ready within moments.
var ErrIconPending = fmt.Errorf("%w: fetching in the background", ErrNotFound)

// iconScheme is how a site is asked for its icon; only the tests change it.
var iconScheme = "https"

// iconPort, appended to the host when set, points a fetch at a local test server; only the tests change it.
var iconPort string

var siteRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// the images that may be shown as an icon (not SVG: it can carry scripts)
var iconTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/x-icon": true, "image/webp": true, "image/bmp": true}

var linkIconRe = regexp.MustCompile(`(?is)<link\s[^>]*?rel\s*=\s*["']?(?:shortcut\s+)?icon["']?[^>]*>`)
var hrefRe = regexp.MustCompile(`(?is)href\s*=\s*["']?([^"'\s>]+)`)

// IconType is the type of image data if it is one that may be an icon, "" otherwise.
func IconType(data []byte) string {
	if len(data) == 0 || len(data) > maxIconBytes {
		return ""
	}
	if t := http.DetectContentType(data); iconTypes[t] {
		return t
	}
	return ""
}

// TrackerIcon returns the icon of a site (its domain, "rutracker.org"): from the folder of the icons, or fetched in
// the background. A confirmed absence (or icons switched off) is ErrNotFound; a fetch still in flight is the more
// specific ErrIconPending, so a caller can tell the two apart (see its doc).
func (m *Manager) TrackerIcon(ctx context.Context, site string) (data []byte, contentType string, err error) {
	site = strings.ToLower(strings.TrimSpace(site))
	if !siteRe.MatchString(site) || len(site) > 100 {
		return nil, "", ErrInvalidInput
	}
	if !m.cfg.Get().TrackerIcons {
		return nil, "", ErrNotFound
	}
	dir := filepath.Join(m.stateDir, iconDirName)
	file, miss := filepath.Join(dir, site+".img"), filepath.Join(dir, site+".none")
	fresh := func(path string, keep time.Duration) bool {
		st, err := os.Stat(path)
		return err == nil && time.Since(st.ModTime()) < keep
	}
	read := func() ([]byte, string, bool) {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, "", false
		}
		t := IconType(b)
		return b, t, t != ""
	}
	if fresh(file, iconKeep) {
		if b, t, ok := read(); ok {
			return b, t, nil
		}
	}
	if fresh(miss, iconMissKeep) {
		return nil, "", ErrNotFound
	}

	// Not cached: ask for it in the background and say now that there is none yet, rather than hold the request
	// (an <img> connection, one of Chromium's 6 per host) open for up to iconFetchWait. A stalled or unreachable
	// tracker site must not be able to queue up behind the ones this page keeps polling, like /api/torrents.
	m.iconMu.Lock()
	if m.iconFetching == nil {
		m.iconFetching = map[string]bool{}
	}
	already := m.iconFetching[site]
	if !already {
		m.iconFetching[site] = true
	}
	m.iconMu.Unlock()
	if !already {
		go m.fetchIconInBackground(site, dir, file, miss)
	}
	return nil, "", ErrIconPending
}

// fetchIconInBackground fetches the icon of a site and writes it (or the negative marker) to the state folder, for
// a later TrackerIcon call to pick up. It runs detached from any one request: ctx does not come from a caller, since
// that would be cancelled the moment the caller's own HTTP response is sent.
func (m *Manager) fetchIconInBackground(site, dir, file, miss string) {
	defer func() {
		m.iconMu.Lock()
		delete(m.iconFetching, site)
		m.iconMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), iconFetchWait)
	defer cancel()
	b := fetchSiteIcon(ctx, site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if t := IconType(b); t != "" {
		_ = os.WriteFile(file, b, 0o644)
		_ = os.Remove(miss)
		return
	}
	_ = os.WriteFile(miss, nil, 0o644)
}

// fetchSiteIcon finds the icon of a site: /favicon.ico, else the one the front page names.
func fetchSiteIcon(ctx context.Context, site string) []byte {
	for _, host := range []string{site, "www." + site} {
		addr := host
		if iconPort != "" {
			addr += ":" + iconPort
		}
		if b, _ := fetchImage(ctx, iconScheme+"://"+addr+"/favicon.ico"); IconType(b) != "" {
			return b
		}
		page, base := fetchPage(ctx, iconScheme+"://"+addr+"/")
		if page == "" {
			continue
		}
		for _, tag := range linkIconRe.FindAllString(page, 4) {
			m := hrefRe.FindStringSubmatch(tag)
			if m == nil {
				continue
			}
			ref, err := url.Parse(strings.TrimSpace(m[1]))
			if err != nil {
				continue
			}
			if b, _ := fetchImage(ctx, base.ResolveReference(ref).String()); IconType(b) != "" {
				return b
			}
		}
	}
	return nil
}

// fetchImage downloads a small file (nothing for any failure); the same rules as for a link to a .torrent: only http and
// https, not this computer, not a service address.
func fetchImage(ctx context.Context, link string) ([]byte, *url.URL) {
	body, u, _ := fetchLimited(ctx, link, maxIconBytes, "image/*,*/*;q=0.5")
	return body, u
}

func fetchPage(ctx context.Context, link string) (string, *url.URL) {
	body, u, _ := fetchLimited(ctx, link, maxIconPage, "text/html,*/*;q=0.5")
	return string(body), u
}

// fetchLimited downloads at most max bytes (more is an error), with the client that refuses this computer. It gives the body
// and the address it came from after the redirects.
func fetchLimited(ctx context.Context, link string, max int, accept string) ([]byte, *url.URL, error) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, nil, ErrInvalidInput
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Equinox")
	req.Header.Set("Accept", accept)
	resp, err := fetchClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, errors.New("status " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > max {
		return nil, nil, errors.New("too large")
	}
	return body, resp.Request.URL, nil
}
