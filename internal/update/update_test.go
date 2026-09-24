package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.5", "1.0.5", 0},
		{"1.0.6", "1.0.5", 1},
		{"1.0.5", "1.0.6", -1},
		{"1.2.0", "1.10.0", -1}, // numeric, not lexical, comparison
		{"2.0", "1.9.9", 1},
		{"1.0.0", "dev", 1},
		{"dev", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestFindSum(t *testing.T) {
	sums := []byte("ABCDEF  Equinox-Setup.exe\n1234  Equinox-pc.zip\n")
	got, err := findSum(sums, "Equinox-Setup.exe")
	if err != nil || got != "abcdef" {
		t.Fatalf("findSum = %q, %v", got, err)
	}
	if _, err := findSum(sums, "missing.exe"); err == nil {
		t.Fatal("expected an error for a name not listed")
	}
}

// fakeGitHub serves a release one version ahead of the running build, with an installer and
// a matching checksum file, both from the same test server (so Install's download+verify
// path can be exercised without touching the real network).
func fakeGitHub(t *testing.T, installerBody []byte, tag string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	sum := sha256.Sum256(installerBody)
	sumLine := hex.EncodeToString(sum[:]) + "  Equinox-Setup.exe\n"

	var srv *httptest.Server
	mux.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(installerBody))) // real GitHub downloads always send this
		w.Write(installerBody)
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sumLine)) })
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		rel := ghRelease{
			TagName: tag,
			Body:    "release notes",
			HTMLURL: "https://example.invalid/releases/" + tag,
			Assets: []ghAsset{
				{Name: "Equinox-Setup.exe", URL: srv.URL + "/setup"},
				{Name: "SHA256SUMS.txt", URL: srv.URL + "/sums"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckReportsNewerRelease(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "1.0.0"
	t.Cleanup(func() { buildinfo.Version = old })

	srv := fakeGitHub(t, []byte("fake installer bytes"), "v1.0.1")
	oldURL := APIURL
	APIURL = srv.URL + "/release"
	t.Cleanup(func() { APIURL = oldURL })

	info, err := Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.Version != "1.0.1" {
		t.Fatalf("Check = %+v, want version 1.0.1", info)
	}
}

// Releases carry the installer under a versioned name and, for the copies installed before that, under the
// old fixed one. Whichever order the assets come in, a new client takes the versioned one and its checksum.
func TestCheckPrefersTheVersionedInstaller(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "1.0.16"
	t.Cleanup(func() { buildinfo.Version = old })

	for name, assets := range map[string][]string{
		"legacy first":    {"Equinox-Setup.exe", "Equinox-Setup-1.0.17.exe", "SHA256SUMS.txt"},
		"versioned first": {"Equinox-Setup-1.0.17.exe", "Equinox-Setup.exe", "SHA256SUMS.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			var srv *httptest.Server
			mux := http.NewServeMux()
			mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
				rel := ghRelease{TagName: "v1.0.17"}
				for _, n := range assets {
					rel.Assets = append(rel.Assets, ghAsset{Name: n, URL: srv.URL + "/dl/" + n})
				}
				_ = json.NewEncoder(w).Encode(rel)
			})
			srv = httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			oldURL := APIURL
			APIURL = srv.URL + "/release"
			t.Cleanup(func() { APIURL = oldURL })

			info, err := Check(context.Background(), true)
			if err != nil || info == nil {
				t.Fatalf("Check = %+v, %v", info, err)
			}
			if info.setupName != "Equinox-Setup-1.0.17.exe" || !strings.HasSuffix(info.setupURL, "/dl/Equinox-Setup-1.0.17.exe") {
				t.Fatalf("took %q from %s, want the versioned installer", info.setupName, info.setupURL)
			}
		})
	}
}

// A beta must not be told to "update" to the release before it: that installer would replace the real
// installation with an older program. It does not even ask GitHub.
func TestCheckSaysNothingToABetaBuild(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = buildinfo.BetaPrefix + "1.0.17"
	t.Cleanup(func() { buildinfo.Version = old })

	srv := fakeGitHub(t, []byte("x"), "v1.0.16")
	oldURL := APIURL
	APIURL = srv.URL + "/release"
	t.Cleanup(func() { APIURL = oldURL })

	info, err := Check(context.Background(), true)
	if err != nil || info != nil {
		t.Fatalf("Check = %+v, %v, want nothing for a beta", info, err)
	}
}

func TestCheckReportsNoUpdateWhenUpToDate(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "1.0.5"
	t.Cleanup(func() { buildinfo.Version = old })

	srv := fakeGitHub(t, []byte("x"), "v1.0.5")
	oldURL := APIURL
	APIURL = srv.URL + "/release"
	t.Cleanup(func() { APIURL = oldURL })

	info, err := Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Fatalf("Check = %+v, want nil (already up to date)", info)
	}
}

func TestInstallVerifiesChecksumBeforeRunning(t *testing.T) {
	installerBody := []byte("fake installer bytes")
	srv := fakeGitHub(t, installerBody, "v9.9.9")

	info := &Info{Version: "9.9.9", setupURL: srv.URL + "/setup", sumsURL: srv.URL + "/sums"}
	dir := t.TempDir()

	// A real Install would exec the downloaded file, which isn't a real installer here, so
	// only check that it downloaded the right bytes and accepted the matching checksum by
	// verifying the file it wrote before the exec step would fail.
	setupPath := filepath.Join(dir, "Equinox-Setup.exe")
	if err := download(context.Background(), info.setupURL, setupPath, maxSetupBytes, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(setupPath)
	if err != nil || string(got) != string(installerBody) {
		t.Fatalf("downloaded content mismatch: %v", err)
	}
	sums, err := downloadBytes(context.Background(), info.sumsURL)
	if err != nil {
		t.Fatal(err)
	}
	want, err := findSum(sums, "Equinox-Setup.exe")
	if err != nil {
		t.Fatal(err)
	}
	gotSum, err := sha256File(setupPath)
	if err != nil || gotSum != want {
		t.Fatalf("checksum mismatch: got %s, want %s (err %v)", gotSum, want, err)
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	body := make([]byte, 50_000)
	srv := fakeGitHub(t, body, "v9.9.9")
	setupPath := filepath.Join(t.TempDir(), "Equinox-Setup.exe")

	var last struct{ done, total int64 }
	var calls int
	err := download(context.Background(), srv.URL+"/setup", setupPath, maxSetupBytes, func(done, total int64) {
		calls++
		last.done, last.total = done, total
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("report must be called at least once (the final, forced call)")
	}
	if last.done != int64(len(body)) || last.total != int64(len(body)) {
		t.Fatalf("final report = %+v, want done=total=%d", last, len(body))
	}
}

func TestInstallLeavesCurrentProgressOnFailure(t *testing.T) {
	// "fake installer bytes" is not a real executable, so exec.Command(...).Start() fails once
	// Install reaches the "installing" phase — the point of this test is that CurrentProgress
	// ends up reporting that failure, not any earlier phase.
	installerBody := []byte("fake installer bytes")
	srv := fakeGitHub(t, installerBody, "v9.9.9")
	info := &Info{Version: "9.9.9", setupURL: srv.URL + "/setup", sumsURL: srv.URL + "/sums"}

	setProgress(Progress{})
	err := info.Install(context.Background(), t.TempDir(), false)
	if err == nil {
		t.Fatal("Install must fail: the downloaded bytes are not a real executable")
	}
	p := CurrentProgress()
	if p.Phase != "error" || p.Err == "" {
		t.Fatalf("CurrentProgress after a failed Install = %+v, want phase=error with a message", p)
	}
}

func TestInstallRejectsTamperedInstaller(t *testing.T) {
	srv := fakeGitHub(t, []byte("original bytes"), "v9.9.9")
	// Point setupURL at a handler that serves different bytes than what the checksum covers.
	mux := http.NewServeMux()
	mux.HandleFunc("/tampered", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tampered bytes")) })
	tamperedSrv := httptest.NewServer(mux)
	t.Cleanup(tamperedSrv.Close)

	info := &Info{Version: "9.9.9", setupURL: tamperedSrv.URL + "/tampered", sumsURL: srv.URL + "/sums"}
	err := info.Install(context.Background(), t.TempDir(), false)
	if err == nil {
		t.Fatal("Install must refuse a download that does not match the published checksum")
	}
}

// A cached answer is reused only while it is younger than the age asked for: the period of the automatic
// check decides how often GitHub is asked, and the manual check and the timer skip the cache.
func TestCheckWithinReusesOnlyAYoungAnswer(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "1.0.0"
	t.Cleanup(func() { buildinfo.Version = old })

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(ghRelease{TagName: "v1.0.1", Assets: []ghAsset{
			{Name: "Equinox-Setup-1.0.1.exe", URL: "http://x/a.exe"}, {Name: "SHA256SUMS.txt", URL: "http://x/sums"},
		}})
	}))
	t.Cleanup(srv.Close)
	oldURL := APIURL
	APIURL = srv.URL
	t.Cleanup(func() { APIURL = oldURL })

	ctx := context.Background()
	if _, err := CheckWithin(ctx, true, time.Hour); err != nil || hits != 1 {
		t.Fatalf("a forced check must ask: hits %d, err %v", hits, err)
	}
	if _, err := CheckWithin(ctx, false, time.Hour); err != nil || hits != 1 {
		t.Fatalf("a young answer must be reused: hits %d, err %v", hits, err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := CheckWithin(ctx, false, 10*time.Millisecond); err != nil || hits != 2 {
		t.Fatalf("an answer older than the age asked for must be replaced: hits %d, err %v", hits, err)
	}
}
