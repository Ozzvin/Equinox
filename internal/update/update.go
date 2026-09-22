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

// Progress describes how an in-progress Install call is going, for a UI to poll.
type Progress struct {
	// Phase is one of "downloading", "verifying", "installing", "error" ("" before any
	// install has been attempted this run).
	Phase   string  `json:"phase"`
	Percent float64 `json:"percent"` // 0..1, meaningful only while Phase is "downloading"
	Err     string  `json:"error,omitempty"`
}

var (
	progMu sync.Mutex
	prog   Progress
)

// CurrentProgress reports the state of the most recent Install call.
func CurrentProgress() Progress {
	progMu.Lock()
	defer progMu.Unlock()
	return prog
}

func setProgress(p Progress) {
	progMu.Lock()
	prog = p
	progMu.Unlock()
}

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
	fail := func(err error) error {
		setProgress(Progress{Phase: "error", Err: err.Error()})
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}
	setupPath := filepath.Join(dir, "Equinox-Setup.exe")
	setProgress(Progress{Phase: "downloading"})
	if err := download(ctx, i.setupURL, setupPath, func(done, total int64) {
		var pct float64
		if total > 0 {
			pct = float64(done) / float64(total)
		}
		setProgress(Progress{Phase: "downloading", Percent: pct})
	}); err != nil {
		return fail(err)
	}
	setProgress(Progress{Phase: "verifying"})
	sums, err := downloadBytes(ctx, i.sumsURL)
	if err != nil {
		return fail(err)
	}
	want, err := findSum(sums, "Equinox-Setup.exe")
	if err != nil {
		return fail(err)
	}
	got, err := sha256File(setupPath)
	if err != nil {
		return fail(err)
	}
	if got != want {
		return fail(fmt.Errorf("update: checksum mismatch for the downloaded installer"))
	}
	setProgress(Progress{Phase: "installing"})
	args := []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"}
	if relaunch {
		args = append(args, "/autoupdate=1")
	}
	if err := exec.Command(setupPath, args...).Start(); err != nil {
		return fail(err)
	}
	return nil
}

// download saves url to path, calling report(bytesSoFar, totalBytes) as it goes (totalBytes
// is 0 if the server did not send a Content-Length). report may be nil.
func download(ctx context.Context, url, path string, report func(done, total int64)) error {
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
	var w io.Writer = f
	pw := &progressWriter{w: f, total: res.ContentLength, report: report}
	if report != nil {
		w = pw
	}
	if _, err := io.Copy(w, res.Body); err != nil {
		return err
	}
	if report != nil {
		report(pw.done, pw.total) // a final call so 100% is always seen, even if throttled
	}
	return nil
}

// progressWriter reports download progress at most a few times a second, so polling stays
// cheap without the UI missing meaningful updates.
type progressWriter struct {
	w        io.Writer
	done     int64
	total    int64
	report   func(done, total int64)
	lastSent time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if time.Since(p.lastSent) > 100*time.Millisecond {
		p.report(p.done, p.total)
		p.lastSent = time.Now()
	}
	return n, err
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
