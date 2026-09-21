package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stageFile(t *testing.T, e *env, data []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "x.torrent")
	fw.Write(data)
	mw.Close()
	r := e.do(t, "POST", "/api/stage", &body, map[string]string{"Content-Type": mw.FormDataContentType()})
	defer r.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out)
	return r.StatusCode, out
}

func TestStageAndBatchAddEndpoints(t *testing.T) {
	e := setup(t)
	code, st := stageFile(t, e, seededTorrent(t, e, bytes.Repeat([]byte{6}, 64<<10)))
	if code != 201 || st["id"] == "" || st["name"] != "clip.mkv" {
		t.Fatalf("stage: %d %v", code, st)
	}
	files, _ := st["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("the file list must come back: %v", st)
	}
	if code, _ := stageFile(t, e, []byte("not a torrent")); code != 400 {
		t.Fatalf("garbage must be refused: %d", code)
	}

	dest := filepath.Join(e.dir, "where")
	body, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"stage": st["id"], "options": map[string]any{"savePath": dest, "label": "Batch", "paused": true, "edgePieces": true, "skipCheck": false}},
		{"infohash": strings.Repeat("ab", 20)},
		{"infohash": "zzz"},
	}})
	r := e.do(t, "POST", "/api/add-batch", bytes.NewReader(body), nil)
	var out struct {
		Results []struct {
			OK    bool   `json:"ok"`
			Hash  string `json:"hash"`
			Error string `json:"error"`
		} `json:"results"`
	}
	_ = json.NewDecoder(r.Body).Decode(&out)
	r.Body.Close()
	if r.StatusCode != 200 || len(out.Results) != 3 || !out.Results[0].OK || !out.Results[1].OK || out.Results[2].OK || out.Results[2].Error == "" {
		t.Fatalf("batch: %d %+v", r.StatusCode, out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		found := false
		for _, s := range e.m.List() {
			if s.Hash == out.Results[0].Hash && s.Paused && s.Label == "Batch" && s.SavePath == dest {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the entry was not added with its options: %+v", e.m.List())
		}
		time.Sleep(50 * time.Millisecond)
	}

	for name, body := range map[string]string{"empty": `{"items":[]}`, "no body": `nope`} {
		r := e.do(t, "POST", "/api/add-batch", bytes.NewReader([]byte(body)), nil)
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Errorf("%s: %d, want 400", name, r.StatusCode)
		}
	}

	// A staged file can be dropped from the list.
	_, st2 := stageFile(t, e, seededTorrent(t, e, bytes.Repeat([]byte{2}, 32<<10)))
	r = e.do(t, "DELETE", "/api/stage/"+st2["id"].(string), nil, nil)
	r.Body.Close()
	if r.StatusCode != 204 {
		t.Fatalf("unstage: %d", r.StatusCode)
	}
	b2, _ := json.Marshal(map[string]any{"items": []map[string]any{{"stage": st2["id"]}}})
	r = e.do(t, "POST", "/api/add-batch", bytes.NewReader(b2), nil)
	_ = json.NewDecoder(r.Body).Decode(&out)
	r.Body.Close()
	if len(out.Results) != 1 || out.Results[0].OK {
		t.Fatalf("a dropped entry must not be added: %+v", out)
	}
}

func TestAddDialogInfoAndDefaults(t *testing.T) {
	e := setup(t)
	r := e.do(t, "GET", "/api/add-dialog", nil, nil)
	var d struct {
		Defaults struct {
			EdgePieces bool `json:"edgePieces"`
			Paused     bool `json:"paused"`
			SkipCheck  bool `json:"skipCheck"`
		} `json:"defaults"`
		DataDir     string `json:"dataDir"`
		Preallocate bool   `json:"preallocate"`
		MaxConns    int    `json:"maxConnsPerTorrent"`
	}
	_ = json.NewDecoder(r.Body).Decode(&d)
	r.Body.Close()
	if r.StatusCode != 200 || d.DataDir == "" || !d.Defaults.EdgePieces || d.MaxConns != 50 {
		t.Fatalf("dialog info: %d %+v", r.StatusCode, d)
	}

	done := filepath.Join(e.dir, "readyfolder")
	body, _ := json.Marshal(map[string]any{"paused": true, "skipCheck": true, "edgePieces": false, "moveDoneEnabled": true, "moveDone": done})
	r = e.do(t, "PUT", "/api/add-defaults", bytes.NewReader(body), nil)
	_ = json.NewDecoder(r.Body).Decode(&d)
	r.Body.Close()
	if r.StatusCode != 200 || !d.Defaults.Paused || !d.Defaults.SkipCheck || d.Defaults.EdgePieces {
		t.Fatalf("defaults not saved: %d %+v", r.StatusCode, d)
	}
	if _, err := os.Stat(done); err != nil {
		t.Fatal("the completed folder must be created")
	}
	bad, _ := json.Marshal(map[string]any{"moveDoneEnabled": true, "moveDone": filepath.Join(e.dir, "settings.json")})
	r = e.do(t, "PUT", "/api/add-defaults", bytes.NewReader(bad), nil)
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("a file as folder must give 400, got %d", r.StatusCode)
	}
}
