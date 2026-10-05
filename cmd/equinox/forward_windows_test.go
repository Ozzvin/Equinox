package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A .torrent opened from Explorer while the program runs is staged by the running instance, whose reply lists every
// file of the torrent. For a torrent with hundreds of files that reply is long; forward used to read only its first
// 4 KB, could not parse it, queued nothing, and the window for adding came up empty.
func TestForwardStagesATorrentWithManyFiles(t *testing.T) {
	files := make([]map[string]any, 600)
	for i := range files {
		files[i] = map[string]any{"path": fmt.Sprintf("BDMV/STREAM/%05d.m2ts", i), "size": 123456789}
	}
	reply, _ := json.Marshal(map[string]any{"id": "stage-42", "name": "Oppenheimer", "files": files})
	if len(reply) < 8<<10 {
		t.Fatalf("the reply must be long for this test, it is %d bytes", len(reply))
	}

	var mu sync.Mutex
	var queued []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/stage":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write(reply)
		case "/api/pending-add":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			queued = append(queued, string(b))
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ui-url"), []byte(srv.URL+"/#token=key"), 0o600); err != nil {
		t.Fatal(err)
	}
	torrentFile := filepath.Join(dir, "Oppenheimer.torrent")
	if err := os.WriteFile(torrentFile, []byte("d4:infod4:name1:xee"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := forward(dir, []string{torrentFile}); err != nil {
		t.Fatalf("forward: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queued) != 1 || !strings.Contains(queued[0], `"stage-42"`) {
		t.Fatalf("the staged torrent must be queued for the window for adding, queued: %q", queued)
	}
}
