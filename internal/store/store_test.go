package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

func multiFileInfo() *metainfo.Info {
	return &metainfo.Info{
		Name:        "album",
		PieceLength: 8,
		Pieces:      make([]byte, 20*3), // 3 pieces; hashes are irrelevant here
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 10},
			{Path: []string{"sub", "b.bin"}, Length: 12},
		},
	}
}

func TestPieceSpanningFilesAndRelease(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	ti, err := New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{1})
	if err != nil {
		t.Fatal(err)
	}

	// Piece 1 covers bytes 8..16: the last 2 bytes of a.bin and the first 6 of b.bin.
	p := ti.Piece(info.Piece(1))
	data := []byte("ABCDEFGH")
	if _, err := p.WriteAt(data, 0); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 8)
	if _, err := p.ReadAt(got, 0); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back %q, err %v", got, err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "album", "a.bin"))
	b, _ := os.ReadFile(filepath.Join(dir, "album", "sub", "b.bin"))
	if !bytes.Equal(a[8:10], []byte("AB")) || !bytes.Equal(b[:6], []byte("CDEFGH")) {
		t.Fatalf("data split wrongly: a=%q b=%q", a, b)
	}

	// Reading data that was never written must fail, not return zeros.
	if _, err := ti.Piece(info.Piece(2)).ReadAt(make([]byte, 4), 0); err == nil {
		t.Fatal("expected an error reading a missing file range")
	}

	// A handle must not stay open: after the idle period the file can be deleted.
	time.Sleep(idleClose + 1500*time.Millisecond)
	if err := os.RemoveAll(filepath.Join(dir, "album")); err != nil {
		t.Fatalf("file still locked after idle period: %v", err)
	}
}

func TestCloseReleasesFilesImmediately(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	ti, _ := New(dir, nil).OpenTorrent(context.Background(), info, metainfo.Hash{2})
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("12345678"), 0); err != nil {
		t.Fatal(err)
	}
	if err := ti.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "album")); err != nil {
		t.Fatalf("Close must release all handles: %v", err)
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	info := multiFileInfo()
	info.Files[0].Path = []string{"..", "..", "evil.bin"}
	if _, err := Paths(t.TempDir(), info); err == nil {
		t.Fatal("path escaping the download dir must be rejected")
	}
}

// Piece records in the completion database outlive the files. A stale "complete" record
// must not make an empty or missing file look like finished data.
func TestStaleCompletionIgnoredWhenFilesAreGone(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	ih := metainfo.Hash{3}
	st := New(dir, nil) // one shared completion database across both opens

	ti, _ := st.OpenTorrent(context.Background(), info, ih)
	p := ti.Piece(info.Piece(0))
	if _, err := p.WriteAt([]byte("12345678"), 0); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	// Reopened with the file present: still complete.
	_ = ti.Close()
	ti, _ = st.OpenTorrent(context.Background(), info, ih)
	if c := ti.Piece(info.Piece(0)).Completion(); !c.Ok || !c.Complete {
		t.Fatalf("piece with its file present must stay complete: %+v", c)
	}

	// Files deleted (remove with data): the record is stale now.
	_ = ti.Close()
	if err := os.RemoveAll(filepath.Join(dir, "album")); err != nil {
		t.Fatal(err)
	}
	ti, _ = st.OpenTorrent(context.Background(), info, ih)
	defer ti.Close()
	if c := ti.Piece(info.Piece(0)).Completion(); !c.Ok || c.Complete {
		t.Fatalf("stale record must read as known-incomplete: %+v", c)
	}
}

// After the files are recreated as zeros (preallocation) a restart must not resurrect the
// old "complete" record.
func TestHealedRecordDoesNotComeBackWithPreallocatedZeros(t *testing.T) {
	dir := t.TempDir()
	info := multiFileInfo()
	ih := metainfo.Hash{4}
	st := New(dir, nil)

	ti, _ := st.OpenTorrent(context.Background(), info, ih)
	p := ti.Piece(info.Piece(0))
	_, _ = p.WriteAt([]byte("12345678"), 0)
	_ = p.MarkComplete()
	_ = ti.Close()
	if err := os.RemoveAll(filepath.Join(dir, "album")); err != nil {
		t.Fatal(err)
	}

	ti, _ = st.OpenTorrent(context.Background(), info, ih) // heals the record
	_ = ti.Close()
	// Preallocation recreates the files, full of zeros.
	if err := os.MkdirAll(filepath.Join(dir, "album", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "album", "a.bin"), make([]byte, 10), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "album", "sub", "b.bin"), make([]byte, 12), 0o644)

	ti, _ = st.OpenTorrent(context.Background(), info, ih)
	defer ti.Close()
	if c := ti.Piece(info.Piece(0)).Completion(); c.Complete {
		t.Fatalf("zero-filled file must not read as a finished piece: %+v", c)
	}
}
