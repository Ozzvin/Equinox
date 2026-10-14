package store

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

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
