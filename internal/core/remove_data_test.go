package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// makeFolderTorrent writes the files into <dir>/src/<name>/ and returns a .torrent of that folder.
func makeFolderTorrent(t *testing.T, dir, name string, files map[string]int) (torrentPath string) {
	t.Helper()
	root := filepath.Join(dir, "src", name)
	for rel, size := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	torrentPath = filepath.Join(dir, name+".torrent")
	f, err := os.Create(torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := (&metainfo.MetaInfo{InfoBytes: b}).Write(f); err != nil {
		t.Fatal(err)
	}
	return torrentPath
}

// Deleting a torrent with its files takes only its own files and the folders they leave empty: a file put into the
// torrent's folder by someone else stays, and so does the folder, with a word about it.
func TestRemoveWithDataKeepsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeFolderTorrent(t, dir, "Show", map[string]int{"e01.mkv": 40 << 10, "extras/making.mkv": 20 << 10})
	save := filepath.Join(dir, "src") // the data is already there: the torrent seeds it
	show := filepath.Join(save, "Show")
	srt := filepath.Join(show, "e01.srt")
	if err := os.WriteFile(srt, []byte("subtitles"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := m.AddFile(tp, WithSavePath(save))
	if err != nil {
		t.Fatal(err)
	}
	err = m.Remove(hash, true)
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != "remove.kept_folder" {
		t.Fatalf("the kept folder must be told about: %v", err)
	}
	if !exists(srt) {
		t.Fatal("a file that is not the torrent's was deleted")
	}
	for _, p := range []string{filepath.Join(show, "e01.mkv"), filepath.Join(show, "extras", "making.mkv"), filepath.Join(show, "extras")} {
		if exists(p) {
			t.Errorf("%s was left", p)
		}
	}
}

// With nothing else in it the torrent's folder goes too, and nothing is said.
func TestRemoveWithDataDeletesTheEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeFolderTorrent(t, dir, "Album", map[string]int{"01.flac": 30 << 10, "cd2/02.flac": 30 << 10})
	save := filepath.Join(dir, "src")

	hash, err := m.AddFile(tp, WithSavePath(save))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(hash, true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(save, "Album")) {
		t.Fatal("the torrent's folder was left")
	}
	if !exists(save) {
		t.Fatal("the save folder itself was deleted")
	}
}
