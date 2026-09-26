package core

import (
	"reflect"
	"testing"
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

// The trackers of a torrent, each once, are in its status: from a magnet link before the metadata is there, and the ones
// the person added later.
func TestListShowsTheTrackers(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	defer m.Close()
	hash, err := m.AddMagnet("magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"+
		"&tr=http%3A%2F%2Fbt.t-ru.org%2Fann%3Fmagnet&tr=http%3A%2F%2Fbt2.t-ru.org%2Fann&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce", WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	find := func() Status {
		for _, st := range m.List() {
			if st.Hash == hash {
				return st
			}
		}
		t.Fatal("the torrent is not in the list")
		return Status{}
	}
	if got, want := find().Trackers, []string{"opentrackr", "rutracker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trackers of the magnet: %v, want %v", got, want)
	}
	if err := m.AddTracker(hash, "http://tapochek.net/announce.php"); err != nil {
		t.Fatal(err)
	}
	if got, want := find().Trackers, []string{"opentrackr", "rutracker", "tapochek"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trackers after adding one: %v, want %v", got, want)
	}
}
