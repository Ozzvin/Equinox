package core

import (
	"bytes"
	"os"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// seedingOnDisk is threeFilesOnDisk, then waits until the torrent is past its first check: until then the preparation
// of the disk that follows adding it may still be under way and would make the files again.
func seedingOnDisk(t *testing.T, m *Manager, dir string) (string, []string) {
	t.Helper()
	hash, paths := threeFilesOnDisk(t, m, dir)
	waitFor(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.phase[hash] == nil
	})
	return hash, paths
}

// goneFor waits until the torrent is stopped for files gone.
func goneFor(t *testing.T, m *Manager, hash string) Status {
	t.Helper()
	var st Status
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		st = s
		return ok && s.ErrorKind == ErrKindMissing
	})
	return st
}

// A file of a seeding torrent deleted by hand stops it with an error naming the file, and the pieces of that file stop
// counting as had; put back, the file is checked and the torrent seeds again by itself.
func TestMissingFileStopsTheTorrentAndItGoesOnWhenItIsBack(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, paths := seedingOnDisk(t, m, dir)
	m.store.CloseFiles(metainfo.NewHashFromHex(hash), paths)
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}

	st := goneFor(t, m, hash)
	if !st.Paused || st.ErrorCode != "torrent.file_missing" || len(st.ErrorArgs) != 1 || st.ErrorArgs[0] != paths[1] {
		t.Fatalf("stopped with %+v", st)
	}
	waitFor(t, func() bool { s, _ := statusOf(m, hash); return s.Progress < 1 })
	if done := fileDone(t, m, hash); done[0] != 64<<10 || done[1] != 0 || done[2] != 64<<10 {
		t.Fatalf("only the deleted file should have lost its pieces: %v", done)
	}

	if err := os.WriteFile(paths[1], bytes.Repeat([]byte{2}, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && s.Error == "" && !s.Paused && !s.Checking && s.Progress == 1
	})
}

// A torrent the person had paused stays paused when its files come back.
func TestMissingFileOfAPausedTorrentLeavesItPaused(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, paths := seedingOnDisk(t, m, dir)
	if err := m.SetPaused(hash, true); err != nil {
		t.Fatal(err)
	}
	m.store.CloseFiles(metainfo.NewHashFromHex(hash), paths)
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths[2]); err != nil {
		t.Fatal(err)
	}
	if st := goneFor(t, m, hash); st.ErrorCode != "torrent.files_missing" || len(st.ErrorArgs) != 2 || st.ErrorArgs[1] != "1" {
		t.Fatalf("stopped with %+v", st)
	}

	for k, i := range []int{0, 2} {
		if err := os.WriteFile(paths[i], bytes.Repeat([]byte{byte([]int{1, 3}[k])}, 64<<10), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && s.Error == "" && !s.Checking && s.Progress == 1
	})
	if s, _ := statusOf(m, hash); !s.Paused {
		t.Fatal("the torrent was paused before its files went missing and must stay so")
	}
}

// "Resume" with the files still gone clears the error and has them checked: what is not there is to be downloaded
// again, and the torrent is not stopped for it once more.
func TestResumeWithFilesGoneDownloadsThemAgain(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, paths := seedingOnDisk(t, m, dir)
	m.store.CloseFiles(metainfo.NewHashFromHex(hash), paths)
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	goneFor(t, m, hash)

	if err := m.SetPaused(hash, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && s.Error == "" && !s.Paused && !s.Checking && s.CheckQueued == 0
	})
	tt, err := m.get(hash)
	if err != nil {
		t.Fatal(err)
	}
	m.lookForMissing(tt, hash, m.snapshotRecords()[hash])
	if s, _ := statusOf(m, hash); s.Error != "" || s.Paused || s.Progress == 1 {
		t.Fatalf("after Resume: %+v", s)
	}
}

// A skipped file, and one nothing of which was had yet, are not missed when they are not on the disk.
func TestSkippedFileIsNotMissed(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, paths := seedingOnDisk(t, m, dir)
	if err := m.SetFilePriorities(hash, []int{2}, PrioSkip); err != nil {
		t.Fatal(err)
	}
	m.store.CloseFiles(metainfo.NewHashFromHex(hash), paths)
	if err := os.Remove(paths[2]); err != nil {
		t.Fatal(err)
	}
	tt, err := m.get(hash)
	if err != nil {
		t.Fatal(err)
	}
	m.lookForMissing(tt, hash, m.snapshotRecords()[hash])
	if s, _ := statusOf(m, hash); s.Error != "" || s.Paused {
		t.Fatalf("a skipped file gone stopped the torrent: %+v", s)
	}
}
