package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

// addTwoFilesOnDisk adds the two-file torrent with its data already in the download folder and waits for it.
func addTwoFilesOnDisk(t *testing.T, m *Manager, dir, name string) (hash, aPath, bPath string) {
	t.Helper()
	mi := namedTwoFileTorrent(t, dir, name)
	root := filepath.Join(dir, "downloads", name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.bin", "b.bin"} {
		b, err := os.ReadFile(filepath.Join(dir, "src", name, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := m.AddMetaInfo(mi)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, h); return ok && s.Progress == 1 })
	return h, filepath.Join(root, "a.bin"), filepath.Join(root, "b.bin")
}

func TestOpenFileSelectsThatFile(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 0 })
	h, a, b := addTwoFilesOnDisk(t, m, dir, "opn")
	var got []string
	old := openInFileManager
	openInFileManager = func(p string) error { got = append(got, p); return nil }
	defer func() { openInFileManager = old }()

	if err := m.OpenFile(h, 1); err != nil || len(got) != 1 || got[0] != b {
		t.Fatalf("second file: %v %v (want %s)", err, got, b)
	}
	if err := m.OpenFile(h, 0); err != nil || got[1] != a {
		t.Fatalf("first file: %v %v", err, got)
	}
	if err := m.OpenFile(h, 5); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an index out of range must be refused: %v", err)
	}
	// A file that is not on the disk opens its folder instead.
	waitFor(t, func() bool { return os.Remove(a) == nil }) // the engine lets go of an idle file after a moment
	if err := m.OpenFile(h, 0); err != nil || got[2] != filepath.Dir(a) {
		t.Fatalf("a missing file opens its folder: %v %v", err, got)
	}
	if err := m.OpenFile("nope", 0); err == nil {
		t.Fatal("unknown torrent")
	}
}

func TestDeleteFilesRemovesThemFromDiskAndStopsWantingThem(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 0 })
	h, a, b := addTwoFilesOnDisk(t, m, dir, "del")

	if _, err := m.DeleteFiles(h, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nothing to delete: %v", err)
	}
	if _, err := m.DeleteFiles(h, []int{9}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("out of range: %v", err)
	}
	n, err := m.DeleteFiles(h, []int{0})
	if err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if exists(a) {
		t.Fatal("the file must be gone from the disk")
	}
	if !exists(b) {
		t.Fatal("the other file must stay")
	}
	if prios := m.filePrios(h); len(prios) < 1 || prios[0] != PrioSkip {
		t.Fatalf("a deleted file is marked \"do not download\": %v", prios)
	}
	// The engine must not go on believing it has the deleted file: the torrent's progress is now about the
	// remaining file only, and it is complete again for that selection.
	waitFor(t, func() bool {
		s, ok := statusOf(m, h)
		return ok && s.Size == 48<<10 && s.Progress == 1
	})
	time.Sleep(700 * time.Millisecond)
	if exists(a) {
		t.Fatal("the deleted file must not come back")
	}
	// Deleting again (nothing on the disk any more) is not an error and deletes nothing.
	if n, err := m.DeleteFiles(h, []int{0}); err != nil || n != 0 {
		t.Fatalf("second delete: %d %v", n, err)
	}
}

// A file can be deleted while the torrent is being checked: the engine keeps opening it, so the store must
// keep it out of the engine's reach for the moment of the deletion.
func TestDeleteFileWhileTheTorrentIsBeingChecked(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 0 })
	tp := makeTorrent(t, dir, "big.bin", 200<<20)
	putOnDisk(t, dir, "big.bin")
	h, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, h); return ok && s.Progress == 1 })
	if err := m.Recheck(h); err != nil {
		t.Fatal(err)
	}
	tor, _ := m.get(h)
	waitFor(t, func() bool { c, _ := checkingPieces(tor); return c > 0 }) // hashing is under way

	n, err := m.DeleteFiles(h, []int{0})
	if err != nil || n != 1 {
		t.Fatalf("the file must go although the engine is reading it: %d %v", n, err)
	}
	if exists(filepath.Join(m.cfg.Get().DataDir, "big.bin")) {
		t.Fatal("still on the disk")
	}
	time.Sleep(1200 * time.Millisecond)
	if exists(filepath.Join(m.cfg.Get().DataDir, "big.bin")) {
		t.Fatal("the engine must not bring the deleted file back")
	}
}

func TestDeleteFileHeldByAnotherProgramSaysSo(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentChecks = 0 })
	h, a, _ := addTwoFilesOnDisk(t, m, dir, "held")
	f, err := os.Open(a) // another program has it open (on Windows this forbids deleting)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n, err := m.DeleteFiles(h, []int{0})
	if runtime.GOOS != "windows" {
		t.Skip("a file that is open can be deleted on this system")
	}
	if err == nil || n != 0 || !strings.Contains(err.Error(), "занят другой программой") || !exists(a) {
		t.Fatalf("the reason must be told in plain words: %d %v", n, err)
	}
}
