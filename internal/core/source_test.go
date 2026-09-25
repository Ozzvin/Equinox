package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// torrentWith builds a .torrent file's bytes around the info of a torrent made by makeTorrent, with the given
// keys next to it, as a tracker would write them.
func torrentWith(t *testing.T, dir, name string, extra map[string]any) []byte {
	t.Helper()
	mi := loadMI(t, makeTorrent(t, dir, name, 20<<10))
	top := map[string]any{"info": bencode.Bytes(mi.InfoBytes)}
	for k, v := range extra {
		top[k] = v
	}
	b, err := bencode.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSource(t *testing.T) {
	mi := &metainfo.MetaInfo{}
	raw := func(extra map[string]any) []byte {
		b, err := bencode.Marshal(extra)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// what a tracker writes
	s := parseSource(raw(map[string]any{
		"comment": "Раздача 42", "created by": "uTorrent/3.5", "creation date": 1700000000,
		"publisher": "tapochek.net", "publisher-url": "https://tapochek.net/viewtopic.php?p=3103703",
	}), &metainfo.MetaInfo{Comment: "Раздача 42", CreatedBy: "uTorrent/3.5", CreationDate: 1700000000})
	if s.Comment != "Раздача 42" || s.CreatedBy != "uTorrent/3.5" || s.CreatedAt != 1700000000 ||
		s.Publisher != "tapochek.net" || s.PublisherURL != "https://tapochek.net/viewtopic.php?p=3103703" {
		t.Errorf("tracker torrent: %+v", s)
	}

	// the .utf-8 keys win over the plain ones
	s = parseSource(raw(map[string]any{"comment": "plain", "comment.utf-8": "утф", "publisher-url.utf-8": "http://a.example/x"}), mi)
	if s.Comment != "утф" || s.PublisherURL != "http://a.example/x" {
		t.Errorf("utf-8 keys: %+v", s)
	}

	// only web addresses become a link
	for _, bad := range []string{"javascript:alert(1)", "ftp://x.example/a", "file:///etc/passwd", "//x.example", "tapochek.net", "https://"} {
		if got := parseSource(raw(map[string]any{"publisher-url": bad}), mi).PublisherURL; got != "" {
			t.Errorf("%q must not become a link, got %q", bad, got)
		}
	}

	// a comment in windows-1251 (as the torrent says in "encoding")
	cp := string([]byte{0xD0, 0xE0, 0xE7, 0xE4, 0xE0, 0xF7, 0xE0, 0x20, 0xB8}) // "Раздача ё" in 1251
	s = parseSource(raw(map[string]any{"comment": cp, "encoding": "windows-1251"}), &metainfo.MetaInfo{})
	if s.Comment != "Раздача ё" {
		t.Errorf("windows-1251 comment: %q", s.Comment)
	}

	// what the engine writes into metadata it builds itself is not the torrent's own
	s = parseSource(nil, &metainfo.MetaInfo{Comment: "dynamic metainfo from client", CreatedBy: "github.com/anacrolix/torrent", CreationDate: time.Now().Unix()})
	if !s.empty() {
		t.Errorf("engine-made metadata must say nothing: %+v", s)
	}
	// but a real comment stays even if the file was made with the same library
	s = parseSource(nil, &metainfo.MetaInfo{Comment: "mine", CreatedBy: "Equinox"})
	if s.Comment != "mine" || s.CreatedBy != "Equinox" {
		t.Errorf("own comment: %+v", s)
	}
	// garbage does no harm
	if s := parseSource([]byte("not bencode"), mi); !s.empty() {
		t.Errorf("garbage: %+v", s)
	}
}

// A torrent added from a file shows that file's comment, creator, date and tracker page, its copy keeps every key
// of the file, and the same file given again fills in what an older record lacks.
func TestDetailsShowWhatTheFileSays(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	raw := torrentWith(t, dir, "src.bin", map[string]any{
		"comment": "A comment from the tracker", "created by": "uTorrent/3.5", "creation date": 1700000000,
		"publisher": "tapochek.net", "publisher-url": "https://tapochek.net/viewtopic.php?p=3103703",
	})
	file := filepath.Join(dir, "with-page.torrent")
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := m.AddFile(file)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.HasMeta })
	d, err := m.Details(hash)
	if err != nil {
		t.Fatal(err)
	}
	if d.Comment != "A comment from the tracker" || d.CreatedBy != "uTorrent/3.5" || d.CreatedAt.Unix() != 1700000000 ||
		d.Publisher != "tapochek.net" || d.PublisherURL != "https://tapochek.net/viewtopic.php?p=3103703" {
		t.Fatalf("details: comment %q by %q at %v, publisher %q %q", d.Comment, d.CreatedBy, d.CreatedAt, d.Publisher, d.PublisherURL)
	}
	// stable: asking again, at another time, gives the same date (not the time of the call)
	time.Sleep(1100 * time.Millisecond)
	if d2, _ := m.Details(hash); d2.CreatedAt != d.CreatedAt {
		t.Errorf("the creation date must not move: %v then %v", d.CreatedAt, d2.CreatedAt)
	}
	// the copies are the file itself
	copyBytes, err := os.ReadFile(d.CopyPath)
	if err != nil || !bytes.Equal(copyBytes, raw) {
		t.Errorf("the copy must be the file as it was given (%v)", err)
	}
	if mb, err := os.ReadFile(m.metaPath(hash)); err != nil || !bytes.Equal(mb, raw) {
		t.Errorf("the internal copy must be the file as it was given (%v)", err)
	}

	// the record survives a restart
	m.Close()
	m2 := newManager(t, dir, nil)
	if d3, err := m2.Details(hash); err != nil || d3.PublisherURL != d.PublisherURL || d3.Comment != d.Comment {
		t.Errorf("after a restart: %+v %v", d3, err)
	}
}

// A torrent added before the page was kept has a record without it; the file given again fills it in, and nothing
// that is already known is replaced.
func TestGivingTheFileAgainFillsInWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	plain := filepath.Join(dir, "plain.torrent")
	first := torrentWith(t, dir, "again.bin", map[string]any{"comment": "first comment"})
	if err := os.WriteFile(plain, first, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := m.AddFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.HasMeta })
	if d, _ := m.Details(hash); d.PublisherURL != "" || d.Comment != "first comment" {
		t.Fatalf("first: %+v", d)
	}

	better := filepath.Join(dir, "better.torrent")
	if err := os.WriteFile(better, torrentWith(t, dir, "again.bin", map[string]any{
		"comment": "another comment", "publisher-url": "https://tracker.example/topic/7",
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	if h2, err := m.AddFile(better); err != nil || h2 != hash {
		t.Fatalf("giving it again must answer the same torrent: %s %v", h2, err)
	}
	d, _ := m.Details(hash)
	if d.PublisherURL != "https://tracker.example/topic/7" {
		t.Errorf("the page must be filled in: %+v", d)
	}
	if d.Comment != "first comment" {
		t.Errorf("what was known must stay: %q", d.Comment)
	}
}

// A torrent made here shows the comment that was typed, not the engine's own.
func TestCreatedTorrentShowsItsComment(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	src := filepath.Join(dir, "mine.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte{4}, 40<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	job, err := m.StartCreate(CreateRequest{Source: src, Comment: "made by hand", Seed: true, PieceLength: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m, job.ID)
	if j.Error != "" {
		t.Fatal(j.Error)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, j.Hash); return ok && s.HasMeta })
	d, err := m.Details(j.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if d.Comment != "made by hand" || d.CreatedBy != "Equinox" || d.CreatedAt.IsZero() {
		t.Errorf("created torrent: comment %q by %q at %v", d.Comment, d.CreatedBy, d.CreatedAt)
	}
}

// A torrent added by a magnet link knows nothing of a comment: the engine's own text must not show up.
func TestMagnetTorrentShowsNoInventedComment(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	mi := loadMI(t, makeTorrent(t, dir, "mag.bin", 20<<10))
	hash, err := m.AddMagnet(mi.Magnet(nil, nil).String(), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	// give it its info the way the swarm would
	tor, _ := m.get(hash)
	if err := tor.SetInfoBytes(mi.InfoBytes); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.HasMeta })
	d, err := m.Details(hash)
	if err != nil {
		t.Fatal(err)
	}
	if d.Comment != "" || d.CreatedBy != "" || !d.CreatedAt.IsZero() {
		t.Errorf("a magnet torrent has no comment of its own: %q by %q at %v", d.Comment, d.CreatedBy, d.CreatedAt)
	}
}

// A record made before the source was kept is filled in from the internal copy of the metadata, which holds the
// comment, the creator and the date (not the page), and is not asked again.
func TestOldRecordReadsTheInternalCopy(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	mi := loadMI(t, makeTorrent(t, dir, "old.bin", 20<<10))
	mi.Comment, mi.CreatedBy, mi.CreationDate = "old comment", "old client", 1600000000
	f := filepath.Join(dir, "old.torrent")
	out, _ := os.Create(f)
	if err := mi.Write(out); err != nil {
		t.Fatal(err)
	}
	out.Close()
	hash, err := m.AddFile(f)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.HasMeta })
	_ = m.state.with(func(s *state) { s.Torrents[hash].Source = nil }) // as a record of an older version
	d, err := m.Details(hash)
	if err != nil {
		t.Fatal(err)
	}
	if d.Comment != "old comment" || d.CreatedBy != "old client" || d.CreatedAt.Unix() != 1600000000 || d.PublisherURL != "" {
		t.Errorf("old record: %q by %q at %v page %q", d.Comment, d.CreatedBy, d.CreatedAt, d.PublisherURL)
	}
	if r := m.snapshotRecords()[hash]; r.Source == nil || r.Source.Comment != "old comment" {
		t.Errorf("what was read must be remembered: %+v", r.Source)
	}
}
