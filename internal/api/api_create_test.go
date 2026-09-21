package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateTorrentEndpoint(t *testing.T) {
	e := setup(t)
	src := filepath.Join(e.dir, "share", "clip.bin")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, bytes.Repeat([]byte{8}, 48<<10), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"source": src, "comment": "api test", "seed": true, "trackers": []string{"udp://t.example:1/announce"}})
	r := e.do(t, "POST", "/api/create", bytes.NewReader(body), nil)
	var job struct {
		ID      string `json:"id"`
		Running bool   `json:"running"`
		Error   string `json:"error"`
		Output  string `json:"output"`
		Hash    string `json:"hash"`
		Seeding bool   `json:"seeding"`
	}
	_ = json.NewDecoder(r.Body).Decode(&job)
	r.Body.Close()
	if r.StatusCode != 202 || job.ID == "" || !job.Running {
		t.Fatalf("start: %d %+v", r.StatusCode, job)
	}
	deadline := time.Now().Add(10 * time.Second)
	for job.Running {
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(50 * time.Millisecond)
		r = e.do(t, "GET", "/api/create/"+job.ID, nil, nil)
		_ = json.NewDecoder(r.Body).Decode(&job)
		r.Body.Close()
	}
	if job.Error != "" || !job.Seeding || job.Hash == "" {
		t.Fatalf("job: %+v", job)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "share", "clip.bin.torrent")); err != nil {
		t.Fatal("the .torrent must be written next to the data")
	}
	// It shows up in the list and is verified as complete from the folder that holds the data.
	for {
		l := e.m.List()
		if len(l) == 1 && l[0].Progress == 1 {
			break
		}
		if time.Now().After(deadline.Add(5 * time.Second)) {
			t.Fatalf("created torrent never became a complete seed: %+v", l)
		}
		time.Sleep(50 * time.Millisecond)
	}

	for name, req := range map[string]string{
		"empty request":  `{}`,
		"not json":       `nope`,
		"missing source": `{"source":"` + filepath.ToSlash(filepath.Join(e.dir, "nothing")) + `"}`,
		"bad tracker":    `{"source":"` + filepath.ToSlash(src) + `","trackers":["ftp://x.example/a"]}`,
	} {
		r := e.do(t, "POST", "/api/create", bytes.NewReader([]byte(req)), nil)
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Errorf("%s: status %d, want 400", name, r.StatusCode)
		}
	}
	r = e.do(t, "GET", "/api/create/unknownjob", nil, nil)
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Errorf("unknown job: %d, want 404", r.StatusCode)
	}
}
