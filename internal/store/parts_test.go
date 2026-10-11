package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// A season and its subtitles: pieces of 8 bytes, the small file in the middle shares piece 1 with a.bin and piece 2
// with c.bin, and has no piece of its own.
func seasonInfo() *metainfo.Info {
	return &metainfo.Info{
		Name:        "season",
		PieceLength: 8,
		Pieces:      make([]byte, 20*4), // 4 pieces of 8 bytes: 0..8 a, 8..12 a, 12..18 subs, 18..32 c
		Files: []metainfo.FileInfo{
			{Path: []string{"a.mkv"}, Length: 12},
			{Path: []string{"subs.srt"}, Length: 6},
			{Path: []string{"c.mkv"}, Length: 14},
		},
	}
}

type partsEnv struct {
	t     *testing.T
	dir   string
	parts string
	skip  []bool
	s     *Storage
	info  *metainfo.Info
	ih    metainfo.Hash
	ti    storage.TorrentImpl
}

func newPartsEnv(t *testing.T, skip ...bool) *partsEnv {
	e := &partsEnv{t: t, dir: t.TempDir(), skip: skip, info: seasonInfo(), ih: metainfo.Hash{7}}
	e.parts = filepath.Join(e.dir, "state", "parts")
	e.s = New(filepath.Join(e.dir, "dl"), nil)
	e.s.UseParts(e.parts, func(metainfo.Hash) []bool { return e.skip })
	e.open()
	t.Cleanup(func() { e.ti.Close() })
	return e
}

func (e *partsEnv) open() {
	ti, err := e.s.OpenTorrent(context.Background(), e.info, e.ih)
	if err != nil {
		e.t.Fatal(err)
	}
	e.ti = ti
}

func (e *partsEnv) path(name string) string { return filepath.Join(e.dir, "dl", "season", name) }

func (e *partsEnv) write(piece int, data string) {
	if _, err := e.ti.Piece(e.info.Piece(piece)).WriteAt([]byte(data), 0); err != nil {
		e.t.Fatalf("writing piece %d: %v", piece, err)
	}
}

func (e *partsEnv) read(piece int) string {
	b := make([]byte, e.info.Piece(piece).Length())
	if _, err := e.ti.Piece(e.info.Piece(piece)).ReadAt(b, 0); err != nil {
		e.t.Fatalf("reading piece %d: %v", piece, err)
	}
	return string(b)
}

func TestSkippedFileGoesToThePartFile(t *testing.T) {
	e := newPartsEnv(t, false, true, false)
	e.write(1, "aaaaSSSS") // the end of a.mkv, then the start of the subtitles
	e.write(2, "SSccccccc"[:8])
	if _, err := os.Stat(e.path("subs.srt")); !os.IsNotExist(err) {
		t.Fatalf("the skipped file must not appear on the disk: %v", err)
	}
	if got := e.read(1); got != "aaaaSSSS" {
		t.Fatalf("piece 1 read back %q", got)
	}
	if got := e.read(2); got != "SScccccc" {
		t.Fatalf("piece 2 read back %q", got)
	}
	if a, _ := os.ReadFile(e.path("a.mkv")); !bytes.Equal(a[8:12], []byte("aaaa")) {
		t.Fatalf("a.mkv got %q", a)
	}
	if kept := e.s.InParts(e.ih); len(kept) != 3 || !kept[1] || kept[0] || kept[2] {
		t.Fatalf("InParts: %v", kept)
	}

	// After a restart the bytes are still there and the pieces count as present.
	e.ti.Close()
	e.open()
	if got := e.read(1); got != "aaaaSSSS" {
		t.Fatalf("after reopening, piece 1 read back %q", got)
	}
	ts := e.s.reg[e.ih]
	if !ts.present(8, 8) || !ts.present(16, 8) {
		t.Fatal("pieces with their bytes in the part file must count as present")
	}
	if ts.present(24, 8) {
		t.Fatal("piece 3 was never written")
	}
}

func TestWantedAgainMovesTheBytesIntoTheFile(t *testing.T) {
	e := newPartsEnv(t, false, true, false)
	e.write(1, "aaaaSSSS")
	e.write(2, "SScccccc")
	e.skip = []bool{false, false, false}
	if err := e.s.FilesChanged(e.ih); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(e.path("subs.srt"))
	if err != nil || string(b) != "SSSSSS" {
		t.Fatalf("subs.srt = %q, %v", b, err)
	}
	if e.s.InParts(e.ih) != nil {
		t.Fatal("nothing should be kept in the part file any more")
	}
	if _, err := os.Stat(filepath.Join(e.parts, e.ih.HexString()+".parts")); !os.IsNotExist(err) {
		t.Fatalf("an empty part file must be deleted: %v", err)
	}
	if got := e.read(1); got != "aaaaSSSS" {
		t.Fatalf("piece 1 read back %q from the files", got)
	}
}

func TestWantedAgainWhileClosed(t *testing.T) {
	e := newPartsEnv(t, false, true, false)
	e.write(1, "aaaaSSSS")
	e.ti.Close()
	e.skip = []bool{false, false, false}
	e.open()
	b, err := os.ReadFile(e.path("subs.srt"))
	if err != nil || string(b[:4]) != "SSSS" {
		t.Fatalf("subs.srt = %q, %v", b, err)
	}
}

func TestSkippedFileOnTheDiskStaysThere(t *testing.T) {
	e := newPartsEnv(t, false, false, false)
	e.write(1, "aaaaSSSS") // downloaded while it was wanted
	e.skip = []bool{false, true, false}
	if err := e.s.FilesChanged(e.ih); err != nil {
		t.Fatal(err)
	}
	e.write(2, "SScccccc")
	b, _ := os.ReadFile(e.path("subs.srt"))
	if string(b) != "SSSSSS" {
		t.Fatalf("a skipped file already on the disk must go on being used: %q", b)
	}
	if e.s.HasParts(e.ih) {
		t.Fatal("no part file was needed")
	}

	// Deleted by the user: from then on its bytes go to the part file.
	e.s.CloseFiles(e.ih, []string{e.path("subs.srt")})
	if err := os.Remove(e.path("subs.srt")); err != nil {
		t.Fatal(err)
	}
	e.write(2, "SScccccc")
	if _, err := os.Stat(e.path("subs.srt")); !os.IsNotExist(err) {
		t.Fatalf("a deleted skipped file must not come back: %v", err)
	}
	if !e.s.HasParts(e.ih) {
		t.Fatal("the bytes should be in the part file")
	}
}

func TestDropPartsDeletesThePartFile(t *testing.T) {
	e := newPartsEnv(t, false, true, false)
	e.write(1, "aaaaSSSS")
	if !e.s.HasParts(e.ih) {
		t.Fatal("expected a part file")
	}
	e.ti.Close()
	e.s.DropParts(e.ih)
	entries, _ := os.ReadDir(e.parts)
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}
