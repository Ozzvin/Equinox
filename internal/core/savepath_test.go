package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

func TestSaveFolderPerTorrentAndLabel(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) {
		s.LabelPaths = map[string]string{"Фильмы": filepath.Join(dir, "movies")}
	})
	defDir := m.cfg.Get().DataDir

	// 1) no options: default folder.
	h1, err := m.AddFile(makeTorrent(t, dir, "plain.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(defDir, "plain.bin")) })

	// 2) explicit folder.
	custom := filepath.Join(dir, "elsewhere", "deep")
	h2, err := m.AddFile(makeTorrent(t, dir, "custom.bin", 20<<10), WithSavePath(custom))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(custom, "custom.bin")) })
	if exists(filepath.Join(defDir, "custom.bin")) {
		t.Fatal("explicit folder must not also create the file in the default one")
	}

	// 3) label with a configured folder.
	h3, err := m.AddFile(makeTorrent(t, dir, "film.bin", 20<<10), WithLabel("Фильмы"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(dir, "movies", "film.bin")) })

	// An explicit folder beats the label's.
	if _, err := m.AddFile(makeTorrent(t, dir, "both.bin", 20<<10), WithLabel("Фильмы"), WithSavePath(custom)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(custom, "both.bin")) })

	for _, s := range m.List() {
		if s.SavePath == "" {
			t.Fatalf("status must report the folder: %+v", s)
		}
	}

	// Removing with data deletes from that torrent's own folder only.
	if err := m.Remove(h2, true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(custom, "custom.bin")) {
		t.Fatal("data in the custom folder was not deleted")
	}
	if !exists(filepath.Join(defDir, "plain.bin")) || !exists(filepath.Join(dir, "movies", "film.bin")) {
		t.Fatal("other torrents' data must stay")
	}
	_, _ = h1, h3

	// The folder survives a restart (and is what the storage uses again).
	m.Close()
	cfg, _ := config.Load(filepath.Join(dir, "settings.json"), dir)
	m2, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m2.Close(); waitFor(t, func() bool { return true }) }()
	got := map[string]string{}
	for _, s := range m2.List() {
		got[s.Name] = s.SavePath
	}
	if got["film.bin"] != filepath.Join(dir, "movies") {
		t.Fatalf("label folder lost after restart: %v", got)
	}
}

func TestBadFoldersAreRejected(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	file := filepath.Join(dir, "iamafile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddFile(makeTorrent(t, dir, "x.bin", 10<<10), WithSavePath(file)); err == nil {
		t.Fatal("a file must not be accepted as a download folder")
	}
	if len(m.List()) != 0 || exists(filepath.Join(dir, "torrents", "x.torrent")) {
		t.Fatal("a failed add must leave nothing behind")
	}

	before := m.cfg.Get()
	err := m.SetFolders(FoldersUpdate{DataDir: filepath.Join(dir, "newdata"), TorrentCopyDir: file})
	if err == nil {
		t.Fatal("unusable copy folder must be rejected")
	}
	if after := m.cfg.Get(); after.DataDir != before.DataDir {
		t.Fatal("nothing may change when one folder is bad")
	}
	if err := m.SetFolders(FoldersUpdate{DataDir: filepath.Join(dir, "newdata"), TorrentCopyDir: ""}); err != nil {
		t.Fatal(err)
	}
	if s := m.cfg.Get(); s.TorrentCopyDir != "" || filepath.Base(s.DataDir) != "newdata" {
		t.Fatalf("folders not applied: %+v", s)
	}
	if err := m.SetListenPort(70000); err == nil {
		t.Fatal("port out of range must be rejected")
	}
}

// A label may exist without a folder: it is kept, and new torrents with it go to the general folder.
func TestLabelWithoutFolderIsKept(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	cur := m.cfg.Get()
	movies := filepath.Join(dir, "movies")
	err := m.SetFolders(FoldersUpdate{
		DataDir: cur.DataDir, TorrentCopyDir: cur.TorrentCopyDir,
		LabelPaths: map[string]string{"Музыка": "", "Фильмы": movies, "  ": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := m.cfg.Get().LabelPaths
	if p, ok := got["Музыка"]; !ok || p != "" {
		t.Fatalf("a label without a folder must be kept: %v", got)
	}
	if got["Фильмы"] == "" || len(got) != 2 {
		t.Fatalf("labels: %v (a blank name must be dropped)", got)
	}
	h, err := m.AddFile(makeTorrent(t, dir, "song.bin", 20<<10), WithLabel("Музыка"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(cur.DataDir, "song.bin")) })
	_ = h
}
