package core

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// A downloaded season whose subtitles are skipped: they share their pieces with the episodes, so their bytes arrive
// too, but they must not appear in the folder. Wanted again later, they are there at once with the right bytes.
func TestSkippedFileStaysOffTheDisk(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	root := filepath.Join(dirA, "src", "season")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := map[string][]byte{}
	for name, n := range map[string]int{"e01.mkv": 40 << 10, "e01.srt": 5 << 10, "e02.mkv": 40 << 10} {
		b := make([]byte, n)
		_, _ = rand.Read(b)
		content[name] = b
		if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: ib}
	srt := -1
	for i, f := range info.UpvertedFiles() {
		if f.BestPath()[0] == "e01.srt" {
			srt = i
		}
	}

	a := newManager(t, dirA, nil)
	hashA, err := a.AddMetaInfo(&mi, WithSavePath(filepath.Join(dirA, "src")), WithSkipCheck())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(a, hashA); return ok && s.Progress == 1 })

	b := newManager(t, dirB, nil)
	prios := make([]int8, len(info.UpvertedFiles()))
	prios[srt] = PrioSkip
	dl := filepath.Join(dirB, "dl")
	hashB, err := b.AddMetaInfo(&mi, WithSavePath(dl), WithFilePriorities(prios))
	if err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(a.Port())
	waitFor(t, func() bool {
		s, ok := statusOf(b, hashB)
		if ok && s.Progress < 1 && s.Peers == 0 {
			_, _ = b.AddPeers(hashB, []string{addr})
		}
		return ok && s.Progress == 1
	})

	srtPath := filepath.Join(dl, "season", "e01.srt")
	if _, err := os.Stat(srtPath); !os.IsNotExist(err) {
		t.Fatalf("the skipped subtitles must not be on the disk: %v", err)
	}
	for _, name := range []string{"e01.mkv", "e02.mkv"} {
		got, err := os.ReadFile(filepath.Join(dl, "season", name))
		if err != nil || !bytes.Equal(got, content[name]) {
			t.Fatalf("%s is not what was seeded (%d bytes, %v)", name, len(got), err)
		}
	}
	files, _ := b.Files(hashB)
	if files[srt].Done != 0 {
		t.Fatalf("a skipped file kept apart is shown as %d bytes done", files[srt].Done)
	}

	// Wanted again: the bytes kept apart become the file (here the whole file sat in pieces shared with the episodes).
	if err := b.SetFilePriorities(hashB, []int{srt}, PrioNormal); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		got, err := os.ReadFile(srtPath)
		return err == nil && bytes.Equal(got, content["e01.srt"])
	})

	// Removing the torrent takes its part file along.
	if err := b.SetFilePriorities(hashB, []int{srt}, PrioSkip); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(hashB, false); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(b.stateDir, "parts")); len(entries) != 0 {
		t.Fatalf("part files left after removal: %v", entries)
	}
}
