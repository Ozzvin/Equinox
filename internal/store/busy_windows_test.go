package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/sys/windows"
)

// holdExclusively opens path the way an antivirus scanning it does: shared with nobody, so any other open fails with
// ERROR_SHARING_VIOLATION until the returned function closes it.
func holdExclusively(t *testing.T, path string) (let func()) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	var once bool
	let = func() {
		if !once {
			once = true
			windows.CloseHandle(h)
		}
	}
	t.Cleanup(let)
	return let
}

// waitBusy makes the tries at a busy file short for a test: n tries, d apart.
func waitBusy(t *testing.T, n int, d time.Duration) {
	old := busyWaits
	busyWaits = make([]time.Duration, n)
	for i := range busyWaits {
		busyWaits[i] = d
	}
	t.Cleanup(func() { busyWaits = old })
}

// A file another program holds for a moment (an antivirus scanning a fresh .dll) is written once it lets go: the
// engine never hears of the failure, so the torrent is not stopped with "write error".
func TestWriteWaitsForAFileHeldForAMoment(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	ti, err := New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{7})
	if err != nil {
		t.Fatal(err)
	}
	defer ti.Close()
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("12345678"), 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "album", "a.bin")
	ti.Close() // its handle, as the janitor would close it, so the next write opens the file again
	ti, err = New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{7})
	if err != nil {
		t.Fatal(err)
	}
	defer ti.Close()

	waitBusy(t, 40, 50*time.Millisecond) // two seconds in all
	let := holdExclusively(t, path)
	time.AfterFunc(500*time.Millisecond, let)
	start := time.Now()
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("ABCDEFGH"), 0); err != nil {
		t.Fatalf("the write failed although the file was let go within the wait: %v", err)
	}
	if d := time.Since(start); d < 400*time.Millisecond {
		t.Fatalf("the write went through in %v while the file was held", d)
	}
	ti.Close()
	if b, _ := os.ReadFile(path); string(b[:8]) != "ABCDEFGH" {
		t.Fatalf("on the disk: %q", b)
	}
}

// A file held for longer than the wait fails as before: then the torrent stops, and the error says why.
func TestWriteGivesUpOnAFileHeldTooLong(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	ti, err := New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("12345678"), 0); err != nil {
		t.Fatal(err)
	}
	ti.Close()
	ti, err = New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{8})
	if err != nil {
		t.Fatal(err)
	}
	defer ti.Close()

	waitBusy(t, 4, 50*time.Millisecond)
	holdExclusively(t, filepath.Join(dir, "album", "a.bin"))
	_, err = ti.Piece(info.Piece(0)).WriteAt([]byte("ABCDEFGH"), 0)
	if err == nil || !busyFile(err) {
		t.Fatalf("want the sharing violation after the wait, got %v", err)
	}
}

// A failure that waiting does not mend (here a folder where the file should be) is not waited for.
func TestWriteDoesNotWaitOnARealFailure(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	if err := os.MkdirAll(filepath.Join(dir, "album", "a.bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	ti, err := New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{9})
	if err != nil {
		t.Fatal(err)
	}
	defer ti.Close()

	waitBusy(t, 10, time.Second)
	start := time.Now()
	_, err = ti.Piece(info.Piece(0)).WriteAt([]byte("ABCDEFGH"), 0)
	if err == nil || busyFile(err) {
		t.Fatalf("want the failure to open a folder as a file, got %v", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("a real failure was waited on for %v", d)
	}
}

// Closing the torrent (removed, moved) does not wait out a busy file.
func TestCloseEndsTheWaitForABusyFile(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	ti, err := New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("12345678"), 0); err != nil {
		t.Fatal(err)
	}
	ti.Close()
	ti, err = New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{10})
	if err != nil {
		t.Fatal(err)
	}

	waitBusy(t, 10, time.Second)
	holdExclusively(t, filepath.Join(dir, "album", "a.bin"))
	time.AfterFunc(300*time.Millisecond, func() { ti.Close() })
	start := time.Now()
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("ABCDEFGH"), 0); err == nil {
		t.Fatal("a write to a closed torrent's busy file went through")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("closing the torrent did not end the wait: %v", d)
	}
}
