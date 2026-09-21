package core

import (
	"path/filepath"
	"testing"
)

func TestOpenFolderTargetsTheTorrentsOwnData(t *testing.T) {
	var opened []string
	orig := openInFileManager
	openInFileManager = func(p string) error { opened = append(opened, p); return nil }
	defer func() { openInFileManager = orig }()

	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeTorrent(t, dir, "shown.bin", 20<<10)
	seed(t, m, dir, "shown.bin")
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	if err := m.OpenFolder(hash); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(m.cfg.Get().DataDir, "shown.bin"); len(opened) != 1 || opened[0] != want {
		t.Fatalf("must open the torrent's own data %q, opened %v", want, opened)
	}

	// A torrent with nothing on disk yet falls back to its save folder.
	opened = nil
	paused, err := m.AddFile(makeTorrent(t, dir, "notyet.bin", 20<<10), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, paused); return ok && s.HasMeta })
	if err := m.OpenFolder(paused); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != m.saveDir(paused) {
		t.Fatalf("expected the save folder, opened %v", opened)
	}

	if err := m.OpenFolder("00000000000000000000000000000000000000ff"); err == nil {
		t.Fatal("unknown torrent must be rejected")
	}
	if err := m.OpenFolder("not a hash"); err == nil {
		t.Fatal("a malformed hash must be rejected")
	}
}
