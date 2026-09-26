package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fsGet(t *testing.T, e *env, path string) fsListing {
	t.Helper()
	r := e.do(t, "GET", "/api/fs?path="+url.QueryEscape(path), nil, nil)
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/fs %q: %d", path, r.StatusCode)
	}
	var l fsListing
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestFolderPickerListing(t *testing.T) {
	e := setup(t)
	root := filepath.Join(e.dir, "tree")
	for _, d := range []string{"beta", "Alpha", filepath.Join("Alpha", "inner")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := fsGet(t, e, root)
	if l.Path != root || len(l.Entries) != 3 {
		t.Fatalf("listing: %+v", l)
	}
	// folders first, then files, each in name order regardless of case
	if l.Entries[0].Name != "Alpha" || l.Entries[1].Name != "beta" || l.Entries[2].Name != "file.txt" || l.Entries[2].Dir || l.Entries[2].Size != 5 {
		t.Fatalf("order or kinds wrong: %+v", l.Entries)
	}
	if l.Parent == nil || *l.Parent != filepath.Dir(root) {
		t.Fatalf("parent: %v", l.Parent)
	}
	if last := l.Crumbs[len(l.Crumbs)-1]; last.Name != "tree" || last.Path != root {
		t.Fatalf("crumbs must end at the folder: %+v", l.Crumbs)
	}
	if len(l.Places) == 0 || l.Places[0].Kind != "data" {
		t.Fatalf("the download folder must come first among the places: %+v", l.Places)
	}

	// a typed path that does not exist falls back to the nearest folder that does
	if l := fsGet(t, e, filepath.Join(root, "Alpha", "nope", "deeper")); l.Path != filepath.Join(root, "Alpha") {
		t.Fatalf("nearest existing folder: %q", l.Path)
	}
	// no path: the download folder
	if l := fsGet(t, e, ""); l.Path == "" {
		t.Fatal("an empty path must start in the download folder")
	}
	if runtime.GOOS == "windows" {
		l := fsGet(t, e, rootsMark)
		if l.Path != "" || l.Parent != nil || len(l.Entries) == 0 || !l.Entries[0].Dir {
			t.Fatalf("the list of drives: %+v", l)
		}
		vol := filepath.VolumeName(root) + `\`
		if up := fsGet(t, e, vol); up.Parent == nil || *up.Parent != rootsMark {
			t.Fatalf("above a drive comes the list of drives: %+v", up.Parent)
		}
	}
}

func TestFolderPickerMkdir(t *testing.T) {
	e := setup(t)
	mk := func(parent, name string) (int, map[string]string) {
		body, _ := json.Marshal(map[string]string{"parent": parent, "name": name})
		r := e.do(t, "POST", "/api/fs/mkdir", bytes.NewReader(body), nil)
		defer r.Body.Close()
		var out map[string]string
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	code, out := mk(e.dir, "Новая папка")
	if code != http.StatusCreated || out["path"] != filepath.Join(e.dir, "Новая папка") {
		t.Fatalf("create: %d %v", code, out)
	}
	if st, err := os.Stat(out["path"]); err != nil || !st.IsDir() {
		t.Fatal("the folder was not created")
	}
	// every refusal has a message and a code, so a client in another language can word it itself
	for name, tc := range map[string][3]string{
		"exists":       {e.dir, "Новая папка", "fs.exists"},
		"empty":        {e.dir, "  ", "fs.name_empty"},
		"dots":         {e.dir, "..", "fs.name_empty"},
		"separator":    {e.dir, `a/b`, "fs.name_chars"},
		"bad char":     {e.dir, `a:b`, "fs.name_chars"},
		"trailing dot": {e.dir, "abc.", "fs.name_end"},
		"no parent":    {filepath.Join(e.dir, "missing"), "x", "fs.parent_missing"},
	} {
		if code, out := mk(tc[0], tc[1]); code != http.StatusBadRequest || out["error"] == "" || out["code"] != tc[2] {
			t.Errorf("%s: %d %v, want 400 with a message and the code %s", name, code, out, tc[2])
		}
	}
	if r := e.do(t, "GET", "/api/fs", nil, map[string]string{"Authorization": "Bearer wrong"}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the key: %d", r.StatusCode)
	}
}
