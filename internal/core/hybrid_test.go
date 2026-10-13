package core

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/merkle"
	"github.com/anacrolix/torrent/metainfo"
)

// hybridTorrent makes a hybrid (v1 + v2, BEP 52) torrent of three files whose sizes are not multiples of the piece:
// in v2 every file starts on a piece boundary, and the v1 part has padding files (BEP 47) to the same layout. The
// files are written under dir/src/hybrid.
func hybridTorrent(t *testing.T, dir string) (*metainfo.MetaInfo, map[string][]byte) {
	t.Helper()
	const pieceLen = 16 << 10
	root := filepath.Join(dir, "src", "hybrid")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"a.mkv", "b.srt", "c.mkv"} // the order of the v2 file tree (sorted)
	sizes := map[string]int{"a.mkv": 40 << 10, "b.srt": 5 << 10, "c.mkv": 30 << 10}
	content := map[string][]byte{}
	tree := metainfo.FileTree{Dir: map[string]metainfo.FileTree{}}
	layers := map[string]string{}
	var v1 []metainfo.FileInfo
	var flat []byte // the v1 layout: the files with their padding
	for k, name := range names {
		b := make([]byte, sizes[name])
		_, _ = rand.Read(b)
		content[name] = b
		if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
		h := merkle.NewHash()
		h.Write(b)
		fileRoot := h.Sum(nil)
		if len(b) > pieceLen {
			var layer []byte
			for off := 0; off < len(b); off += pieceLen {
				ph := merkle.NewHash()
				ph.Write(b[off:min(off+pieceLen, len(b))])
				layer = ph.SumMinLength(layer, pieceLen)
			}
			layers[string(fileRoot)] = string(layer)
		}
		tree.Dir[name] = metainfo.FileTree{File: metainfo.FileTreeFile{Length: int64(len(b)), PiecesRoot: string(fileRoot)}}
		v1 = append(v1, metainfo.FileInfo{Length: int64(len(b)), Path: []string{name}})
		flat = append(flat, b...)
		if pad := (pieceLen - len(b)%pieceLen) % pieceLen; pad > 0 && k < len(names)-1 {
			v1 = append(v1, metainfo.FileInfo{Length: int64(pad), Path: []string{".pad", strconv.Itoa(pad)}, ExtendedFileAttrs: metainfo.ExtendedFileAttrs{Attr: "p"}})
			flat = append(flat, make([]byte, pad)...)
		}
	}
	var pieces []byte
	for off := 0; off < len(flat); off += pieceLen {
		s := sha1.Sum(flat[off:min(off+pieceLen, len(flat))])
		pieces = append(pieces, s[:]...)
	}
	if err := metainfo.ValidatePieceLayers(layers, &tree, pieceLen); err != nil {
		t.Fatalf("the test torrent is not a valid v2 one: %v", err)
	}
	// the file tree is put in by hand: inside Info (a value, not a pointer) the encoder does not use its own encoding
	v1b, err := bencode.Marshal(metainfo.Info{Name: "hybrid", PieceLength: pieceLen, Pieces: pieces, Files: v1, MetaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	var dict map[string]bencode.Bytes
	if err := bencode.Unmarshal(v1b, &dict); err != nil {
		t.Fatal(err)
	}
	treeBytes, err := tree.MarshalBencode()
	if err != nil {
		t.Fatal(err)
	}
	dict["file tree"] = treeBytes
	ib, err := bencode.Marshal(dict)
	if err != nil {
		t.Fatal(err)
	}
	var check metainfo.Info
	if err := bencode.Unmarshal(ib, &check); err != nil || !check.HasV2() || !check.HasV1() || len(check.UpvertedFiles()) != 3 {
		t.Fatalf("the test torrent must be a hybrid of three files: %v", err)
	}
	return &metainfo.MetaInfo{InfoBytes: ib, PieceLayers: layers}, content
}

// A hybrid torrent of several files is written to the disk at the places of its files: in v2 a file starts on a piece
// boundary, not right after the one before it. Found by reading the code: the storage put every file right after the
// one before it, so from the second file on the bytes went to the wrong places (and the hashes still matched, as it
// read them back from the same wrong places).
func TestHybridTorrentFilesAreWrittenWhereTheyBelong(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	mi, content := hybridTorrent(t, dirA)

	a := newManager(t, dirA, nil)
	hashA, err := a.AddMetaInfo(mi, WithSavePath(filepath.Join(dirA, "src")))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(a, hashA); return ok && s.Progress == 1 && !s.Checking })

	b := newManager(t, dirB, nil)
	dl := filepath.Join(dirB, "dl")
	hashB, err := b.AddMetaInfo(mi, WithSavePath(dl))
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
	for name, want := range content {
		got, err := os.ReadFile(filepath.Join(dl, "hybrid", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s on the disk is not what was seeded (%d bytes, %v)", name, len(got), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dl, "hybrid", ".pad")); !os.IsNotExist(err) {
		t.Fatalf("no padding files on the disk: %v", err)
	}
}
