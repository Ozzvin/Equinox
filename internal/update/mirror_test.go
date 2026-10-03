package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

// No test of this package may reach the real site: the mirror is off unless a test points it at a fake one.
func init() { MirrorBase = "" }

// fakeMirror serves what the site's /download/ folder holds: version.txt, SHA256SUMS.txt, the installer under
// its versioned name, and (if notes is not empty) notes.md.
func fakeMirror(t *testing.T, tag string, installer []byte, notes string) *httptest.Server {
	t.Helper()
	version := strings.TrimPrefix(tag, "v")
	sum := sha256.Sum256(installer)
	name := versionedSetupName(version)
	mux := http.NewServeMux()
	mux.HandleFunc("/download/version.txt", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tag)) })
	mux.HandleFunc("/download/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + name + "\n" + hex.EncodeToString(sum[:]) + "  Equinox-Setup.exe\n"))
	})
	mux.HandleFunc("/download/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(installer) })
	mux.HandleFunc("/download/notes.md", func(w http.ResponseWriter, r *http.Request) {
		if notes == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(notes))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// useMirror points the package at a fake GitHub API (api) and a fake mirror for the length of a test.
func useMirror(t *testing.T, running, api, mirror string) {
	t.Helper()
	oldV, oldAPI, oldMirror, oldWait := buildinfo.Version, APIURL, MirrorBase, githubWait
	buildinfo.Version, APIURL, MirrorBase = running, api, mirror
	t.Cleanup(func() { buildinfo.Version, APIURL, MirrorBase, githubWait = oldV, oldAPI, oldMirror, oldWait })
}

func downGitHub(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckFallsBackToTheMirror(t *testing.T) {
	mirror := fakeMirror(t, "v1.0.1", []byte("installer from the mirror"), "- Fixed the tray menu.")
	useMirror(t, "1.0.0", downGitHub(t).URL, mirror.URL+"/download/")

	info, err := Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.Version != "1.0.1" || info.setupName != "Equinox-Setup-1.0.1.exe" || !strings.HasPrefix(info.setupURL, mirror.URL) {
		t.Fatalf("the release must come from the mirror: %+v", info)
	}
	if info.Notes != "- Fixed the tray menu." {
		t.Fatalf("the notes of the mirror are lost: %q", info.Notes)
	}
	path, err := info.fetchVerified(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("an installer from the mirror must pass its checksum: %v", err)
	}
	if !strings.HasSuffix(path, "Equinox-Setup.exe") {
		t.Fatalf("unexpected path %s", path)
	}
}

// A blocked GitHub often hangs instead of refusing: the check gives up on it after githubWait, not the caller's time.
func TestCheckFallsBackWhenGitHubHangs(t *testing.T) {
	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); slow.Close() })
	mirror := fakeMirror(t, "v1.0.1", []byte("x"), "")
	useMirror(t, "1.0.0", slow.URL, mirror.URL+"/download/")
	githubWait = 200 * time.Millisecond

	start := time.Now()
	info, err := Check(context.Background(), true)
	if err != nil || info == nil || info.Version != "1.0.1" {
		t.Fatalf("Check = %+v, %v", info, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the check waited %v for a hanging GitHub", d)
	}
	if info.Notes != "" {
		t.Fatalf("no notes.md on the mirror, yet notes %q", info.Notes)
	}
}

func TestMirrorThatIsNotNewerMeansNoUpdate(t *testing.T) {
	mirror := fakeMirror(t, "v1.0.0", []byte("x"), "")
	useMirror(t, "1.0.0", downGitHub(t).URL, mirror.URL+"/download/")
	if info, err := Check(context.Background(), true); err != nil || info != nil {
		t.Fatalf("Check = %+v, %v, want no update", info, err)
	}
}

func TestMirrorWithAGarbledVersionIsAnError(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>oops</html>")) }))
	t.Cleanup(bad.Close)
	useMirror(t, "1.0.0", downGitHub(t).URL, bad.URL+"/")
	if info, err := Check(context.Background(), true); err == nil || info != nil {
		t.Fatalf("Check = %+v, %v, want an error", info, err)
	}
}

// GitHub answers, so the release and its checksum list come from it, but its file downloads fail: the installer is
// taken from the mirror and still checked against the release's own list.
func TestInstallerFromTheMirrorWhenGitHubDownloadsFail(t *testing.T) {
	body := []byte("the real installer")
	gh := fakeGitHub(t, body, "v1.0.1")
	mirror := fakeMirror(t, "v1.0.1", body, "")
	info := &Info{Version: "1.0.1", setupURL: gh.URL + "/missing", setupName: "Equinox-Setup.exe", sumsURL: gh.URL + "/sums",
		mirrorSetupURL: mirror.URL + "/download/Equinox-Setup-1.0.1.exe", mirrorSumsURL: mirror.URL + "/download/SHA256SUMS.txt"}
	if _, err := info.fetchVerified(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("the mirror's copy of the same installer must be accepted: %v", err)
	}
}

// The mirror is up to an hour behind GitHub: its installer of the previous version must not pass for the new one.
func TestInstallerFromAMirrorThatIsBehindIsRefused(t *testing.T) {
	gh := fakeGitHub(t, []byte("installer 1.0.2"), "v1.0.2")
	old := fakeMirror(t, "v1.0.1", []byte("installer 1.0.1"), "")
	info := &Info{Version: "1.0.2", setupURL: gh.URL + "/missing", setupName: "Equinox-Setup.exe", sumsURL: gh.URL + "/sums",
		mirrorSetupURL: old.URL + "/download/Equinox-Setup-1.0.1.exe"}
	if _, err := info.fetchVerified(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("an older installer from the mirror must fail the checksum, got %v", err)
	}
}

// When GitHub answers, the release found there also knows where the mirror keeps the same files.
func TestReleaseFromGitHubKnowsTheMirror(t *testing.T) {
	gh := fakeGitHub(t, []byte("x"), "v1.0.1")
	useMirror(t, "1.0.0", gh.URL+"/release", "https://mirror.invalid/download/")
	info, err := Check(context.Background(), true)
	if err != nil || info == nil {
		t.Fatalf("Check = %+v, %v", info, err)
	}
	if info.mirrorSetupURL != "https://mirror.invalid/download/Equinox-Setup.exe" || info.mirrorSumsURL != "https://mirror.invalid/download/SHA256SUMS.txt" {
		t.Fatalf("mirror addresses: %q %q", info.mirrorSetupURL, info.mirrorSumsURL)
	}
}
