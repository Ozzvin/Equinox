package core

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDiskFullStopsTheTorrentAndSaysWhy(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	orig := diskFree
	diskFree = func(string) (uint64, error) { return 100, nil } // "the disk has 100 bytes left"
	defer func() { diskFree = orig }()

	hash, err := m.AddFile(makeTorrent(t, dir, "big.bin", 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Paused && s.ErrorKind == ErrKindDiskFull })
	s, _ := statusOf(m, hash)
	if s.Error == "" {
		t.Fatal("the reason must be reported")
	}
	if exists(filepath.Join(m.cfg.Get().DataDir, "big.bin")) {
		t.Fatal("nothing may be allocated on a disk that cannot hold the torrent")
	}

	// Making room and resuming is the retry: the error goes away and the file is allocated.
	diskFree = orig
	if err := m.SetPaused(hash, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(m.cfg.Get().DataDir, "big.bin")) })
	if s, _ := statusOf(m, hash); s.Error != "" || s.Paused {
		t.Fatalf("resume must clear the failure: %+v", s)
	}
}

func TestWriteErrorIsReportedOnceAndPauses(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "w.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	tor, _ := m.get(hash)

	m.onWriteError(tor, hash, errors.New("the disk went away"))
	m.onWriteError(tor, hash, errors.New("second chunk failed too")) // the engine repeats itself
	s, _ := statusOf(m, hash)
	if s.ErrorKind != ErrKindWrite || s.Error != "the disk went away" || !s.Paused {
		t.Fatalf("first failure must be kept and the torrent paused: %+v", s)
	}
	if err := m.SetPaused(hash, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := statusOf(m, hash); s.Error != "" {
		t.Fatalf("resuming clears the failure: %+v", s)
	}

	// Removing a torrent forgets its failure as well.
	m.onWriteError(tor, hash, errors.New("again"))
	if err := m.Remove(hash, false); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, left := m.errs[hash]
	m.mu.Unlock()
	if left {
		t.Fatal("failure record must be dropped with the torrent")
	}
}
