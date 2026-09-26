package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/core"
)

func TestStageByLink(t *testing.T) {
	defer core.AllowLocalFetches(false)
	info := metainfo.Info{Name: "film.bin", PieceLength: 16 << 10, Length: 100, Pieces: make([]byte, 20)}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var file strings.Builder
	if err := (&metainfo.MetaInfo{InfoBytes: ib, Comment: "from a link"}).Write(&file); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/film.torrent", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(file.String())) })
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>log in first</html>")) })
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	host := httptest.NewServer(mux)
	defer host.Close()

	e := setup(t)
	post := func(link string) (int, map[string]any) {
		body, _ := json.Marshal(map[string]string{"url": link})
		r := e.do(t, "POST", "/api/stage-url", strings.NewReader(string(body)), nil)
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}

	core.AllowLocalFetches(false)
	if code, out := post(host.URL + "/film.torrent"); code != http.StatusBadRequest || out["code"] != "url.blocked" {
		t.Fatalf("a link to this computer: %d %v", code, out)
	}

	core.AllowLocalFetches(true)
	code, out := post(host.URL + "/film.torrent")
	if code != http.StatusCreated || out["name"] != "film.bin" || out["id"] == "" || out["comment"] != "from a link" {
		t.Fatalf("a link to a torrent: %d %v", code, out)
	}
	// it is on the list like an uploaded file: it can be asked about again by its id
	if r := e.do(t, "GET", "/api/stage/"+out["id"].(string), nil, nil); r.StatusCode != http.StatusOK {
		t.Errorf("the staged torrent must be known: %d", r.StatusCode)
	}
	for name, tc := range map[string]struct{ link, code string }{
		"a page, not a torrent": {host.URL + "/page", "url.not_torrent"},
		"gone":                  {host.URL + "/gone", "url.status"},
		"not a web address":     {"magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "url.invalid"},
	} {
		if code, out := post(tc.link); code != http.StatusBadRequest || out["code"] != tc.code || out["error"] == "" {
			t.Errorf("%s: %d %v, want 400 %s", name, code, out, tc.code)
		}
	}
	if r := e.do(t, "POST", "/api/stage-url", strings.NewReader(`{"url":"http://x/y"}`), map[string]string{"Authorization": "Bearer wrong"}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the key: %d", r.StatusCode)
	}
}

func TestChangelogEndpoint(t *testing.T) {
	e := setup(t)
	r := e.do(t, "GET", "/api/changelog", nil, nil)
	defer r.Body.Close()
	var out struct {
		Current  string `json:"current"`
		Releases []struct {
			Version string `json:"version"`
			Body    string `json:"body"`
		} `json:"releases"`
	}
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", r.StatusCode, err)
	}
	if len(out.Releases) == 0 || out.Releases[0].Version == "" || out.Releases[0].Body == "" {
		t.Fatalf("the changelog the program carries: %+v", out.Releases)
	}
	if r := e.do(t, "GET", "/api/changelog", nil, map[string]string{"Authorization": "Bearer wrong"}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the key: %d", r.StatusCode)
	}
}
