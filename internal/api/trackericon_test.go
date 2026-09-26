package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrackerIconEndpoint(t *testing.T) {
	e := setup(t)
	icons := filepath.Join(e.dir, "icons")
	if err := os.MkdirAll(icons, 0o755); err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)
	if err := os.WriteFile(filepath.Join(icons, "kept.example.img"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(icons, "none.example.none"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, "GET", "/api/tracker-icon?site=kept.example", nil, nil)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "image/png" || len(body) != len(png) || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the kept icon: %d %q %d bytes", r.StatusCode, r.Header.Get("Content-Type"), len(body))
	}
	// an <img> cannot send the header: the key may come in the address, for this one only
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/tracker-icon?site=kept.example&token="+tok, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("with the key in the address: %v %v", resp, err)
	}
	req, _ = http.NewRequest("GET", e.srv.URL+"/api/torrents?token="+tok, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the key in the address must not open the rest of the API: %v", resp)
	}
	req, _ = http.NewRequest("GET", e.srv.URL+"/api/tracker-icon?site=kept.example", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the key: %v", resp)
	}
	if r := e.do(t, "GET", "/api/tracker-icon?site=none.example", nil, nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("a site with no icon: %d", r.StatusCode)
	}
	if r := e.do(t, "GET", "/api/tracker-icon?site=../etc", nil, nil); r.StatusCode != http.StatusBadRequest {
		t.Errorf("not a site: %d", r.StatusCode)
	}
}

func TestTrackerIconsSetting(t *testing.T) {
	e := setup(t)
	put := func(extra string) map[string]any {
		body := `{"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"` + extra + `}`
		r := e.do(t, "PUT", "/api/settings", strings.NewReader(body), nil)
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return out
	}
	if put("")["trackerIcons"] != true {
		t.Error("the icons are on by default")
	}
	if put(`,"trackerIcons":false`)["trackerIcons"] != false {
		t.Error("off not saved")
	}
	if put("")["trackerIcons"] != false {
		t.Error("omitted must stay")
	}
	if r := e.do(t, "GET", "/api/tracker-icon?site=kept.example", nil, nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("with the icons off: %d", r.StatusCode)
	}
}
