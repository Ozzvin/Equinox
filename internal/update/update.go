// Package update checks GitHub for a newer Equinox release and can download and silently
// install one. It is used by the desktop application; the server variant only checks.
//
// GitHub is not always reachable (from some countries, at some times), so the site keeps a mirror of the latest
// release (MirrorBase): when the GitHub API does not answer, the check asks the mirror instead, and when GitHub
// answers but its file downloads do not, the files are taken from the mirror. Either way the installer is checked
// against the release's SHA256SUMS.txt before it runs, exactly as before.
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

// Caps on what a release may hand us. Nothing here is trusted to be the size it claims: a
// Content-Length is the server's word, so the readers below are bounded instead.
const (
	maxSetupBytes   = 256 << 20 // the installer; it is ~10 MB today
	maxSumsBytes    = 1 << 20   // SHA256SUMS.txt is a handful of lines
	maxReleaseBytes = 8 << 20   // the release JSON from the GitHub API
	maxVersionBytes = 64        // the mirror's version.txt: "v1.0.35"
	maxNotesBytes   = 256 << 10 // the mirror's notes.md, if it has one
)

// githubWait is how long the check waits for the GitHub API before it asks the mirror. A blocked GitHub often
// does not refuse at once but lets the connection hang, and the whole check has to fit in the caller's time.
var githubWait = 10 * time.Second

// APIURL is a var so tests can point it at a fake server.
var APIURL = "https://api.github.com/repos/Ozzvin/equinox/releases/latest"

// MirrorBase is the folder on the site that mirrors the latest release (refreshed from GitHub every hour by the
// site itself): version.txt with the tag, SHA256SUMS.txt from the release, the installer under both its names,
// and, if present, notes.md with the release's description. Empty turns the mirror off (tests).
var MirrorBase = "https://equinoxtorrent.com/download/"

// Info describes a GitHub release that is newer than the version currently running.
type Info struct {
	Version string // e.g. "1.0.6", without the leading "v"
	Notes   string // the release's own description (Markdown)
	URL     string // the release's page, for a manual download

	setupURL  string
	setupName string // the release asset the installer was taken from, the name its checksum is listed under
	sumsURL   string
	// the same files on the mirror, tried when a download from setupURL or sumsURL fails ("" when the release
	// already comes from the mirror, or the mirror is off)
	mirrorSetupURL string
	mirrorSumsURL  string
}

// legacySetupName is the installer's name in the releases before file names carried the version. Every
// release still publishes the same installer under it, because the copies of the program that are already
// installed look for exactly this name and would otherwise never find an update.
const legacySetupName = "Equinox-Setup.exe"

// versionedSetupName is the installer's name in a release: "Equinox-Setup-1.0.17.exe".
func versionedSetupName(version string) string { return "Equinox-Setup-" + version + ".exe" }

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
func Check(ctx context.Context, force bool) (*Info, error) { return CheckWithin(ctx, force, cacheTTL) }

// CheckWithin is Check with the age up to which a cached result is good enough.
func CheckWithin(ctx context.Context, force bool, maxAge time.Duration) (*Info, error) {
	if !buildinfo.IsRelease() {
		return nil, nil // a beta or dev build has no release to update to, and must not overwrite the real install
	}
	mu.Lock()
	if !force && checked && time.Since(cachedAt) < maxAge {
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

// fetch asks GitHub first and the mirror when GitHub does not answer in time or answers with an error. An answer
// from GitHub that there is nothing newer is final: the mirror is never newer than GitHub.
func fetch(ctx context.Context) (*Info, error) {
	gctx, cancel := context.WithTimeout(ctx, githubWait)
	info, err := fetchGitHub(gctx)
	cancel()
	if err == nil || MirrorBase == "" {
		if info != nil && MirrorBase != "" {
			info.mirrorSetupURL, info.mirrorSumsURL = MirrorBase+info.setupName, MirrorBase+"SHA256SUMS.txt"
		}
		return info, err
	}
	minfo, merr := fetchMirror(ctx)
	if merr != nil {
		return nil, fmt.Errorf("%w (the mirror did not help either: %v)", err, merr)
	}
	return minfo, nil
}

// fetchMirror reads the latest release from the site's mirror: its version from version.txt, the installer's
// name from the release's SHA256SUMS.txt (the versioned name if it is listed, else the old fixed one), and the
// description from notes.md when the mirror has one.
func fetchMirror(ctx context.Context) (*Info, error) {
	tag, err := getSmall(ctx, MirrorBase+"version.txt", maxVersionBytes)
	if err != nil {
		return nil, err
	}
	latest := strings.TrimPrefix(strings.TrimSpace(string(tag)), "v")
	if latest == "" || strings.Trim(latest, "0123456789.") != "" {
		return nil, fmt.Errorf("update: the mirror's version.txt is not a version: %q", tag)
	}
	if compareVersions(latest, buildinfo.Version) <= 0 {
		return nil, nil
	}
	sums, err := downloadBytes(ctx, MirrorBase+"SHA256SUMS.txt")
	if err != nil {
		return nil, err
	}
	name := versionedSetupName(latest)
	if _, err := findSum(sums, name); err != nil {
		name = legacySetupName
		if _, err := findSum(sums, name); err != nil {
			return nil, fmt.Errorf("update: the mirror has no installer for %s", latest)
		}
	}
	info := &Info{Version: latest, URL: MirrorBase + name, setupURL: MirrorBase + name, setupName: name, sumsURL: MirrorBase + "SHA256SUMS.txt"}
	if notes, err := getSmall(ctx, MirrorBase+"notes.md", maxNotesBytes); err == nil {
		info.Notes = string(notes) // optional: without it the page says the description is not given
	}
	return info, nil
}

// getSmall reads a small file, refusing one over limit.
func getSmall(ctx context.Context, url string, limit int64) ([]byte, error) {
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
		return nil, fmt.Errorf("update: %s: %s", filepath.Base(url), res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("update: %s is larger than the %d byte limit", filepath.Base(url), limit)
	}
	return b, nil
}

func fetchGitHub(ctx context.Context) (*Info, error) {
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
	if err := json.NewDecoder(io.LimitReader(res.Body, maxReleaseBytes)).Decode(&rel); err != nil {
		return nil, err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if compareVersions(latest, buildinfo.Version) <= 0 {
		return nil, nil // already on the latest version (or newer, e.g. a dev build)
	}
	info := &Info{Version: latest, Notes: rel.Body, URL: rel.HTMLURL}
	for _, a := range rel.Assets {
		switch a.Name {
		case versionedSetupName(latest):
			info.setupURL, info.setupName = a.URL, a.Name // preferred: named after the release it belongs to
		case legacySetupName:
			if info.setupName == "" {
				info.setupURL, info.setupName = a.URL, a.Name
			}
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
	setupPath, err := i.fetchVerified(ctx, dir)
	if err != nil {
		return fail(err)
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

// fetchVerified downloads the installer into dir and checks it against the release's SHA256SUMS.txt. A file that
// cannot be had from where the release was found (GitHub's downloads may be blocked while its API is not) is taken
// from the mirror. The checksum always comes from the release's own list, so an installer from a mirror that is
// behind (a different version) fails the check instead of being run.
func (i *Info) fetchVerified(ctx context.Context, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	setupPath := filepath.Join(dir, "Equinox-Setup.exe")
	setProgress(Progress{Phase: "downloading"})
	report := func(done, total int64) {
		var pct float64
		if total > 0 {
			pct = float64(done) / float64(total)
		}
		setProgress(Progress{Phase: "downloading", Percent: pct})
	}
	if err := download(ctx, i.setupURL, setupPath, maxSetupBytes, report); err != nil {
		if i.mirrorSetupURL == "" {
			return "", err
		}
		if merr := download(ctx, i.mirrorSetupURL, setupPath, maxSetupBytes, report); merr != nil {
			return "", fmt.Errorf("%w (the mirror did not help either: %v)", err, merr)
		}
	}
	setProgress(Progress{Phase: "verifying"})
	sums, err := downloadBytes(ctx, i.sumsURL)
	if err != nil && i.mirrorSumsURL != "" {
		sums, err = downloadBytes(ctx, i.mirrorSumsURL)
	}
	if err != nil {
		return "", err
	}
	name := i.setupName
	if name == "" {
		name = legacySetupName
	}
	want, err := findSum(sums, name)
	if err != nil {
		return "", err
	}
	got, err := sha256File(setupPath)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("update: checksum mismatch for the downloaded installer")
	}
	return setupPath, nil
}

// download saves url to path, calling report(bytesSoFar, totalBytes) as it goes (totalBytes
// is 0 if the server did not send a Content-Length). report may be nil.
func download(ctx context.Context, url, path string, limit int64, report func(done, total int64)) error {
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
	// One byte over the limit is read on purpose, so an oversized body is caught rather than
	// silently truncated into a file that then fails its checksum for the wrong reason.
	n, err := io.Copy(w, io.LimitReader(res.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("update: %s is larger than the %d byte limit", filepath.Base(path), limit)
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
	b, err := io.ReadAll(io.LimitReader(res.Body, maxSumsBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxSumsBytes {
		return nil, fmt.Errorf("update: the checksum file is larger than the %d byte limit", maxSumsBytes)
	}
	return b, nil
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
