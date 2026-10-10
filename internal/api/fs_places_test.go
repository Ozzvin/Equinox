package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A server given folders by name (Umbrel: Downloads is /downloads) offers just those in the folder picker, does not
// go above them, starts the way with the folder's name and hands the names to the page before anything else.
func TestFolderPickerPlaces(t *testing.T) {
	var dl, media string
	e := setup(t, func(s *Server) {
		dl, media = filepath.Join(t.TempDir(), "downloads"), filepath.Join(t.TempDir(), "media-not-chosen")
		s.SetPlaces([]Place{{Name: "Downloads", Path: dl}, {Name: "Media", Path: media}})
	})
	deep := filepath.Join(dl, "Equinox", "incomplete")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	l := fsGet(t, e, deep)
	if l.Path != deep {
		t.Fatalf("path %q, want %q", l.Path, deep)
	}
	if len(l.Places) != 1 || l.Places[0].Name != "Downloads" || l.Places[0].Kind != "place" {
		t.Fatalf("places %+v: want only Downloads (the media slot is not chosen, so not there)", l.Places)
	}
	if len(l.Crumbs) != 3 || l.Crumbs[0].Name != "Downloads" || l.Crumbs[0].Path != dl || l.Crumbs[2].Name != "incomplete" {
		t.Fatalf("crumbs %+v: want Downloads › Equinox › incomplete", l.Crumbs)
	}
	if l.Parent == nil || *l.Parent != filepath.Join(dl, "Equinox") {
		t.Fatalf("parent %v", l.Parent)
	}

	if top := fsGet(t, e, dl); top.Parent != nil {
		t.Fatalf("no way above a place: parent %q", *top.Parent)
	}
	if out := fsGet(t, e, e.dir); out.Path != dl { // outside every place: the first place instead
		t.Fatalf("a folder outside the places shows %q, want %q", out.Path, dl)
	}

	r := e.do(t, "POST", "/api/fs/mkdir", bytes.NewReader([]byte(`{"parent":`+jsonString(e.dir)+`,"name":"x"}`)), nil)
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("a folder made outside the places: %d, want 400", r.StatusCode)
	}
	r = e.do(t, "POST", "/api/fs/mkdir", bytes.NewReader([]byte(`{"parent":`+jsonString(deep)+`,"name":"x"}`)), nil)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("a folder made inside a place: %d %s, want 201", r.StatusCode, body)
	}

	res, err := http.Get(e.srv.URL + "/session.js")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), "__equinoxPlaces") || !strings.Contains(string(b), `"Downloads"`) {
		t.Fatalf("session.js does not hand the places to the page: %s", b)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
