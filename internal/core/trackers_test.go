package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestTrackerName(t *testing.T) {
	for announce, want := range map[string]string{
		"http://bt.t-ru.org/ann?magnet":              "rutracker",
		"http://bt2.t-ru.org/ann?magnet":             "rutracker",
		"http://retracker.local/announce":            "retracker",
		"http://tapochek.net/announce.php?uk=SECRET": "tapochek",
		"udp://tracker.opentrackr.org:1337/announce": "opentrackr",
		"https://tr.kinozal.tv/announce":             "kinozal",
		"udp://tracker.torrent.eu.org:451/announce":  "torrent",
		"http://www.example.co.uk/announce":          "example",
		"udp://93.184.216.34:6969/announce":          "93.184.216.34",
		"http://[2606:2800:220:1::1]:80/announce":    "2606:2800:220:1::1",
		"  udp://Open.Demonii.com:1337/announce  ":   "demonii",
		"http://localhost:8080/announce":             "localhost",
		"":                                           "",
		"not an address":                             "",
		"udp:///nohost":                              "",
	} {
		if got := TrackerName(announce); got != want {
			t.Errorf("TrackerName(%q) = %q, want %q", announce, got, want)
		}
	}
}

// The trackers here have a scheme the engine does not know ("test://"): it starts no announcer, so the test asks nothing of the
// network (and does not meet the race the library has when it starts announcers for torrents added one after another).
// A torrent belongs to its main tracker: the "announce" of its file, or, with none (a magnet link), the first of its list.
func TestListShowsTheMainTracker(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	defer m.Close()
	tracker := func(hash string) string {
		for _, st := range m.List() {
			if st.Hash == hash {
				return st.Tracker
			}
		}
		t.Fatal("the torrent is not in the list")
		return ""
	}
	// a magnet link: the first tracker, whatever follows
	hash, err := m.AddMagnet("magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"+
		"&tr=test%3A%2F%2Fbt.t-ru.org%2Fann%3Fmagnet&tr=test%3A%2F%2Ftapochek.net%2Fannounce.php&tr=test%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce", WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	if got := tracker(hash); got != "rutracker" {
		t.Fatalf("the main tracker of the magnet: %q", got)
	}
	// a file: its "announce", even when the list names other trackers first
	mi, err := metainfo.LoadFromFile(makeTorrent(t, dir, "film.bin", 40<<10))
	if err != nil {
		t.Fatal(err)
	}
	mi.Announce = "test://tapochek.net/announce.php?uk=SECRET"
	mi.AnnounceList = [][]string{{"test://tracker.opentrackr.org:1337/announce"}, {"test://tapochek.net/announce.php?uk=SECRET"}}
	withAnnounce := filepath.Join(dir, "with-announce.torrent")
	f, _ := os.Create(withAnnounce)
	if err := mi.Write(f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	hash, err = m.AddFile(withAnnounce, WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	if got := tracker(hash); got != "tapochek" {
		t.Fatalf("the main tracker of the file: %q, want the one of its announce", got)
	}
}
