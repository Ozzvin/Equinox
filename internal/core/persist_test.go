package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func loadRaw(t *testing.T, path string) state {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("state.json on disk is not valid JSON: %v", err)
	}
	return s
}

// touch must not reach the disk on its own: that is the whole reason it exists.
func TestTouchDefersTheWriteUntilFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.with(func(s *state) { s.Torrents["h"] = &record{InfoHash: "h"} }); err != nil {
		t.Fatal(err)
	}
	st.touch(func(s *state) { s.Torrents["h"].Downloaded = 4242 })
	if got := loadRaw(t, path).Torrents["h"].Downloaded; got != 0 {
		t.Errorf("touch wrote to the disk straight away: got %d, want 0", got)
	}
	if err := st.flush(); err != nil {
		t.Fatal(err)
	}
	if got := loadRaw(t, path).Torrents["h"].Downloaded; got != 4242 {
		t.Errorf("flush did not write the counter: got %d, want 4242", got)
	}
}

// with is for changes the user would notice losing, so it still writes at once.
func TestWithWritesImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.with(func(s *state) { s.Torrents["h"] = &record{InfoHash: "h", Label: "Кино"} }); err != nil {
		t.Fatal(err)
	}
	if got := loadRaw(t, path).Torrents["h"].Label; got != "Кино" {
		t.Errorf("with did not persist: got %q", got)
	}
}

func TestFlushIsCheapWhenNothingChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.with(func(s *state) { s.Torrents["h"] = &record{InfoHash: "h"} }); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if err := st.flush(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(fi.ModTime()) {
		t.Error("flush rewrote the file although nothing had been touched")
	}
}

// A state.json torn by a power cut must not cost the whole torrent list.
func TestLoadStateFallsBackToTheBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.with(func(s *state) { s.Torrents["h"] = &record{InfoHash: "h", Label: "первая"} }); err != nil {
		t.Fatal(err)
	}
	if err := st.with(func(s *state) { s.Torrents["h"].Label = "вторая" }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"torrents":`), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := loadState(path)
	if err != nil {
		t.Fatalf("a torn state.json must not stop the daemon: %v", err)
	}
	r := again.st.Torrents["h"]
	if r == nil || r.Label != "первая" {
		t.Fatalf("the backup was not used: %+v", r)
	}
}
