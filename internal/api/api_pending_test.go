package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ozzvin/equinox/internal/buildinfo"
	"github.com/Ozzvin/equinox/internal/update"
)

// A .torrent file opened from outside the app (Explorer, a second launch) is staged and queued,
// not added outright; the web page picks it up through GET /api/pending-add and re-reads its
// details through GET /api/stage/{id}.
func TestPendingAddQueuesAStagedFileInsteadOfAdding(t *testing.T) {
	e := setup(t)
	code, st := stageFile(t, e, seededTorrent(t, e, bytes.Repeat([]byte{6}, 64<<10)))
	if code != 201 {
		t.Fatalf("stage: %d %v", code, st)
	}
	id, _ := st["id"].(string)

	body, _ := json.Marshal(map[string]string{"stage": id})
	r := e.do(t, "POST", "/api/pending-add", bytes.NewReader(body), nil)
	r.Body.Close()
	if r.StatusCode != 204 {
		t.Fatalf("post pending-add: %d", r.StatusCode)
	}

	// Nothing was added: the torrent list stays empty until the dialog's own add call.
	r = e.do(t, "GET", "/api/torrents", nil, nil)
	var list []any
	_ = json.NewDecoder(r.Body).Decode(&list)
	r.Body.Close()
	if len(list) != 0 {
		t.Fatalf("queueing must not add the torrent: %v", list)
	}

	r = e.do(t, "GET", "/api/stage/"+id, nil, nil)
	var again map[string]any
	_ = json.NewDecoder(r.Body).Decode(&again)
	r.Body.Close()
	if r.StatusCode != 200 || again["name"] != st["name"] {
		t.Fatalf("stage/%s: %d %v", id, r.StatusCode, again)
	}

	r = e.do(t, "GET", "/api/pending-add", nil, nil)
	var got []map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	r.Body.Close()
	if len(got) != 1 || got[0]["kind"] != "stage" || got[0]["stage"] != id {
		t.Fatalf("pending-add: %d %+v", r.StatusCode, got)
	}

	// Taking the queue empties it.
	r = e.do(t, "GET", "/api/pending-add", nil, nil)
	var again2 []map[string]any
	_ = json.NewDecoder(r.Body).Decode(&again2)
	r.Body.Close()
	if len(again2) != 0 {
		t.Fatalf("pending-add must be cleared after being read: %+v", again2)
	}
}

// A magnet link opened from outside queues the same way, and both an unknown stage id and an
// empty body are refused.
func TestPendingAddMagnetAndBadInput(t *testing.T) {
	e := setup(t)
	body, _ := json.Marshal(map[string]string{"magnet": "magnet:?xt=urn:btih:" + "0123456789abcdef0123456789abcdef01234567"})
	r := e.do(t, "POST", "/api/pending-add", bytes.NewReader(body), nil)
	r.Body.Close()
	if r.StatusCode != 204 {
		t.Fatalf("magnet: %d", r.StatusCode)
	}
	r = e.do(t, "GET", "/api/pending-add", nil, nil)
	var got []map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	r.Body.Close()
	if len(got) != 1 || got[0]["kind"] != "magnet" {
		t.Fatalf("magnet not queued: %+v", got)
	}

	if r := e.do(t, "POST", "/api/pending-add", bytes.NewReader([]byte(`{"stage":"nope-at-all"}`)), nil); r.StatusCode != 400 {
		t.Fatalf("unknown stage id must be refused: %d", r.StatusCode)
	}
	if r := e.do(t, "POST", "/api/pending-add", bytes.NewReader([]byte(`{}`)), nil); r.StatusCode != 400 {
		t.Fatalf("an empty body must be refused: %d", r.StatusCode)
	}
	if r := e.do(t, "GET", "/api/stage/nope-at-all", nil, nil); r.StatusCode != 404 {
		t.Fatalf("an unknown stage id must 404: %d", r.StatusCode)
	}
}

// The About endpoint reports the running build's own name and version.
func TestAboutEndpoint(t *testing.T) {
	e := setup(t)
	r := e.do(t, "GET", "/api/about", nil, nil)
	var a struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		GoVersion string `json:"goVersion"`
		OS        string `json:"os"`
	}
	_ = json.NewDecoder(r.Body).Decode(&a)
	r.Body.Close()
	if r.StatusCode != 200 || a.Name != "Equinox" || a.GoVersion == "" || a.OS == "" {
		t.Fatalf("about: %d %+v", r.StatusCode, a)
	}
}

// GET /api/update reports a newer release when GitHub has one, and nothing when it does not.
func TestUpdateEndpoint(t *testing.T) {
	oldVersion := buildinfo.Version
	buildinfo.Version = "1.0.0"
	t.Cleanup(func() { buildinfo.Version = oldVersion })

	mux := http.NewServeMux()
	installer := []byte("fake installer")
	sum := sha256.Sum256(installer)
	mux.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) { w.Write(installer) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  Equinox-Setup.exe\n"))
	})
	var srv *httptest.Server
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.0.1",
			"body":     "notes",
			"html_url": "https://example.invalid/releases/v1.0.1",
			"assets": []map[string]string{
				{"name": "Equinox-Setup.exe", "browser_download_url": srv.URL + "/setup"},
				{"name": "SHA256SUMS.txt", "browser_download_url": srv.URL + "/sums"},
			},
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldAPI := update.APIURL
	update.APIURL = srv.URL + "/release"
	t.Cleanup(func() { update.APIURL = oldAPI })

	e := setup(t)
	r := e.do(t, "GET", "/api/update?force=1", nil, nil)
	var st struct {
		Available bool   `json:"available"`
		Version   string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if r.StatusCode != 200 || !st.Available || st.Version != "1.0.1" {
		t.Fatalf("update: %d %+v", r.StatusCode, st)
	}
}
