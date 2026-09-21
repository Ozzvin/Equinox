package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

func TestWatchFolderAddsTorrentsAfterTheyAreStable(t *testing.T) {
	dir := t.TempDir()
	watch := filepath.Join(dir, "watch")
	if err := os.MkdirAll(watch, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newManager(t, dir, func(s *config.Settings) { s.WatchDir = watch })

	src := makeTorrent(t, dir, "auto.bin", 20<<10)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(watch, "Good.TORRENT") // the extension is matched without regard to case
	if err := os.WriteFile(good, data, 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(watch, "broken.torrent")
	_ = os.WriteFile(bad, []byte("this is not bencode"), 0o644)
	_ = os.WriteFile(filepath.Join(watch, "notes.txt"), []byte("ignore me"), 0o644)
	_ = os.MkdirAll(filepath.Join(watch, "sub.torrent"), 0o755) // a folder with that name is not a file

	m.scanWatch() // first sight: nothing is taken yet
	if len(m.List()) != 0 || !exists(good) || !exists(bad) {
		t.Fatal("files must wait for a second look before they are taken")
	}
	m.scanWatch()
	if len(m.List()) != 1 || m.List()[0].Name != "auto.bin" {
		t.Fatalf("the valid torrent must be added: %+v", m.List())
	}
	if exists(good) || !exists(good+".added") {
		t.Fatal("an added file must be renamed to .added")
	}
	if exists(bad) || !exists(bad+".failed") {
		t.Fatal("an unusable file must be renamed to .failed")
	}
	if !exists(filepath.Join(watch, "notes.txt")) {
		t.Fatal("other files must be left alone")
	}

	m.scanWatch() // nothing new: no churn, no duplicates
	if len(m.List()) != 1 {
		t.Fatalf("scanning again must not add anything: %d", len(m.List()))
	}
}

func TestWatchFolderIgnoresFilesStillBeingWritten(t *testing.T) {
	dir := t.TempDir()
	watch := filepath.Join(dir, "watch")
	_ = os.MkdirAll(watch, 0o755)
	m := newManager(t, dir, func(s *config.Settings) { s.WatchDir = watch })
	data, _ := os.ReadFile(makeTorrent(t, dir, "grow.bin", 20<<10))

	p := filepath.Join(watch, "grow.torrent")
	if err := os.WriteFile(p, data[:len(data)/2], 0o644); err != nil { // half of it has arrived
		t.Fatal(err)
	}
	m.scanWatch()
	if err := os.WriteFile(p, data, 0o644); err != nil { // the copy finishes
		t.Fatal(err)
	}
	m.scanWatch() // the size changed since the last look: still not taken
	if len(m.List()) != 0 || exists(p+".failed") {
		t.Fatalf("a file that changed between scans must not be processed yet: %d, failed=%v", len(m.List()), exists(p+".failed"))
	}
	m.scanWatch() // stable now
	if len(m.List()) != 1 {
		t.Fatal("the finished file must be added on the next scan")
	}
}

func TestWatchFolderOffDoesNothing(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil) // WatchDir is empty
	m.scanWatch()
	m.scanWatch()
	if len(m.List()) != 0 {
		t.Fatal("no watch folder, nothing to add")
	}
	// Switching it on through the folder settings validates and creates the folder.
	watch := filepath.Join(dir, "newwatch")
	if err := m.SetFolders(FoldersUpdate{DataDir: m.cfg.Get().DataDir, TorrentCopyDir: m.cfg.Get().TorrentCopyDir, WatchDir: watch}); err != nil {
		t.Fatal(err)
	}
	if !exists(watch) || m.cfg.Get().WatchDir == "" {
		t.Fatal("the watch folder must be created and stored")
	}
	file := filepath.Join(dir, "afile")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	if err := m.SetFolders(FoldersUpdate{DataDir: m.cfg.Get().DataDir, WatchDir: file}); err == nil {
		t.Fatal("a file is not a valid watch folder")
	}
}
