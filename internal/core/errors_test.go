package core

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDiskFullStopsTheTorrentAndSaysWhy(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	orig := diskFree
	diskFree = func(string) (uint64, error) { return 100, nil } // "the disk has 100 bytes left"
	defer func() { diskFree = orig }()

	hash, err := m.AddFile(makeTorrent(t, dir, "big.bin", 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, hash); return ok && s.Paused && s.ErrorKind == ErrKindDiskFull })
	s, _ := statusOf(m, hash)
	if s.Error == "" {
		t.Fatal("the reason must be reported")
	}
	if s.ErrorCode != CodeNoSpace || !slices.Equal(s.ErrorArgs, []string{"65536", "100"}) || strings.Contains(s.Error, "bytes") {
		t.Fatalf("the page words it from the code and the byte counts: %q %v %q", s.ErrorCode, s.ErrorArgs, s.Error)
	}
	if exists(filepath.Join(m.cfg.Get().DataDir, "big.bin")) {
		t.Fatal("nothing may be allocated on a disk that cannot hold the torrent")
	}

	// Making room and resuming is the retry: the error goes away and the file is allocated.
	diskFree = orig
	if err := m.SetPaused(hash, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(m.cfg.Get().DataDir, "big.bin")) })
	if s, _ := statusOf(m, hash); s.Error != "" || s.Paused {
		t.Fatalf("resume must clear the failure: %+v", s)
	}
}

// A torrent that would fit the free space by itself must still be refused if, together with what every other
// active torrent on the same disk still has left to download, there would not be room for both — otherwise two
// large torrents added one after another could each pass their own check and only run out of room once both are
// well under way (the bug the ledger in otherNeededBytes closes; see BACKLOG.md).
func TestPreallocateAccountsForOtherActiveTorrentsOnTheSameDisk(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	orig := diskFree
	diskFree = func(string) (uint64, error) { return 100 << 10, nil } // "the disk has 100 KiB left"
	defer func() { diskFree = orig }()

	first, err := m.AddFile(makeTorrent(t, dir, "first.bin", 60<<10))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exists(filepath.Join(m.cfg.Get().DataDir, "first.bin")) })
	if s, _ := statusOf(m, first); s.Error != "" {
		t.Fatalf("the first torrent fits the free space alone: %+v", s)
	}

	second, err := m.AddFile(makeTorrent(t, dir, "second.bin", 60<<10))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, ok := statusOf(m, second); return ok && s.Paused && s.ErrorKind == ErrKindDiskFull })
	if s, _ := statusOf(m, second); s.ErrorCode != CodeNoSpaceOthers || !slices.Equal(s.ErrorArgs, []string{"61440", "61440", "102400"}) {
		t.Fatalf("the share of the other torrents must be named: %q %v", s.ErrorCode, s.ErrorArgs)
	}
	if exists(filepath.Join(m.cfg.Get().DataDir, "second.bin")) {
		t.Fatal("the second torrent must get no space while the first still needs its own 60 KiB of the same 100 KiB")
	}
	if s, _ := statusOf(m, first); s.Error != "" || s.Paused {
		t.Fatalf("the second torrent's refusal must not touch the first: %+v", s)
	}
}

func TestWriteErrorIsReportedOnceAndPauses(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "w.bin", 20<<10))
	if err != nil {
		t.Fatal(err)
	}
	tor, _ := m.get(hash)

	m.onWriteError(tor, hash, errors.New("the disk went away"))
	m.onWriteError(tor, hash, errors.New("second chunk failed too")) // the engine repeats itself
	s, _ := statusOf(m, hash)
	if s.ErrorKind != ErrKindWrite || s.Error != "the disk went away" || !s.Paused {
		t.Fatalf("first failure must be kept and the torrent paused: %+v", s)
	}
	if err := m.SetPaused(hash, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := statusOf(m, hash); s.Error != "" {
		t.Fatalf("resuming clears the failure: %+v", s)
	}

	// Removing a torrent forgets its failure as well.
	m.onWriteError(tor, hash, errors.New("again"))
	if err := m.Remove(hash, false); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, left := m.errs[hash]
	m.mu.Unlock()
	if left {
		t.Fatal("failure record must be dropped with the torrent")
	}
}

// begin() runs in the background after a torrent is added; when it finishes it forgets a failure of the disk
// preparation, but not a write error the engine reported meanwhile.
func TestFinishedSetupKeepsAWriteError(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash, err := m.AddFile(makeTorrent(t, dir, "k.bin", 20<<10), WithPaused())
	if err != nil {
		t.Fatal(err)
	}
	m.setError(hash, ErrKindWrite, "the disk went away")
	m.clearSetupError(hash)
	if s, _ := statusOf(m, hash); s.ErrorKind != ErrKindWrite || s.Error != "the disk went away" {
		t.Fatalf("a write error must survive the end of the setup: %+v", s)
	}
	m.setError(hash, ErrKindDiskFull, "no room")
	m.clearSetupError(hash)
	if s, _ := statusOf(m, hash); s.Error != "" {
		t.Fatalf("a failure of the setup itself must be forgotten once it is over: %+v", s)
	}
}

// "Not enough disk space" in Russian and in the units of the list, with what is needed and what is free; the
// numbers of the two toasts the user saw on a move (3 395 680 625 and 11 429 696 605 bytes, 809 136 128 free).
func TestNoSpaceSaysHowMuchIsShortInTheUnitsOfTheList(t *testing.T) {
	for _, c := range []struct {
		code               string
		need, others, free uint64
		msg                string
		args               []string
	}{
		{CodeNoSpaceMove, 3395680625, 0, 809136128, "На диске не хватает 2,4 ГБ (нужно 3,2 ГБ, свободно в целевой папке 772 МБ)", []string{"3395680625", "809136128"}},
		{CodeNoSpaceMove, 11429696605, 0, 809136128, "На диске не хватает 9,9 ГБ (нужно 10,6 ГБ, свободно в целевой папке 772 МБ)", []string{"11429696605", "809136128"}},
		{CodeNoSpace, 1 << 30, 0, 300 << 10, "На диске не хватает 1024 МБ (нужно 1,0 ГБ, свободно 300 КБ)", []string{"1073741824", "307200"}},
		{CodeNoSpace, 700 << 10, 0, 0, "На диске не хватает 700 КБ (нужно 700 КБ, свободно 0 Б)", []string{"716800", "0"}},
		{CodeNoSpaceOthers, 2 << 30, 1 << 30, 2 << 30, "На диске не хватает 1,0 ГБ (нужно 3,0 ГБ, свободно 2,0 ГБ), считая 1,0 ГБ для других активных раздач на этом диске",
			[]string{"2147483648", "1073741824", "2147483648"}},
	} {
		err := noSpace(c.code, c.need, c.others, c.free)
		code, args := err.(Coder).ErrCode()
		if err.Error() != c.msg || code != c.code || !slices.Equal(args, c.args) || !errors.Is(err, ErrNoDiskSpace) {
			t.Errorf("%s %d: %q %q %v", c.code, c.need, err, code, args)
		}
	}
}

// A failed move hands its code to the list, so the page words the toast itself.
func TestMoveErrorCodeReachesTheStatus(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir, nil)
	hash := makeAndAdd(t, m, dir)
	m.mu.Lock()
	m.moves[hash] = &moveJob{name: "b.bin", err: "На диске не хватает …", errCode: CodeNoSpaceMove, errArgs: []string{"2", "1"}}
	m.mu.Unlock()
	if s, _ := statusOf(m, hash); s.MoveErrorCode != CodeNoSpaceMove || !slices.Equal(s.MoveErrorArgs, []string{"2", "1"}) {
		t.Fatalf("%q %v", s.MoveErrorCode, s.MoveErrorArgs)
	}
}
