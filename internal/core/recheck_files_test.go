package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// threeFilesOnDisk adds a torrent of three files of four pieces each (pieces 0-3, 4-7, 8-11), whose data is already
// on the disk, and waits until it is complete. It returns the hash and the files' paths.
func threeFilesOnDisk(t *testing.T, m *Manager, dir string) (string, []string) {
	t.Helper()
	root := filepath.Join(dir, "src", "three")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for k, name := range []string{"1.bin", "2.bin", "3.bin"} {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, bytes.Repeat([]byte{byte(k + 1)}, 64<<10), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := m.AddMetaInfo(&metainfo.MetaInfo{InfoBytes: ib}, WithSavePath(filepath.Join(dir, "src")), WithSkipCheck())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 && !s.Checking })
	return hash, paths
}

func spoil(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt([]byte{0xFF}, 20<<10); err != nil {
		t.Fatal(err)
	}
}

func fileDone(t *testing.T, m *Manager, hash string) []int64 {
	t.Helper()
	fs, err := m.Files(hash)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(fs))
	for i, f := range fs {
		out[i] = f.Done
	}
	return out
}

// A check of chosen files goes through their pieces only: a spoiled file that was chosen is found, one that was not is
// left alone.
func TestRecheckOfChosenFiles(t *testing.T) {
	slow(t)
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, paths := threeFilesOnDisk(t, m, dir)
	spoil(t, paths[1])
	spoil(t, paths[2])
	m.store.CloseFiles(metainfo.NewHashFromHex(hash), paths)

	if err := m.RecheckFiles(hash, []int{1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && !s.Checking && s.Progress < 1
	})
	done := fileDone(t, m, hash)
	if done[0] != 64<<10 || done[1] == 64<<10 || done[2] != 64<<10 {
		t.Fatalf("only the second file should have been checked (and found spoiled): %v", done)
	}

	if err := m.RecheckFiles(hash, []int{7}); err == nil {
		t.Fatal("a file that is not there must be refused")
	}
}

// While a check of chosen files runs, only those files show its progress; the others keep their usual one.
func TestFileCheckedShowsOnlyChosenFiles(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, _ := threeFilesOnDisk(t, m, dir)
	tor, err := m.get(hash)
	if err != nil {
		t.Fatal(err)
	}
	// waiting for its turn: the chosen file says so, the others show nothing about a check
	m.mu.Lock()
	m.checkSel[hash] = checkSel{files: []int{1}}
	m.mu.Unlock()
	if fs, _ := m.Files(hash); fs[0].Waiting || !fs[1].Waiting || fs[2].Waiting || fs[1].Checking {
		t.Fatalf("only the chosen file waits for the check: %+v", fs)
	}

	pieces := checkPieces(tor, []int{1})
	if len(pieces) != 4 || pieces[0] != 4 || pieces[3] != 7 {
		t.Fatalf("the pieces of the second file: %v", pieces)
	}
	m.mu.Lock()
	m.checkSel[hash] = checkSel{files: []int{1}, pieces: pieces}
	m.mu.Unlock()
	m.setCheckPos(tor, hash, pieces, 2)
	got := m.fileChecked(tor, hash)
	if len(got) != 3 || got[0] != -1 || got[1] != 0.5 || got[2] != -1 {
		t.Fatalf("fileChecked: %v", got)
	}
	m.mu.Lock()
	prog := m.checkProg[hash]
	m.mu.Unlock()
	if prog != 0.5 {
		t.Fatalf("the progress is the share of the chosen pieces: %v", prog)
	}
	fs, _ := m.Files(hash)
	if fs[0].Checking || !fs[1].Checking || fs[2].Checking {
		t.Fatalf("only the chosen file is shown as being checked: %+v", fs)
	}
}

// A check of chosen files cut short goes on with the same files after a restart; checks.json of the older form (a
// number per torrent) is still read.
func TestRecheckOfChosenFilesGoesOnAfterARestart(t *testing.T) {
	slow(t)
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, paths := threeFilesOnDisk(t, m, dir)
	spoil(t, paths[0])
	spoil(t, paths[2])
	m.store.CloseFiles(metainfo.NewHashFromHex(hash), paths)

	if err := os.WriteFile(m.checksPath(), []byte(`{"`+hash+`":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if pl := m.loadCheckPlaces()[hash]; pl.Pos != 3 || pl.Files != nil {
		t.Fatalf("the older form: %+v", pl)
	}

	m.saveCheckPlace(hash, 0, []int{2}) // as if the program had closed at the start of a check of the third file
	m.resumeChecks()
	waitFor(t, func() bool {
		_, err := os.Stat(m.checksPath())
		s, _ := statusOf(m, hash)
		return os.IsNotExist(err) && !s.Checking
	})
	done := fileDone(t, m, hash)
	if done[0] != 64<<10 || done[2] == 64<<10 {
		t.Fatalf("the check must have gone on with the third file only: %v", done)
	}
}
