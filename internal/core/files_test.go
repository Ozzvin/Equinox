package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// twoFileTorrent builds a torrent of a.bin (32 KiB) and b.bin (48 KiB) and returns it
// together with the source directory holding the payload.
func twoFileTorrent(t *testing.T, dir string) *metainfo.MetaInfo {
	t.Helper()
	return namedTwoFileTorrent(t, dir, "pack")
}

// namedTwoFileTorrent is twoFileTorrent with a chosen root folder name (so the info hash differs).
func namedTwoFileTorrent(t *testing.T, dir, name string) *metainfo.MetaInfo {
	t.Helper()
	root := filepath.Join(dir, "src", name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, n := range map[string]int{"a.bin": 32 << 10, "b.bin": 48 << 10} {
		if err := os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte{9}, n), 0o644); err != nil {
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
	return &metainfo.MetaInfo{InfoBytes: b}
}

func TestSkippedFilesCostNothingAndProgressUsesSelection(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	mi := twoFileTorrent(t, dir)

	// Added paused: nothing may be allocated yet.
	hash, err := m.AddMetaInfo(mi, WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(m.cfg.Get().DataDir, "pack", "a.bin")
	b := filepath.Join(m.cfg.Get().DataDir, "pack", "b.bin")
	waitFor(t, func() bool { l := m.List(); return len(l) == 1 && l[0].HasMeta })
	if exists(a) || exists(b) {
		t.Fatal("a torrent added paused must not reserve disk space")
	}

	// Skip a.bin, then resume: only b.bin is allocated.
	if err := m.SetFilePriorities(hash, []int{0}, PrioSkip); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPaused(hash, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { fi, err := os.Stat(b); return err == nil && fi.Size() == 48<<10 })
	if exists(a) {
		t.Fatal("skipped file must not be allocated")
	}

	l := m.List()[0]
	if l.TotalSize != 80<<10 || l.Size != 48<<10 {
		t.Fatalf("size must cover the selection only: total=%d selected=%d", l.TotalSize, l.Size)
	}
	files, _ := m.Files(hash)
	if files[0].Priority != "skip" || files[1].Priority != "normal" {
		t.Fatalf("priorities not reported: %+v", files)
	}

	// Un-skipping allocates the file and survives a restart.
	if err := m.SetFilePriorities(hash, []int{0}, PrioHigh); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(a) })
	if got := m.filePrios(hash); len(got) != 2 || got[0] != PrioHigh {
		t.Fatalf("priorities not persisted: %v", got)
	}

	if err := m.SetFilePriorities(hash, []int{5}, PrioSkip); err == nil {
		t.Fatal("out-of-range file index must be rejected")
	}
}
