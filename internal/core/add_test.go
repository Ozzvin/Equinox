package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/config"
)

func loadMI(t *testing.T, path string) *metainfo.MetaInfo {
	t.Helper()
	mi, err := metainfo.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return mi
}

func pieceHigh(tor *torrent.Torrent, i int) bool {
	return tor.Piece(i).State().Priority == torrent.PiecePriorityHigh
}

func TestBatchAddGivesEachEntryItsOwnOptions(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)

	twoFiles := namedTwoFileTorrent(t, dir, "pack") // a.bin (32 KiB) and b.bin (48 KiB)
	plain := loadMI(t, makeTorrent(t, dir, "plain.bin", 20<<10))
	st1, err := m.Stage(twoFiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	st2, err := m.Stage(plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(st1.Files) != 2 || st1.Files[0].Path != "a.bin" || st1.Size != 80<<10 || st1.Exists {
		t.Fatalf("staged torrent described wrongly: %+v", st1)
	}

	custom := filepath.Join(dir, "custom")
	done := filepath.Join(dir, "finished")
	noPrealloc := false
	res, err := m.AddBatch([]BatchItem{
		{Stage: st1.ID, Options: TorrentOptions{
			SavePath: custom, Label: "Work", Paused: true, EdgePieces: true, MaxConns: 7,
			MoveDone: done, Preallocate: &noPrealloc, Files: []string{"skip", "high"},
		}},
		{Stage: st2.ID},
		{Magnet: "magnet:?xt=urn:btih:" + repeatHex("ab", 20) + "&dn=some+magnet"},
		{Infohash: repeatHex("cd", 20)},
		{Infohash: "not-a-hash"},
		{},
		{Stage: "nosuchid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := []bool{true, true, true, true, false, false, false}
	for i, r := range res {
		if r.OK != ok[i] {
			t.Errorf("entry %d: ok=%v want %v (%s)", i, r.OK, ok[i], r.Error)
		}
	}
	if res[4].Error == "" || res[5].Error == "" || res[6].Error == "" {
		t.Fatal("failures must say why")
	}

	// Entry 0: every option was applied.
	rec := m.snapshotRecords()[st1.Hash]
	if rec.SavePath != custom || rec.Label != "Work" || !rec.Paused || !rec.EdgePieces || rec.MaxConns != 7 {
		t.Fatalf("options lost: %+v", rec)
	}
	if rec.MoveDone != done || rec.Prealloc == nil || *rec.Prealloc {
		t.Fatalf("completed folder / preallocation lost: %+v", rec)
	}
	if len(rec.FilePrios) != 2 || rec.FilePrios[0] != PrioSkip || rec.FilePrios[1] != PrioHigh {
		t.Fatalf("file priorities lost: %v", rec.FilePrios)
	}
	if !exists(done) {
		t.Fatal("the completed-downloads folder must be created")
	}
	// Entry 1 got nothing special.
	rec2 := m.snapshotRecords()[st2.Hash]
	if rec2.SavePath != m.cfg.Get().DataDir && filepath.Clean(rec2.SavePath) != filepath.Clean(m.cfg.Get().DataDir) {
		t.Fatalf("defaults expected: %+v", rec2)
	}
	if rec2.Paused || rec2.EdgePieces || rec2.MaxConns != 0 || rec2.Prealloc != nil {
		t.Fatalf("entry 1 must keep the defaults: %+v", rec2)
	}
	// Magnet and infohash entries are in, waiting for their metadata.
	for _, i := range []int{2, 3} {
		if _, ok := statusOf(m, res[i].Hash); !ok {
			t.Errorf("entry %d not in the list", i)
		}
	}

	// Paused entry with preallocation off: resuming must not reserve the space; the skipped
	// file must not appear at all. The other entry (defaults) does preallocate.
	waitFor(t, func() bool { s, ok := statusOf(m, st1.Hash); return ok && s.HasMeta })
	_ = m.SetPaused(st1.Hash, false)
	waitFor(t, func() bool { return exists(filepath.Join(m.cfg.Get().DataDir, "plain.bin")) })
	time.Sleep(300 * time.Millisecond)
	if exists(filepath.Join(custom, "pack", "a.bin")) {
		t.Fatal("a skipped file must not be created")
	}
	if exists(filepath.Join(custom, "pack", "b.bin")) {
		t.Fatal("preallocation was switched off for this torrent")
	}

	// The same torrent added again is reported, not changed.
	again, _ := m.Stage(twoFiles, nil)
	if !again.Exists {
		t.Fatal("staging must say that the torrent is already in the client")
	}
	res, _ = m.AddBatch([]BatchItem{{Stage: again.ID, Options: TorrentOptions{Label: "Other"}}})
	if !res[0].OK || !res[0].Exists || m.snapshotRecords()[st1.Hash].Label != "Work" {
		t.Fatalf("an existing torrent must be left untouched: %+v", res[0])
	}
}

func repeatHex(s string, n int) string { return string(bytes.Repeat([]byte(s), n)) }

func TestBatchRejectsBadOptions(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	file := filepath.Join(dir, "afile")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	mk := func(name string) string {
		st, err := m.Stage(loadMI(t, makeTorrent(t, dir, name, 20<<10)), nil)
		if err != nil {
			t.Fatal(err)
		}
		return st.ID
	}
	res, _ := m.AddBatch([]BatchItem{
		{Stage: mk("o1.bin"), Options: TorrentOptions{MaxConns: 5000}},
		{Stage: mk("o2.bin"), Options: TorrentOptions{MoveDone: file}},
		{Stage: mk("o3.bin"), Options: TorrentOptions{Files: []string{"skip", "skip"}}}, // one file, two priorities
		{Stage: mk("o4.bin"), Options: TorrentOptions{Files: []string{"maybe"}}},
		{Stage: mk("o5.bin"), Options: TorrentOptions{SavePath: file}},
	})
	for i, r := range res {
		if r.OK || r.Error == "" {
			t.Errorf("entry %d must be refused: %+v", i, r)
		}
	}
	if len(m.List()) != 0 {
		t.Fatalf("refused entries must not be added: %d in the list", len(m.List()))
	}
	if _, err := m.AddBatch(nil); err == nil {
		t.Fatal("an empty list must be refused")
	}
}

func TestSkipCheckTrustsFilesOnDisk(t *testing.T) {
	dir := t.TempDir()
	tp := makeTorrent(t, dir, "trust.bin", 96<<10) // the torrent describes bytes of value 7

	place := func(m *Manager) { // the file on disk has the right size but the wrong content
		p := filepath.Join(m.cfg.Get().DataDir, "trust.bin")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte{1}, 96<<10), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Checked (the default): the engine hashes the file and finds nothing usable in it.
	checked := newManager(t, filepath.Join(dir, "a"), nil)
	place(checked)
	hc, _ := checked.AddFile(tp)
	time.Sleep(1200 * time.Millisecond)
	if s, _ := statusOf(checked, hc); s.Progress != 0 {
		t.Fatalf("hashing must find the wrong content: progress %v", s.Progress)
	}

	// Skipped: the same file is taken as complete without being read.
	skipped := newManager(t, filepath.Join(dir, "b"), nil)
	place(skipped)
	hs, err := skipped.AddFile(tp, WithOptions(TorrentOptions{SkipCheck: true}))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(skipped, hs); return ok && s.Progress == 1 })
	if s, _ := statusOf(skipped, hs); s.Checking {
		t.Fatal("nothing was checked")
	}

	// A later recheck does look at the data, and corrects the record.
	if err := skipped.Recheck(hs); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, _ := statusOf(skipped, hs); return !s.Checking && s.Progress == 0 })

	// Files that are missing or short are never trusted.
	partial := newManager(t, filepath.Join(dir, "c"), nil)
	p := filepath.Join(partial.cfg.Get().DataDir, "trust.bin")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, bytes.Repeat([]byte{1}, 40<<10), 0o644) // shorter than the torrent says
	hp, _ := partial.AddFile(tp, WithOptions(TorrentOptions{SkipCheck: true}))
	time.Sleep(800 * time.Millisecond)
	if s, _ := statusOf(partial, hp); s.Progress != 0 {
		t.Fatalf("a short file must not be trusted: progress %v", s.Progress)
	}
}

func TestLocalFilesAreCheckedAutomaticallyAndTheCheckIsVisible(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	const size = 48 << 20
	src := filepath.Join(dir, "src", "big.bin")
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	if err := os.WriteFile(src, bytes.Repeat([]byte{0x33}, size), 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 1 << 20}
	if err := info.BuildFromFilePath(src); err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: mustMarshal(t, info)}
	if err := os_copy(src, filepath.Join(m.cfg.Get().DataDir, "big.bin")); err != nil {
		t.Fatal(err)
	}

	hash, err := m.AddMetaInfo(mi) // nothing asks for a check: it happens by itself
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Progress == 1 })

	// A manual recheck of the same data shows its progress while it runs.
	tor, _ := m.get(hash)
	if err := m.Recheck(hash); err != nil {
		t.Fatal(err)
	}
	sawChecking, sawPartial := false, false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		m.updateChecks([]*torrent.Torrent{tor})
		s, _ := statusOf(m, hash)
		if s.Checking {
			sawChecking = true
			if s.CheckProgress > 0 && s.CheckProgress < 1 {
				sawPartial = true
			}
		}
		m.mu.Lock()
		still := m.checking[hash]
		m.mu.Unlock()
		if !still && sawChecking {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !sawChecking || !sawPartial {
		t.Fatalf("the check must be visible with a percentage: checking=%v partial=%v", sawChecking, sawPartial)
	}
	m.updateChecks([]*torrent.Torrent{tor})
	if s, _ := statusOf(m, hash); s.Checking || s.Progress != 1 {
		t.Fatalf("after the check the data is complete and not marked as checking: %+v", s)
	}
}

func TestEdgePiecesAreRaisedWithoutSequentialMode(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	mk := func(name string, opts TorrentOptions) (*torrent.Torrent, int) {
		h, err := m.AddMetaInfo(namedTwoFileTorrent(t, dir, name), WithOptions(opts))
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { s, ok := statusOf(m, h); return ok && s.HasMeta })
		tor, _ := m.get(h)
		return tor, tor.NumPieces()
	}
	with, n := mk("edge-on", TorrentOptions{EdgePieces: true})
	without, _ := mk("edge-off", TorrentOptions{})
	waitFor(t, func() bool { return pieceHigh(with, 0) && pieceHigh(with, n-1) })
	for _, i := range []int{0, n - 1} {
		if pieceHigh(without, i) {
			t.Fatalf("piece %d must keep its normal priority when edges were not asked for", i)
		}
	}
	if pieceHigh(with, n/2) && n > 8 {
		t.Fatal("only the edges are raised, not the middle")
	}
	if s, _ := statusOf(m, mustHashString(with)); s.Sequential {
		t.Fatal("edges must not turn on sequential mode")
	}
}

func mustHashString(t *torrent.Torrent) string { return t.InfoHash().HexString() }

func TestPerTorrentCompletedFolderBeatsTheGlobalOne(t *testing.T) {
	dir := t.TempDir()
	globalDone := filepath.Join(dir, "global-done")
	own := filepath.Join(dir, "own-done")
	m := newManager(t, dir, func(s *config.Settings) { s.MoveCompletedDir = globalDone })

	tp := makeTorrent(t, dir, "mine.bin", 48<<10)
	hash, err := m.AddFile(tp, WithOptions(TorrentOptions{MoveDone: own}))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return m.snapshotRecords()[hash].WasIncomplete })
	if err := os_copy(filepath.Join(dir, "src", "mine.bin"), filepath.Join(m.cfg.Get().DataDir, "mine.bin")); err != nil {
		t.Fatal(err)
	}
	tor, _ := m.get(hash)
	go tor.VerifyData()
	waitFor(t, func() bool { return exists(filepath.Join(own, "mine.bin")) })
	if exists(filepath.Join(globalDone, "mine.bin")) {
		t.Fatal("the torrent's own folder must win over the global one")
	}
}

func TestStagingIsBounded(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	if _, err := m.Stage(&metainfo.MetaInfo{InfoBytes: []byte("garbage")}, nil); err == nil {
		t.Fatal("an unparsable torrent must be refused")
	}
	m.mu.Lock()
	for i := 0; i < maxStaged; i++ {
		m.staged[string(rune('a'+i%26))+repeatHex("0", i)] = &stagedEntry{at: time.Now()}
	}
	m.mu.Unlock()
	if _, err := m.Stage(loadMI(t, makeTorrent(t, dir, "over.bin", 20<<10)), nil); err == nil {
		t.Fatal("the list must be bounded")
	}
	// Old entries are dropped instead of blocking new ones forever.
	m.mu.Lock()
	for _, e := range m.staged {
		e.at = time.Now().Add(-2 * stagedTTL)
	}
	m.mu.Unlock()
	if _, err := m.Stage(loadMI(t, makeTorrent(t, dir, "fresh.bin", 20<<10)), nil); err != nil {
		t.Fatalf("expired entries must make room: %v", err)
	}
}

func TestAddDefaultsAreSavedAndValidated(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	if !m.AddDialog().Defaults.EdgePieces {
		t.Fatal("first/last pieces are on by default")
	}
	target := filepath.Join(dir, "later")
	if err := m.SetAddDefaults(config.AddDefaults{Paused: true, SkipCheck: true, MoveDoneEnabled: true, MoveDone: target}); err != nil {
		t.Fatal(err)
	}
	d := m.AddDialog()
	if !d.Defaults.Paused || !d.Defaults.SkipCheck || d.Defaults.EdgePieces || d.Defaults.MoveDone == "" || !exists(target) {
		t.Fatalf("defaults not saved: %+v", d.Defaults)
	}
	f := filepath.Join(dir, "file")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	if err := m.SetAddDefaults(config.AddDefaults{MoveDoneEnabled: true, MoveDone: f}); err == nil {
		t.Fatal("a file is not a valid folder")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := bencode.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
