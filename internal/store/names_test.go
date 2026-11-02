package store

import (
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// Only construct paths: never create test files in the owner's drive root.
func TestPathsAtFilesystemRoot(t *testing.T) {
	bases := []string{string(filepath.Separator)}
	if runtime.GOOS == "windows" {
		bases = []string{`F:\`, `C:\`, `\\server\share\`}
	}
	for _, base := range bases {
		for _, multi := range []bool{false, true} {
			info := &metainfo.Info{Name: "movie.mkv", Length: 10}
			want := []string{filepath.Join(base, "movie.mkv")}
			if multi {
				info = multiFileInfo()
				want = []string{filepath.Join(base, "album", "a.bin"), filepath.Join(base, "album", "sub", "b.bin")}
			}
			got, err := Paths(base, info)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("base %q multi %v: %v, %v; want %v", base, multi, got, err, want)
			}
		}
	}
}

func TestPathsStillRejectEscapesAndDestinationItself(t *testing.T) {
	base := t.TempDir()
	for _, path := range [][]string{{"..", "..", "escape.bin"}, {"..", "..", filepath.Base(base) + "-other", "escape.bin"}, {".."}} {
		info := multiFileInfo()
		info.Files[0].Path = path
		if got, err := Paths(base, info); err == nil {
			t.Fatalf("unsafe path %v accepted: %v", path, got)
		}
	}
}

func TestSingleFileHybridDoesNotRepeatName(t *testing.T) {
	info := &metainfo.Info{Name: "movie.mkv", Length: 10, Pieces: make([]byte, 20), MetaVersion: 2, PieceLength: 16384,
		FileTree: metainfo.FileTree{Dir: map[string]metainfo.FileTree{
			"movie.mkv": {File: metainfo.FileTreeFile{Length: 10}},
		}}}
	bases := []string{t.TempDir()}
	if runtime.GOOS == "windows" {
		bases = append(bases, `F:\`)
	}
	for _, base := range bases {
		got, err := Paths(base, info)
		if err != nil || !slices.Equal(got, []string{filepath.Join(base, "movie.mkv")}) {
			t.Fatalf("base %q: %v, %v", base, got, err)
		}
	}
}

// Names Windows cannot have are mended the way qBittorrent does it.
func TestWindowsName(t *testing.T) {
	for in, want := range map[string]string{
		"video:x.mkv": "video_x.mkv", `a<b>c"d|e?f*g`: "a_b_c_d_e_f_g", "tab\there": "tab_here", `sub\dir`: "sub_dir",
		"Album.": "Album", "name . ": "name", "...": "_",
		"CON": "CON_", "nul.txt": "nul_.txt", "com1.tar.gz": "com1_.tar.gz", "CONSOLE.txt": "CONSOLE.txt", "LPT10": "LPT10",
		"Фильм (2024).mkv": "Фильм (2024).mkv",
	} {
		if got := windowsName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// On Windows the paths of a torrent's files come mended, the name of its folder too.
func TestPathsAreMendedOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("names stay as they are elsewhere")
	}
	info := &metainfo.Info{Name: "Show:", PieceLength: 16 << 10, Files: []metainfo.FileInfo{
		{Path: []string{"e01:cut.mkv"}, Length: 1}, {Path: []string{"AUX", "x."}, Length: 1},
	}}
	base := t.TempDir()
	got, err := Paths(base, info)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(base, "Show_", "e01_cut.mkv"), filepath.Join(base, "Show_", "AUX_", "x")}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d: %s, want %s", i, got[i], want[i])
		}
	}
}
