package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Content-Length is the server's word, not a fact, so the reader itself has to be bounded:
// without that a hostile or broken release could fill the disk.
func TestDownloadRefusesAnOversizedBody(t *testing.T) {
	// No Content-Length at all (a chunked response): nothing up front says how big this is,
	// so only a bounded reader can stop it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		for i := 0; i < 50; i++ {
			w.Write(make([]byte, 1000))
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "Equinox-Setup.exe")
	err := download(context.Background(), srv.URL, path, 1000, nil)
	if err == nil {
		t.Fatal("an oversized download was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("the error should say what went wrong: %v", err)
	}
}

// The same with an honest, and honestly huge, Content-Length.
func TestDownloadRefusesAnOversizedDeclaredBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 50_000))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "Equinox-Setup.exe")
	if err := download(context.Background(), srv.URL, path, 1000, nil); err == nil {
		t.Fatal("an oversized download was accepted")
	}
}

func TestDownloadAcceptsABodyWithinTheLimit(t *testing.T) {
	body := make([]byte, 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "Equinox-Setup.exe")
	if err := download(context.Background(), srv.URL, path, int64(len(body)), nil); err != nil {
		t.Fatalf("a body exactly at the limit must go through: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || len(got) != len(body) {
		t.Fatalf("got %d bytes (err %v), want %d", len(got), err, len(body))
	}
}

func TestDownloadBytesRefusesAnOversizedChecksumFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxSumsBytes+1))
	}))
	defer srv.Close()

	if _, err := downloadBytes(context.Background(), srv.URL); err == nil {
		t.Fatal("an oversized checksum file was accepted")
	}
}
