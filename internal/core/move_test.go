package core

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

// seed puts a finished payload where the manager will look for it, so the torrent is complete.
func seed(t *testing.T, m *Manager, dir, name string) {
	t.Helper()
	if err := os_copy(filepath.Join(dir, "src", name), filepath.Join(m.cfg.Get().DataDir, name)); err != nil {
		t.Fatal(err)
	}
}

func statusOf(m *Manager, hash string) (Status, bool) {
	for _, s := range m.List() {
		if s.Hash == hash {
			return s, true
		}
	}
	return Status{}, false
}

func TestMoveStorageKeepsSeedingFromNewFolder(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeTorrent(t, dir, "big.bin", 64<<10)
	seed(t, m, dir, "big.bin")
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	oldFile := filepath.Join(m.cfg.Get().DataDir, "big.bin")
	target := filepath.Join(dir, "moved", "here")
	if err := m.MoveStorage(hash, target); err != nil {
		t.Fatal(err)
	}
	// While it runs the torrent must stay in the list; when done it is back in the engine.
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && s.SavePath == target && s.Moving == 0 && s.Progress == 1
	})
	if !exists(filepath.Join(target, "big.bin")) {
		t.Fatal("file is not in the new folder")
	}
	if exists(oldFile) {
		t.Fatal("file is still in the old folder")
	}

	// Still complete after a restart, and Remove(with data) deletes from the new place.
	m.Close()
	cfg, _ := config.Load(filepath.Join(dir, "settings.json"), dir)
	m2, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m2.Close(); waitFor(t, func() bool { return true }) }()
	waitFor(t, func() bool { s, ok := statusOf(m2, hash); return ok && s.Progress == 1 && s.SavePath == target })
	if err := m2.Remove(hash, true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(target, "big.bin")) {
		t.Fatal("data must be deleted from the moved location")
	}
}

func TestMoveStorageRefusesUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	tp := makeTorrent(t, dir, "safe.bin", 20<<10)
	seed(t, m, dir, "safe.bin")
	hash, _ := m.AddFile(tp)
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })
	def := m.cfg.Get().DataDir

	// Something with the same name is already there: never overwrite.
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "safe.bin"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.MoveStorage(hash, other); err == nil {
		t.Fatal("moving onto an existing file must be refused")
	}
	if b, _ := os.ReadFile(filepath.Join(other, "safe.bin")); string(b) != "precious" {
		t.Fatal("the existing file was touched")
	}
	// Same folder is a no-op, unknown torrent and a file as target are errors.
	if err := m.MoveStorage(hash, def); err != nil {
		t.Fatalf("moving to the current folder must be a no-op: %v", err)
	}
	if err := m.MoveStorage("00000000000000000000000000000000000000ff", other); err == nil {
		t.Fatal("unknown torrent must be rejected")
	}
	if err := m.MoveStorage(hash, filepath.Join(other, "safe.bin")); err == nil {
		t.Fatal("a file is not a folder")
	}
	if !exists(filepath.Join(def, "safe.bin")) {
		t.Fatal("refused moves must not disturb the data")
	}
}

func TestMoveBeforeAnythingIsDownloadedOnlyChangesTheFolder(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "empty.bin", 20<<10), WithPaused()) // paused: nothing allocated
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.HasMeta })
	target := filepath.Join(dir, "later")
	if err := m.MoveStorage(hash, target); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.SavePath == target && s.Moving == 0 })
	if err := m.SetPaused(hash, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(target, "empty.bin")) }) // allocated in the new folder
}

func TestCompletedTorrentsAreMovedAutomatically(t *testing.T) {
	dir := t.TempDir()
	done := filepath.Join(dir, "finished")
	m := newManager(t, dir, func(s *config.Settings) { s.MoveCompletedDir = done })
	tp := makeTorrent(t, dir, "auto.bin", 48<<10)
	hash, err := m.AddFile(tp) // no data yet: incomplete
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		r := m.snapshotRecords()[hash]
		return r.WasIncomplete
	})
	// The data "arrives" (as if downloaded); the engine notices it on verification.
	if err := os_copy(filepath.Join(dir, "src", "auto.bin"), filepath.Join(m.cfg.Get().DataDir, "auto.bin")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	for _, tt := range m.torrents {
		go tt.VerifyData()
	}
	m.mu.Unlock()
	waitFor(t, func() bool { return exists(filepath.Join(done, "auto.bin")) })
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.SavePath == done && s.Progress == 1 })
}

func TestTorrentSurvivesRestartEvenWithoutUserCopies(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.TorrentCopyDir = "" }) // copies switched off
	hash, err := m.AddFile(makeTorrent(t, dir, "nocopy.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	m.Close()

	cfg, _ := config.Load(filepath.Join(dir, "settings.json"), dir)
	m2, err := New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m2.Close(); waitFor(t, func() bool { return true }) }()
	if _, ok := statusOf(m2, hash); !ok {
		t.Fatal("torrent lost on restart: metadata was only kept in the user's copy folder")
	}
	// The internal copy goes away with the torrent.
	if err := m2.Remove(hash, false); err != nil {
		t.Fatal(err)
	}
	if exists(m2.metaPath(hash)) {
		t.Fatal("internal metadata copy must be deleted on removal")
	}
}

func TestCopyTreeCountsBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(src, "x.bin"), make([]byte, 3000), 0o644)
	_ = os.WriteFile(filepath.Join(src, "sub", "y.bin"), make([]byte, 5000), 0o644)
	_ = os.MkdirAll(filepath.Join(src, "emptydir"), 0o755)

	if n, err := treeSize(src); err != nil || n != 8000 {
		t.Fatalf("treeSize = %d, %v", n, err)
	}
	var done atomic.Int64
	dst := filepath.Join(dir, "b")
	if err := copyTree(src, dst, &done); err != nil {
		t.Fatal(err)
	}
	if done.Load() != 8000 {
		t.Fatalf("progress counted %d bytes", done.Load())
	}
	for _, p := range []string{"x.bin", filepath.Join("sub", "y.bin"), "emptydir"} {
		if !exists(filepath.Join(dst, p)) {
			t.Fatalf("%s was not copied", p)
		}
	}
}

// A move between two drives cannot be a rename: it must copy, report progress and then
// remove the source. Skipped when the temp dir and the package dir share a drive.
func TestMoveTreeAcrossDrives(t *testing.T) {
	src := t.TempDir() // usually on the system drive
	here, _ := filepath.Abs(".")
	if filepath.VolumeName(src) == filepath.VolumeName(here) {
		t.Skip("no second drive available: temp dir and package dir share a volume")
	}
	root := filepath.Join(src, "album")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 3<<20+123)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	_ = os.WriteFile(filepath.Join(root, "a.bin"), payload, 0o644)
	_ = os.WriteFile(filepath.Join(root, "sub", "b.bin"), payload[:1000], 0o644)

	dstBase, err := os.MkdirTemp(here, "xdrive-")
	if err != nil {
		t.Skipf("cannot create a folder on the other drive: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dstBase) })
	dst := filepath.Join(dstBase, "album")

	m := &Manager{}
	job := &moveJob{running: true}
	moveErr, leftover := m.moveTree(root, dst, job)
	if moveErr != nil || leftover != nil {
		t.Fatalf("move failed: %v / %v", moveErr, leftover)
	}
	if job.copySize != int64(len(payload)+1000) || job.done.Load() != job.copySize {
		t.Fatalf("progress: copied %d of %d", job.done.Load(), job.copySize)
	}
	got, err := os.ReadFile(filepath.Join(dst, "a.bin"))
	if err != nil || string(got) != string(payload) {
		t.Fatalf("copied data differs (err %v)", err)
	}
	if exists(root) {
		t.Fatal("source must be removed after a successful copy")
	}
}

// The trail recoverMoves relies on must be written before the move and gone after it.
func TestMoveStorageClearsItsTrail(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	defer m.Close()
	tp := makeTorrent(t, dir, "trail.bin", 64<<10)
	seed(t, m, dir, "trail.bin")
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	target := filepath.Join(dir, "moved")
	if err := m.MoveStorage(hash, target); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && s.SavePath == target && s.Moving == 0
	})
	r, ok := m.record(hash)
	if !ok {
		t.Fatal("record is gone")
	}
	if r.MoveFrom != "" || r.MoveTo != "" || r.MoveDir != "" {
		t.Errorf("a finished move left its trail behind: %+v", r)
	}
	// A restart must not now think a move was interrupted and delete the data.
	m.recoverMoves()
	if !exists(filepath.Join(target, "trail.bin")) {
		t.Error("recoverMoves removed the data of a move that had finished")
	}
}
