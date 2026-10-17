package api

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A .torrent lying on the server, in one of its places, goes on the "Add torrents" list; a path outside the places,
// a file that is not a .torrent and one that is not there are refused with words the page shows.
func TestStageAFileOnTheServer(t *testing.T) {
	var dl, outside string
	e := setup(t, func(s *Server) {
		dl, outside = filepath.Join(t.TempDir(), "downloads"), t.TempDir()
		s.SetPlaces([]Place{{Name: "Downloads", Path: dl}})
	})
	if err := os.MkdirAll(filepath.Join(dl, "Torrents"), 0o755); err != nil {
		t.Fatal(err)
	}
	tor := seededTorrent(t, e, make([]byte, 50<<10))
	in := filepath.Join(dl, "Torrents", "clip.torrent")
	out := filepath.Join(outside, "clip.torrent")
	notTorrent := filepath.Join(dl, "notes.txt")
	for p, b := range map[string][]byte{in: tor, out: tor, notTorrent: []byte("hello")} {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	post := func(path string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]string{"path": path})
		r := e.do(t, "POST", "/api/stage-path", strings.NewReader(string(b)), nil)
		defer r.Body.Close()
		raw, _ := io.ReadAll(r.Body)
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		return r.StatusCode, v
	}

	if c, v := post(in); c != 201 || v["name"] != "clip.mkv" {
		t.Fatalf("a .torrent in a place: %d %v", c, v)
	}
	for path, code := range map[string]string{out: "stage.no_file", notTorrent: "stage.not_torrent", filepath.Join(dl, "gone.torrent"): "stage.no_file"} {
		if c, v := post(path); c != 400 || v["code"] != code {
			t.Errorf("%s: %d %v, want 400 %s", path, c, v, code)
		}
	}
}

// Several .torrent files chosen at once are staged each on its own: one outside the places, or not there, is listed
// as failed with its code, and does not keep the others off the list.
func TestStageSeveralFilesOnTheServer(t *testing.T) {
	var dl, outside string
	e := setup(t, func(s *Server) {
		dl, outside = filepath.Join(t.TempDir(), "downloads"), t.TempDir()
		s.SetPlaces([]Place{{Name: "Downloads", Path: dl}})
	})
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dl, "a.torrent")
	b := filepath.Join(dl, "b.torrent")
	out := filepath.Join(outside, "c.torrent")
	for p, data := range map[string][]byte{a: seededTorrent(t, e, make([]byte, 50<<10)), b: seededTorrent(t, e, make([]byte, 70<<10)), out: seededTorrent(t, e, make([]byte, 90<<10))} {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := json.Marshal(map[string][]string{"paths": {a, out, b}})
	r := e.do(t, "POST", "/api/stage-path", strings.NewReader(string(body)), nil)
	defer r.Body.Close()
	var v struct {
		Staged []struct {
			Size int64 `json:"size"`
		} `json:"staged"`
		Failed []struct {
			Path string `json:"path"`
			Code string `json:"code"`
		} `json:"failed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil || r.StatusCode != 200 {
		t.Fatalf("%d %v", r.StatusCode, err)
	}
	if len(v.Staged) != 2 || v.Staged[0].Size != 50<<10 || v.Staged[1].Size != 70<<10 {
		t.Fatalf("staged %+v, want a and b in their order", v.Staged)
	}
	if len(v.Failed) != 1 || v.Failed[0].Path != out || v.Failed[0].Code != "stage.no_file" {
		t.Fatalf("failed %+v, want only the one outside the places", v.Failed)
	}

	if r := e.do(t, "POST", "/api/stage-path", strings.NewReader(`{"paths": []}`), nil); r.StatusCode != 400 {
		t.Fatalf("an empty list: %d, want 400", r.StatusCode)
	}
}
