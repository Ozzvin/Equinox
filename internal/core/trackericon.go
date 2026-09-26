package core

import (
	"context"
	"errors"
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

// iconScheme is how a site is asked for its icon; only the tests change it.
var iconScheme = "https"

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

// TrackerIcon returns the icon of a site (its domain, "rutracker.org"): from the folder of the icons, or fetched now. ErrNotFound
// says there is none (or that icons are switched off).
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

	m.iconMu.Lock() // one at a time: a sidebar of many trackers must not be many requests at once
	defer m.iconMu.Unlock()
	if fresh(file, iconKeep) { // another request got it while this one waited
		if b, t, ok := read(); ok {
			return b, t, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, iconFetchWait)
	defer cancel()
	b := fetchSiteIcon(ctx, site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", ErrNotFound
	}
	if t := IconType(b); t != "" {
		_ = os.WriteFile(file, b, 0o644)
		_ = os.Remove(miss)
		return b, t, nil
	}
	_ = os.WriteFile(miss, nil, 0o644)
	if b, t, ok := read(); ok { // the site is not answering now, the old icon is better than none
		return b, t, nil
	}
	return nil, "", ErrNotFound
}

// fetchSiteIcon finds the icon of a site: /favicon.ico, else the one the front page names.
func fetchSiteIcon(ctx context.Context, site string) []byte {
	for _, host := range []string{site, "www." + site} {
		if b, _ := fetchImage(ctx, iconScheme+"://"+host+"/favicon.ico"); IconType(b) != "" {
			return b
		}
		page, base := fetchPage(ctx, iconScheme+"://"+host+"/")
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
