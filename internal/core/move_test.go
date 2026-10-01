package core

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

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
	defer closeAndSettle(m2, dir)
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

func TestMoveQueueRespectsMaxConcurrentMoves(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, func(s *config.Settings) { s.MaxConcurrentMoves = 1 })
	tp := makeTorrent(t, dir, "q.bin", 20<<10)
	seed(t, m, dir, "q.bin")
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	// Occupy the one move slot with a synthetic job (no real goroutine, so it never finishes on
	// its own): this makes the queueing deterministic instead of racing a real background move.
	m.mu.Lock()
	m.moves["occupying"] = &moveJob{running: true}
	m.mu.Unlock()

	target := filepath.Join(dir, "moved")
	if err := m.MoveStorage(hash, target); err != nil {
		t.Fatalf("a move past the limit must queue, not fail: %v", err)
	}
	m.mu.Lock()
	job := m.moves[hash]
	queued := job != nil && job.queued && !job.running
	m.mu.Unlock()
	if !queued {
		t.Fatalf("move must be queued while the one slot is taken: %+v", job)
	}
	if s, ok := statusOf(m, hash); !ok || s.Moving != -2 || s.SavePath == target {
		t.Fatalf("queued torrent must show as waiting and stay put: %+v", s)
	}

	// Free the slot: the queued move must start and finish on its own.
	m.mu.Lock()
	delete(m.moves, "occupying")
	m.mu.Unlock()
	m.runMoveQueue()
	waitFor(t, func() bool {
		s, ok := statusOf(m, hash)
		return ok && s.SavePath == target && s.Moving == 0 && s.Progress == 1
	})
}

func TestMoveDoneOffSkipsTheGlobalFolder(t *testing.T) {
	dir := t.TempDir()
	done := filepath.Join(dir, "finished")
	m := newManager(t, dir, func(s *config.Settings) { s.MoveCompletedDir = done })
	tp := makeTorrent(t, dir, "stay.bin", 48<<10)
	hash, err := m.AddFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetMoveDone(hash, false, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		r := m.snapshotRecords()[hash]
		return r.WasIncomplete
	})
	if err := os_copy(filepath.Join(dir, "src", "stay.bin"), filepath.Join(m.cfg.Get().DataDir, "stay.bin")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	for _, tt := range m.torrents {
		go tt.VerifyData()
	}
	m.mu.Unlock()
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })
	time.Sleep(200 * time.Millisecond) // give a wrongly-triggered move a chance to start
	if exists(filepath.Join(done, "stay.bin")) {
		t.Fatal("a torrent with its move explicitly turned off must not be moved to the global folder")
	}
	if s, ok := statusOf(m, hash); !ok || s.SavePath == done {
		t.Fatalf("save path must stay put: %+v", s)
	}
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
	defer closeAndSettle(m2, dir)
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

// A torrent being moved is briefly out of the engine (see List's "keep showing them" fallback below) and, until
// this was fixed, that placeholder Status never got a Tracker/TrackerSite at all, so the sidebar filed a torrent
// with a perfectly good tracker under "Без трекера" for as long as its move took. Reproduced here without needing
// a move slow enough to poll mid-flight (a same-drive move is a near-instant rename): simulate the exact state
// List() sees during a real one directly, since that is the only part of a move this is really about.
func TestListShowsTheTrackerWhileMoving(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	defer m.Close()

	mi, err := metainfo.LoadFromFile(makeTorrent(t, dir, "moving.bin", 32<<10))
	if err != nil {
		t.Fatal(err)
	}
	mi.Announce = "http://tapochek.net/announce.php?uk=SECRET"
	withAnnounce := filepath.Join(dir, "with-announce.torrent")
	f, err := os.Create(withAnnounce)
	if err != nil {
		t.Fatal(err)
	}
	if err := mi.Write(f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	seed(t, m, dir, "moving.bin")
	hash, err := m.AddFile(withAnnounce)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })
	before, ok := statusOf(m, hash)
	if !ok || before.Tracker != "tapochek" {
		t.Fatalf("need a torrent with a known tracker before simulating a move: %+v", before)
	}

	m.mu.Lock()
	var ih metainfo.Hash
	for h := range m.torrents {
		if h.HexString() == hash {
			ih = h
		}
	}
	delete(m.torrents, ih) // exactly what a real move does while it runs
	m.moves[hash] = &moveJob{name: before.Name, selSize: before.Size, running: true}
	m.mu.Unlock()

	during, ok := statusOf(m, hash)
	if !ok {
		t.Fatal("a torrent being moved must stay in the list")
	}
	if during.Moving == 0 {
		t.Fatalf("the fallback status must report as moving: %+v", during)
	}
	if during.Tracker != "tapochek" || during.TrackerSite != "tapochek.net" {
		t.Fatalf("the tracker must not be lost while a torrent is being moved: %+v", during)
	}
}
