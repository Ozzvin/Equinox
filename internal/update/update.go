// Package update checks GitHub for a newer Equinox release and can download and silently
// install one. It is used by the desktop application; the server variant only checks.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

const cacheTTL = time.Hour

// APIURL is a var so tests can point it at a fake server.
var APIURL = "https://api.github.com/repos/Ozzvin/equinox/releases/latest"

// Info describes a GitHub release that is newer than the version currently running.
type Info struct {
	Version string // e.g. "1.0.6", without the leading "v"
	Notes   string // the release's own description (Markdown)
	URL     string // the release's page, for a manual download

	setupURL string
	sumsURL  string
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Body    string    `json:"body"`
	HTMLURL string    `json:"html_url"`
	Assets  []ghAsset `json:"assets"`
}

var (
	mu       sync.Mutex
	cached   *Info
	cachedAt time.Time
	checked  bool
)

// Check reports the latest GitHub release if it is newer than the running version, or nil if
// not (or if the check fails). A result is cached for an hour unless force is set, so opening
// the settings repeatedly does not hit GitHub every time.
func Check(ctx context.Context, force bool) (*Info, error) {
	mu.Lock()
	if !force && checked && time.Since(cachedAt) < cacheTTL {
		info := cached
		mu.Unlock()
		return info, nil
	}
	mu.Unlock()

	info, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	cached, cachedAt, checked = info, time.Now(), true
	mu.Unlock()
	return info, nil
}

func fetch(ctx context.Context) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, APIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", buildinfo.Name+"/"+buildinfo.Version)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: github returned %s", res.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return nil, err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if compareVersions(latest, buildinfo.Version) <= 0 {
		return nil, nil // already on the latest version (or newer, e.g. a dev build)
	}
	info := &Info{Version: latest, Notes: rel.Body, URL: rel.HTMLURL}
	for _, a := range rel.Assets {
		switch a.Name {
		case "Equinox-Setup.exe":
			info.setupURL = a.URL
		case "SHA256SUMS.txt":
			info.sumsURL = a.URL
		}
	}
	if info.setupURL == "" || info.sumsURL == "" {
		return nil, fmt.Errorf("update: release %s has no installer to download", rel.TagName)
	}
	return info, nil
}

// compareVersions compares two dotted-numeric versions ("1.2.10" > "1.2.9"). Unparsable or
// missing parts count as 0, so "dev" always looks older than any real release.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(bs[i])
		}
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Install downloads the installer and its checksum into dir, verifies it against the
// checksum published in the same release, and runs it silently. The installer's own
// InitializeSetup closes this process once it starts (see installer/equinox.iss), so the
// caller does not need to exit separately. When relaunch is true, the installer reopens
// Equinox once the update is in place (see the IsAutoUpdate check in equinox.iss).
func (i *Info) Install(ctx context.Context, dir string, relaunch bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	setupPath := filepath.Join(dir, "Equinox-Setup.exe")
	if err := download(ctx, i.setupURL, setupPath); err != nil {
		return err
	}
	sums, err := downloadBytes(ctx, i.sumsURL)
	if err != nil {
		return err
	}
	want, err := findSum(sums, "Equinox-Setup.exe")
	if err != nil {
		return err
	}
	got, err := sha256File(setupPath)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("update: checksum mismatch for the downloaded installer")
	}
	args := []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"}
	if relaunch {
		args = append(args, "/autoupdate=1")
	}
	return exec.Command(setupPath, args...).Start()
}

func download(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", buildinfo.Name+"/"+buildinfo.Version)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("update: download %s: %s", filepath.Base(path), res.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, res.Body)
	return err
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.Name+"/"+buildinfo.Version)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: download checksums: %s", res.Status)
	}
	return io.ReadAll(res.Body)
}

// findSum reads a line like "<hash>  <name>" (the format build.ps1 writes) out of sums.
func findSum(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("update: no checksum listed for %s", name)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
