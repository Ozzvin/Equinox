package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

func waitJob(t *testing.T, m *Manager, id string) CreateJob {
	t.Helper()
	var j CreateJob
	waitFor(t, func() bool {
		var err error
		j, err = m.Create(id)
		return err == nil && !j.Running
	})
	return j
}

func TestCreateTorrentFromFolderAndSeedIt(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)

	root := filepath.Join(dir, "library", "Album")
	if err := os.MkdirAll(filepath.Join(root, "CD2"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "a.bin"), bytes.Repeat([]byte{1}, 40<<10), 0o644)
	_ = os.WriteFile(filepath.Join(root, "CD2", "b.bin"), bytes.Repeat([]byte{2}, 25<<10), 0o644)

	job, err := m.StartCreate(CreateRequest{
		Source: root, Trackers: []string{"udp://tracker.one:1/announce", " https://tracker.two/announce "},
		Comment: "made in a test", Private: true, PieceLength: 16 << 10, Seed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, m, job.ID)
	if done.Error != "" || !done.Seeding || done.Hash == "" {
		t.Fatalf("job failed: %+v", done)
	}
	if want := filepath.Join(dir, "library", "Album.torrent"); done.Output != want {
		t.Fatalf("by default the file goes next to the source: %s", done.Output)
	}

	mi, err := metainfo.LoadFromFile(done.Output)
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Album" || len(info.Files) != 2 || info.TotalLength() != 65<<10 || info.PieceLength != 16<<10 {
		t.Fatalf("wrong info: name=%q files=%d total=%d piece=%d", info.Name, len(info.Files), info.TotalLength(), info.PieceLength)
	}
	if info.Private == nil || !*info.Private {
		t.Fatal("private flag missing")
	}
	if mi.Comment != "made in a test" || mi.CreatedBy != "Equinox" || mi.CreationDate == 0 {
		t.Fatalf("metadata missing: %+v", mi)
	}
	al := mi.UpvertedAnnounceList()
	if len(al) != 2 || al[1][0] != "https://tracker.two/announce" {
		t.Fatalf("trackers: %v", al)
	}
	if mi.HashInfoBytes().HexString() != done.Hash {
		t.Fatal("the reported hash must be the torrent's real hash")
	}

	// The strongest proof that the hashes are right: the client verifies the data it was
	// pointed at and finds it complete, ready to seed.
	waitFor(t, func() bool { s, ok := statusOf(m, done.Hash); return ok && s.Progress == 1 })
	s, _ := statusOf(m, done.Hash)
	if s.SavePath != filepath.Join(dir, "library") {
		t.Fatalf("seeding must use the folder that holds the data: %s", s.SavePath)
	}
	if !exists(filepath.Join(root, "a.bin")) {
		t.Fatal("the source data must stay untouched")
	}
}

func TestCreateSingleFileWithoutSeeding(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	src := filepath.Join(dir, "movie.mkv")
	_ = os.WriteFile(src, bytes.Repeat([]byte{9}, 100<<10), 0o644)
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)

	job, err := m.StartCreate(CreateRequest{Source: src, Output: outDir}) // a folder as output
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, m, job.ID)
	if done.Error != "" || done.Seeding {
		t.Fatalf("unexpected: %+v", done)
	}
	if done.Output != filepath.Join(outDir, "movie.mkv.torrent") || !exists(done.Output) {
		t.Fatalf("output: %s", done.Output)
	}
	if len(m.List()) != 0 {
		t.Fatal("without Seed the torrent must not be added")
	}
	mi, _ := metainfo.LoadFromFile(done.Output)
	if info, _ := mi.UnmarshalInfo(); info.Length != 100<<10 || info.Name != "movie.mkv" {
		t.Fatalf("single-file info wrong: %+v", info)
	}
}

func TestCreateRejectsBadRequests(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	src := filepath.Join(dir, "data.bin")
	_ = os.WriteFile(src, []byte("some data"), 0o644)
	empty := filepath.Join(dir, "empty")
	_ = os.MkdirAll(empty, 0o755)
	taken := filepath.Join(dir, "taken.torrent")
	_ = os.WriteFile(taken, []byte("keep me"), 0o644)

	cases := map[string]CreateRequest{
		"no source":         {},
		"missing source":    {Source: filepath.Join(dir, "nope")},
		"empty folder":      {Source: empty},
		"bad tracker":       {Source: src, Trackers: []string{"gopher://x.example/a"}},
		"piece not pow2":    {Source: src, PieceLength: 100000},
		"piece too small":   {Source: src, PieceLength: 1024},
		"overwrite":         {Source: src, Output: taken},
		"comment too long":  {Source: src, Comment: strings.Repeat("x", 501)},
		"tracker not a url": {Source: src, Trackers: []string{"::::"}},
	}
	for name, req := range cases {
		if _, err := m.StartCreate(req); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if b, _ := os.ReadFile(taken); string(b) != "keep me" {
		t.Fatal("an existing file must never be overwritten")
	}
	if _, err := m.Create("nosuchjob"); err == nil {
		t.Fatal("unknown job id must be an error")
	}
}

func TestHasData(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	_ = os.MkdirAll(filepath.Join(empty, "a", "b"), 0o755)
	_ = os.WriteFile(filepath.Join(empty, "a", "zero.bin"), nil, 0o644)
	if hasData(empty, time.Minute) {
		t.Fatal("folders and zero-length files are no data")
	}
	if !hasData(empty, -time.Second) {
		t.Fatal("when the budget is out the answer is \"yes\": the job itself finds out")
	}
	_ = os.WriteFile(filepath.Join(empty, "a", "b", "x.bin"), []byte("x"), 0o644)
	if !hasData(empty, time.Minute) {
		t.Fatal("a file with content is data")
	}
}

// Finished jobs make room for new ones, oldest first, while a full set of running ones refuses another.
func TestCreateJobsAreBounded(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	src := filepath.Join(dir, "d.bin")
	_ = os.WriteFile(src, bytes.Repeat([]byte{3}, 20<<10), 0o644)

	m.mu.Lock()
	for i := 0; i < maxCreateJobs; i++ {
		id := string(rune('a' + i))
		m.creates[id] = &CreateJob{ID: id, Running: true, Started: time.Now().Add(-time.Duration(i) * time.Second)}
	}
	m.mu.Unlock()
	if _, err := m.StartCreate(CreateRequest{Source: src}); err == nil {
		t.Fatal("a full set of running jobs must refuse another")
	}

	m.mu.Lock()
	oldest := string(rune('a' + 5))
	m.creates[oldest].Running = false // the only finished one, so the one to go
	m.mu.Unlock()
	job, err := m.StartCreate(CreateRequest{Source: src, Output: filepath.Join(dir, "d2.torrent")})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	n, kept := len(m.creates), m.creates[oldest]
	m.mu.Unlock()
	if kept != nil || n != maxCreateJobs {
		t.Fatalf("the oldest finished job should have made room: %d jobs, oldest kept = %v", n, kept != nil)
	}
	waitJob(t, m, job.ID)
}
