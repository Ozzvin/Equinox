package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A server behind a proxy (Umbrel) runs on another machine: it refuses to open a file manager no one would see, and
// tells the page so, which then does not offer "Показать в Проводнике" at all.
func TestRemoteServerOpensNoFileManager(t *testing.T) {
	e := setup(t, func(s *Server) { s.SetOpen(nil) })
	for _, p := range []string{"/api/torrents/" + strings.Repeat("a", 40) + "/open-folder", "/api/torrents/" + strings.Repeat("a", 40) + "/files/0/open"} {
		req, _ := http.NewRequest("POST", e.srv.URL+p, nil)
		req.Header.Set("Authorization", "Bearer "+OpenToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "fs.no_file_manager") {
			t.Errorf("%s: %d %s, want 400 fs.no_file_manager", p, res.StatusCode, b)
		}
	}
	res, err := http.Get(e.srv.URL + "/session.js")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), "__equinoxRemote = true") {
		t.Fatalf("session.js does not say the server is remote: %s", b)
	}
}
