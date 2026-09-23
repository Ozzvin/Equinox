package atomicfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func parseJSON(v any) func([]byte) error {
	return func(b []byte) error { return json.Unmarshal(b, v) }
}

func TestWriteThenRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte(`{"n":1}`), 0o644, true); err != nil {
		t.Fatal(err)
	}
	var got struct{ N int }
	ok, fromBackup, err := ReadWithBackup(path, parseJSON(&got))
	if err != nil || !ok || fromBackup || got.N != 1 {
		t.Fatalf("ok=%v fromBackup=%v n=%d err=%v", ok, fromBackup, got.N, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	var got struct{ N int }
	ok, _, err := ReadWithBackup(path, parseJSON(&got))
	if err != nil || ok {
		t.Fatalf("a first run should read nothing without an error: ok=%v err=%v", ok, err)
	}
}

// The point of the backup: the newest file is unreadable, the one before it is not.
func TestFallsBackToBackupWhenMainIsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte(`{"n":1}`), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte(`{"n":2}`), 0o644, true); err != nil {
		t.Fatal(err)
	}
	// A torn write is what a power cut leaves behind.
	if err := os.WriteFile(path, []byte(`{"n":`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got struct{ N int }
	ok, fromBackup, err := ReadWithBackup(path, parseJSON(&got))
	if err != nil || !ok || !fromBackup {
		t.Fatalf("ok=%v fromBackup=%v err=%v", ok, fromBackup, err)
	}
	if got.N != 1 {
		t.Errorf("backup holds the save before last: got n=%d, want 1", got.N)
	}
}

func TestCorruptWithNoBackupReportsTheParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got struct{ N int }
	ok, _, err := ReadWithBackup(path, parseJSON(&got))
	if err == nil || ok {
		t.Fatalf("want the parse error, got ok=%v err=%v", ok, err)
	}
}

func TestNoBackupWhenNotAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	for _, body := range []string{`{"n":1}`, `{"n":2}`} {
		if err := Write(path, []byte(body), 0o644, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("a backup was written although keepBackup was false")
	}
}

// A filesystem without hard links (a portable install on a FAT32 stick) must still get a
// backup, or the recovery above quietly never happens there.
func TestBackupFallsBackToACopyWithoutHardLinks(t *testing.T) {
	old := linkFile
	linkFile = func(string, string) error { return errors.New("no hard links here") }
	defer func() { linkFile = old }()

	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte(`{"n":1}`), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte(`{"n":2}`), 0o644, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no backup was made without hard links: %v", err)
	}
	if string(b) != `{"n":1}` {
		t.Errorf("backup holds %q, want the save before last", b)
	}
}
