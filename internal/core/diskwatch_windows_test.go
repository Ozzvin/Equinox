package core

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestStorageIOErrorsOnWindows(t *testing.T) {
	for _, code := range []error{windows.ERROR_IO_DEVICE, windows.ERROR_NOT_READY, windows.ERROR_PATH_NOT_FOUND,
		windows.ERROR_NETNAME_DELETED, windows.ERROR_DEVICE_NOT_CONNECTED, windows.ERROR_SEM_TIMEOUT} {
		if !storageIOError(fmt.Errorf("open: %w", code)) {
			t.Errorf("not classified: %v", code)
		}
	}
	for _, code := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_DISK_FULL, windows.ERROR_SHARING_VIOLATION} {
		if storageIOError(code) {
			t.Errorf("must keep its original handling: %v", code)
		}
	}
}

func TestWriteIOFailureIsConfirmedAndRecovers(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash := makeAndAdd(t, m, dir)
	tt, _ := m.get(hash)
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return !m.filesBusy(hash) })
	w := newDiskWatcher()
	var seconds atomic.Int64
	seconds.Store(100)
	w.now = func() time.Time { return time.Unix(seconds.Load(), 0) }
	m.mu.Lock()
	m.diskWatch = w
	m.mu.Unlock()
	scan := func() { m.lookForMissing(tt, hash, m.snapshotRecords()[hash]) }
	m.onWriteError(tt, hash, windows.ERROR_IO_DEVICE)
	scan()
	if st, _ := statusOf(m, hash); st.Error != "" || st.Paused {
		t.Fatal("one write failure paused the torrent")
	}
	seconds.Store(107)
	scan() // the single error expired; it must not carry into the next incident
	for i := int64(0); i < 3; i++ {
		seconds.Store(110 + i)
		m.onWriteError(tt, hash, windows.ERROR_IO_DEVICE)
		scan()
	}
	if st, _ := statusOf(m, hash); st.ErrorKind != ErrKindUnavailable || !st.Paused {
		t.Fatalf("not confirmed: %+v", st)
	}
	seconds.Store(120)
	scan()
	waitFor(t, func() bool { st, _ := statusOf(m, hash); return st.Error == "" && !st.Paused && !st.Checking })
}
