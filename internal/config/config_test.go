package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefaultsOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	st, err := Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Get().ListenPort != 51413 {
		t.Errorf("defaults were not applied: port %d", st.Get().ListenPort)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the settings file was not written: %v", err)
	}
}

// A new install does not copy .torrent files anywhere until asked to, and only the first start
// counts as fresh: an existing settings file keeps what it says, including a copy folder chosen
// back when copies were on by default.
func TestFreshStartDefaultsAndExistingFilesKeepTheirCopyFolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	st, err := Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Fresh() {
		t.Error("no settings file yet: this is a fresh start")
	}
	if st.Get().TorrentCopyDir != "" {
		t.Errorf("copies of .torrent files must be off by default, got %q", st.Get().TorrentCopyDir)
	}

	kept := filepath.Join(dir, "old-copies")
	if err := st.Update(func(s *Settings) { s.TorrentCopyDir = kept }); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Fresh() {
		t.Error("the settings file exists now: the second start is not fresh")
	}
	if again.Get().TorrentCopyDir != kept {
		t.Errorf("an existing copy folder must survive, got %q", again.Get().TorrentCopyDir)
	}
}

// A settings file from an older build knows nothing about fields added since; those must come
// up at their defaults, not at Go's zero values (a zero SequentialWindow would break streaming).
func TestLoadKeepsDefaultsForAbsentFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"downLimitKBps": 500}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Get()
	if s.DownLimitKBps != 500 {
		t.Errorf("the stored value was lost: %d", s.DownLimitKBps)
	}
	if s.SequentialWindow != 16 || s.EdgePieces != 4 || s.MaxConcurrentChecks != 2 {
		t.Errorf("absent fields did not fall back to the defaults: %+v", s)
	}
}

// The same protection state.json gets: a settings file torn by a power cut must not stop the
// daemon when the copy from the save before it is readable.
func TestLoadFallsBackToTheBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	st, err := Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *Settings) { s.DownLimitKBps = 111 }); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *Settings) { s.DownLimitKBps = 222 }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"downLimitKBps":`), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path, dir)
	if err != nil {
		t.Fatalf("a torn settings.json must not stop the daemon: %v", err)
	}
	if got := again.Get().DownLimitKBps; got != 111 {
		t.Errorf("the backup holds the save before last: got %d, want 111", got)
	}
}

func TestLoadReportsACorruptFileWithNoBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`not json at all`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, dir); err == nil {
		t.Fatal("a corrupt settings file with no backup should be reported, not ignored")
	}
}

func TestUpdatePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	st, err := Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *Settings) { s.SpeedUnit = "bits" }); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get().SpeedUnit != "bits" {
		t.Errorf("the change did not survive a reload: %q", again.Get().SpeedUnit)
	}
}

func TestValidLabelColor(t *testing.T) {
	if !ValidLabelColor("violet") {
		t.Error("a colour from the palette was rejected")
	}
	if ValidLabelColor("green") {
		t.Error("green is a state colour and must not be a label colour")
	}
}
