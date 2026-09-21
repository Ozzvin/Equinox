package core

import (
	"os"
	"testing"
)

// TestWriteSampleTorrent is a fixture generator, not a check: with TORRENT_SAMPLE_OUT set
// it writes a two-file .torrent there (TORRENT_SAMPLE_NAME picks the root folder name, so
// several different samples can be made). Used to try the UI by hand.
func TestWriteSampleTorrent(t *testing.T) {
	out := os.Getenv("TORRENT_SAMPLE_OUT")
	if out == "" {
		t.Skip("set TORRENT_SAMPLE_OUT to write a sample torrent")
	}
	name := os.Getenv("TORRENT_SAMPLE_NAME")
	if name == "" {
		name = "pack"
	}
	mi := namedTwoFileTorrent(t, t.TempDir(), name)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := mi.Write(f); err != nil {
		t.Fatal(err)
	}
}
