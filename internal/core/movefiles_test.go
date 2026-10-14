package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// noNameTorrent makes a torrent without a folder of its own (its name is "-"): a.bin and sub/b.bin, written right into
// save, next to a file of someone else's, other.txt. It returns the .torrent file and its info's bytes.
func noNameTorrent(t *testing.T, dir, save string) (torrentPath string, infoBytes []byte) {
	t.Helper()
	src := filepath.Join(dir, "nn-src")
	for rel, size := range map[string]int{"a.bin": 40 << 10, "sub/b.bin": 20 << 10} {
		for _, root := range []string{src, save} {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			b := make([]byte, size)
			for i := range b {
				b[i] = byte(i * 13)
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(save, "other.txt"), []byte("not the torrent's"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(src); err != nil {
		t.Fatal(err)
	}
	info.Name = metainfo.NoName
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	torrentPath = filepath.Join(dir, "noname.torrent")
	f, err := os.Create(torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := (&metainfo.MetaInfo{InfoBytes: b}).Write(f); err != nil {
		t.Fatal(err)
	}
	return torrentPath, b
}

// A torrent without a folder of its own moves file by file: its files go, someone else's file stays, and the torrent
// is complete in the new folder.
func TestNoNameTorrentMovesItsFiles(t *testing.T) {
	dir := t.TempDir()
	save, target := filepath.Join(dir, "dl"), filepath.Join(dir, "done")
	tp, _ := noNameTorrent(t, dir, save)
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(tp, WithSavePath(save))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 && !s.Checking })

	if err := m.MoveStorage(hash, target); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && s.Moving == 0 && s.SavePath == target && s.Progress == 1
	})
	for _, rel := range []string{"a.bin", filepath.Join("sub", "b.bin")} {
		if !exists(filepath.Join(target, rel)) {
			t.Errorf("%s did not get to the new folder", rel)
		}
		if exists(filepath.Join(save, rel)) {
			t.Errorf("%s was left in the old folder", rel)
		}
	}
	if exists(filepath.Join(save, "sub")) {
		t.Error("the emptied folder was left behind")
	}
	if !exists(filepath.Join(save, "other.txt")) {
		t.Fatal("a file that is not the torrent's was moved or deleted")
	}

	// and deleted with its files: only its own
	if err := m.Remove(hash, true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(target, "a.bin")) || exists(filepath.Join(target, "sub")) {
		t.Error("the torrent's files were not deleted")
	}
	if !exists(target) {
		t.Error("the save folder itself was deleted")
	}
}

// A move file by file cut short: without the mark the renamed files go back and the copies go; with it the move is
// finished.
func TestRecoverFileMove(t *testing.T) {
	for _, copied := range []bool{false, true} {
		dir := t.TempDir()
		save, target := filepath.Join(dir, "dl"), filepath.Join(dir, "done")
		_, ib := noNameTorrent(t, dir, save)
		var info metainfo.Info
		if err := bencode.Unmarshal(ib, &info); err != nil {
			t.Fatal(err)
		}
		hash := metainfo.HashBytes(ib).HexString()
		m := newStateOnlyManager(t, dir)
		m.stateDir = dir
		if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(m.metaPath(hash))
		if err != nil {
			t.Fatal(err)
		}
		if err := (&metainfo.MetaInfo{InfoBytes: ib}).Write(f); err != nil {
			t.Fatal(err)
		}
		f.Close()

		// a.bin was renamed across, sub/b.bin copied (both ends there)
		if err := os.MkdirAll(filepath.Join(target, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(save, "a.bin"), filepath.Join(target, "a.bin")); err != nil {
			t.Fatal(err)
		}
		if err := os_copy(filepath.Join(save, "sub", "b.bin"), filepath.Join(target, "sub", "b.bin")); err != nil {
			t.Fatal(err)
		}
		err = m.state.with(func(s *state) {
			s.Torrents[hash] = &record{InfoHash: hash, SavePath: save, MoveFrom: save, MoveTo: target, MoveDir: target, MoveCopied: copied, MoveFiles: true}
		})
		if err != nil {
			t.Fatal(err)
		}
		m.recoverMoves()

		r := recordOf(t, m, hash)
		if r.MoveTo != "" || r.MoveFiles || r.MoveCopied {
			t.Errorf("copied=%v: the trail was not cleared: %+v", copied, r)
		}
		here, gone := save, target
		if copied {
			here, gone = target, save
		}
		if r.SavePath != here {
			t.Errorf("copied=%v: the torrent is in %q, want %q", copied, r.SavePath, here)
		}
		for _, rel := range []string{"a.bin", filepath.Join("sub", "b.bin")} {
			if !exists(filepath.Join(here, rel)) || exists(filepath.Join(gone, rel)) {
				t.Errorf("copied=%v: %s is not only in %s", copied, rel, here)
			}
		}
		if !exists(filepath.Join(save, "other.txt")) {
			t.Errorf("copied=%v: someone else's file was touched", copied)
		}
	}
}
