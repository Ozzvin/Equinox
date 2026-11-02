package core

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/config"
)

func TestDiskWatcherRequiresRepeatedFailuresAndResets(t *testing.T) {
	w := newDiskWatcher()
	now := time.Unix(100, 0)
	w.now = func() time.Time { return now }
	if w.failed("a") || w.failed("a") {
		t.Fatal("a burst of errors must not stop a torrent")
	}
	now = now.Add(time.Second)
	if w.failed("a") {
		t.Fatal("second failure")
	}
	w.healthy("a")
	if w.failed("a") {
		t.Fatal("success must reset confirmation")
	}
	now = now.Add(time.Second)
	if w.failed("a") {
		t.Fatal("second failure after reset")
	}
	now = now.Add(time.Second)
	if !w.failed("a") {
		t.Fatal("third failure must confirm")
	}
	if w.failed("b") {
		t.Fatal("another torrent must be independent")
	}
}

func TestDiskWriteFailureExpiresWhenWritesRecover(t *testing.T) {
	w := newDiskWatcher()
	now := time.Unix(100, 0)
	w.now = func() time.Time { return now }
	w.noteWrite("a")
	if !w.recentWriteFailure("a") {
		t.Fatal("lost write failure")
	}
	now = now.Add(3 * time.Second)
	if w.confirmed("a") {
		t.Fatal("one failed write is not three failures")
	}
	w.noteWrite("a")
	now = now.Add(time.Second)
	w.noteWrite("a")
	if !w.confirmed("a") {
		t.Fatal("repeated failed writes were not confirmed")
	}
	now = now.Add(7 * time.Second)
	if w.recentWriteFailure("a") {
		t.Fatal("a transient write error must expire")
	}
	w.healthy("a")
	if len(w.writes) != 0 {
		t.Fatal("write failure not forgotten")
	}
}

func TestUnavailableDiskStopsAndRechecksWhenBack(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.SeedTimeLimitMinutes = 0 })
	hash, paths := seedingOnDisk(t, m, dir)
	tt, _ := m.get(hash)
	w := newDiskWatcher()
	var fault atomic.Int32
	var seconds atomic.Int64
	seconds.Store(100)
	w.now = func() time.Time { return time.Unix(seconds.Load(), 0) }
	w.dir = func(p string) error {
		if fault.Load() == 1 {
			return errors.New("transport endpoint is not connected")
		}
		return readableDirectory(p)
	}
	w.stat = func(p string) (os.FileInfo, error) {
		if fault.Load() == 2 && p == paths[1] {
			return nil, errors.New("input/output error")
		}
		return os.Stat(p)
	}
	m.mu.Lock()
	m.diskWatch = w
	m.mu.Unlock()
	scan := func() { m.lookForMissing(tt, hash, m.snapshotRecords()[hash]) }
	// A single failure that clears is invisible to the user.
	fault.Store(1)
	scan()
	fault.Store(0)
	scan()
	if st, _ := statusOf(m, hash); st.Error != "" || st.Paused {
		t.Fatalf("transient error: %+v", st)
	}
	for round, mode := range []int32{1, 2, 1} {
		// The last round starts user-paused; recovery must respect that choice.
		wasPaused := round == 2
		if wasPaused {
			if err := m.SetPaused(hash, true); err != nil {
				t.Fatal(err)
			}
		}
		fault.Store(mode)
		for i := 0; i < 3; i++ {
			seconds.Add(5)
			scan()
		}
		st, _ := statusOf(m, hash)
		if st.ErrorKind != ErrKindUnavailable || !st.Paused || len(st.ErrorArgs) != 1 {
			t.Fatalf("mode %d: %+v", mode, st)
		}
		if mode == 1 {
			if err := m.SetPaused(hash, false); err == nil {
				t.Fatal("resume must not ignore an inaccessible disk")
			}
		}
		fault.Store(0)
		scan()
		waitFor(t, func() bool {
			st, _ := statusOf(m, hash)
			return st.Error == "" && st.Paused == wasPaused && !st.Checking && st.Progress == 1
		})
	}
}

func TestReadableDirectoryAcceptsEmptyAndRejectsFile(t *testing.T) {
	dir := t.TempDir()
	if err := readableDirectory(dir); err != nil {
		t.Fatal(err)
	}
	if err := readableDirectory(dir + "/missing"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := readableDirectory(file); err == nil {
		t.Fatal("a file is not a readable directory")
	}
}
