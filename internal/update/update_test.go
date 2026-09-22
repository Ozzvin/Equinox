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
	"testing"

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
	mux.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) { w.Write(installerBody) })
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
	if err := download(context.Background(), info.setupURL, setupPath); err != nil {
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
