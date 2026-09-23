package core

import (
	"os"
	"path/filepath"
	"testing"
)

// newStateOnlyManager builds a Manager with nothing but its state store: recoverMoves runs
// before the engine is touched, so that is all it needs.
func newStateOnlyManager(t *testing.T, dir string) *Manager {
	t.Helper()
	st, err := loadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Manager{state: st}
}

func writeTree(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(root, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func markMove(t *testing.T, m *Manager, hash, save, from, to, dir string) {
	t.Helper()
	err := m.state.with(func(s *state) {
		s.Torrents[hash] = &record{InfoHash: hash, SavePath: save, MoveFrom: from, MoveTo: to, MoveDir: dir}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func recordOf(t *testing.T, m *Manager, hash string) record {
	t.Helper()
	r, ok := m.record(hash)
	if !ok {
		t.Fatalf("record %s is gone", hash)
	}
	return r
}

// The copy was interrupted: the source is intact, so the half-written destination is junk.
func TestRecoverMovesDeletesThePartialCopy(t *testing.T) {
	dir := t.TempDir()
	old, target := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	src, dst := filepath.Join(old, "Show"), filepath.Join(target, "Show")
	writeTree(t, src, "a.mkv", "b.mkv")
	writeTree(t, dst, "a.mkv") // only the first file made it across

	m := newStateOnlyManager(t, dir)
	markMove(t, m, "h", old, src, dst, target)
	m.recoverMoves()

	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("the partial copy was left in the target folder")
	}
	if _, err := os.Stat(filepath.Join(src, "b.mkv")); err != nil {
		t.Errorf("the source data was touched: %v", err)
	}
	r := recordOf(t, m, "h")
	if r.SavePath != old {
		t.Errorf("the torrent should stay in the old folder: got %q, want %q", r.SavePath, old)
	}
	if r.MoveTo != "" || r.MoveFrom != "" || r.MoveDir != "" {
		t.Errorf("the move trail was not cleared: %+v", r)
	}
}

// The rename went through (or the copy finished and the source was removed) but the record
// never learned about it: the destination is the real data now.
func TestRecoverMovesAdoptsTheFinishedMove(t *testing.T) {
	dir := t.TempDir()
	old, target := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	src, dst := filepath.Join(old, "Show"), filepath.Join(target, "Show")
	writeTree(t, dst, "a.mkv", "b.mkv")

	m := newStateOnlyManager(t, dir)
	markMove(t, m, "h", old, src, dst, target)
	m.recoverMoves()

	r := recordOf(t, m, "h")
	if r.SavePath != target {
		t.Errorf("the finished move was not adopted: got %q, want %q", r.SavePath, target)
	}
	if _, err := os.Stat(filepath.Join(dst, "b.mkv")); err != nil {
		t.Errorf("the moved data was removed: %v", err)
	}
	if r.MoveTo != "" {
		t.Errorf("the move trail was not cleared: %+v", r)
	}
}

// Nothing had been downloaded yet: only the folder was going to change.
func TestRecoverMovesWithNothingOnDisk(t *testing.T) {
	dir := t.TempDir()
	old, target := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	m := newStateOnlyManager(t, dir)
	markMove(t, m, "h", old, filepath.Join(old, "Show"), filepath.Join(target, "Show"), target)
	m.recoverMoves()

	r := recordOf(t, m, "h")
	if r.SavePath != old {
		t.Errorf("without data on disk the folder should stay put: got %q", r.SavePath)
	}
	if r.MoveTo != "" {
		t.Errorf("the move trail was not cleared: %+v", r)
	}
}

// A torrent with no move in progress must be left exactly as it is.
func TestRecoverMovesLeavesQuietRecordsAlone(t *testing.T) {
	dir := t.TempDir()
	m := newStateOnlyManager(t, dir)
	err := m.state.with(func(s *state) {
		s.Torrents["h"] = &record{InfoHash: "h", SavePath: filepath.Join(dir, "d"), Label: "Кино"}
	})
	if err != nil {
		t.Fatal(err)
	}
	m.recoverMoves()
	if r := recordOf(t, m, "h"); r.Label != "Кино" || r.SavePath != filepath.Join(dir, "d") {
		t.Errorf("an untouched record was changed: %+v", r)
	}
}
