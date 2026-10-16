package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/store"
)

// Files deleted or taken away by hand while the torrent is in the program. The engine notices nothing: it reads a piece
// for a peer, the read fails, it refuses that peer and goes on; with nobody asking it does not even read. The torrent
// stayed "Seeding" at 100%. Here, as in qBittorrent and Transmission, it stops with an error saying which files are not
// found, and the pieces of those files stop counting as had. Then:
//   - the files are put back: the program checks them and the torrent goes on by itself (also after "Check");
//   - "Resume": the files are checked, and what is not there is downloaded again.
// A skipped file and a file nothing of which was had yet are not looked for. A move of the files (move.go,
// movefiles.go) takes the torrent out of the engine or holds it busy, so it is not taken for files gone.

const (
	missingEvery  = 5   // seconds between the rounds of looking at the files
	missingBudget = 500 // files looked at in one round at most: a large library is gone through over several rounds
)

// noteMissing is the storage telling that a read found a file of the torrent gone (see store.OnMissingFile). It runs on
// the engine's reading goroutine: it only marks the torrent, the loop looks at it on its next tick.
func (m *Manager) noteMissing(ih metainfo.Hash) {
	m.missMu.Lock()
	if m.missNow == nil {
		m.missNow = map[metainfo.Hash]bool{}
	}
	m.missNow[ih] = true
	m.missMu.Unlock()
}

// checkMissing looks for files gone: every few seconds through the torrents in turn, within missingBudget files a round,
// and at once at those a read found a file of gone.
func (m *Manager) checkMissing(ts []*torrent.Torrent, recs map[string]record, tickN int) {
	m.missMu.Lock()
	now := m.missNow
	m.missNow = nil
	m.missMu.Unlock()
	round := tickN%missingEvery == 0
	if (!round && len(now) == 0) || len(ts) == 0 {
		return
	}
	order := make([]*torrent.Torrent, len(ts))
	copy(order, ts)
	sort.Slice(order, func(i, j int) bool { return order[i].InfoHash().HexString() < order[j].InfoHash().HexString() })
	start := sort.Search(len(order), func(i int) bool { return order[i].InfoHash().HexString() > m.missNext })
	budget := missingBudget
	for k := range order {
		t := order[(start+k)%len(order)]
		hash := t.InfoHash().HexString()
		due := now[t.InfoHash()]
		if round && budget > 0 {
			due = true
			m.missNext = hash
		}
		if due {
			budget -= m.lookForMissing(t, hash, recs[hash])
		}
	}
}

// filesBusy says whether the files of a torrent are being checked or moved now, or are about to be: their absence then
// is no loss. Called with m.mu held.
func (m *Manager) filesBusy(hash string) bool {
	if m.checkHolds(hash) || m.rechecking[hash] || m.phase[hash] != nil || m.held[hash] != nil {
		return true
	}
	if _, ok := m.checkSel[hash]; ok { // a recheck of ours, also one still waiting for its turn
		return true
	}
	if j := m.moves[hash]; j != nil && (j.running || j.queued) {
		return true
	}
	return false
}

// lookForMissing looks at the files of one torrent and returns how many it looked at.
func (m *Manager) lookForMissing(t *torrent.Torrent, hash string, r record) int {
	info := t.Info()
	if info == nil {
		return 0
	}
	m.mu.Lock()
	e, failed := m.errs[hash]
	busy := m.filesBusy(hash)
	m.mu.Unlock()
	if busy || (failed && (e.kind != ErrKindMissing || e.recheck)) {
		return 0 // stopped for another reason, or the files that came back are being checked
	}
	paths, err := store.Paths(m.saveDir(hash), info)
	if err != nil {
		return 0
	}
	if failed { // waiting for the files to come back
		for _, i := range e.files {
			if !onDisk(paths[i]) {
				return len(e.files)
			}
		}
		if err := m.RecheckFiles(hash, e.files); err != nil && !errors.Is(err, ErrBusy) {
			fmt.Fprintf(os.Stderr, "checking the files of %s that came back: %v\n", t.Name(), err)
		}
		return len(e.files)
	}
	kept := m.store.InParts(t.InfoHash()) // skipped files whose bytes are in the part file only
	var gone []int
	n := 0
	for i, f := range t.Files() {
		if f.Length() == 0 || padFile(f) || prioAt(r.FilePrios, i) == PrioSkip || (i < len(kept) && kept[i]) {
			continue
		}
		if f.BytesCompleted() == 0 {
			continue // nothing of it was had yet: it is not on the disk because it was never written
		}
		n++
		if _, err := os.Stat(paths[i]); errors.Is(err, fs.ErrNotExist) {
			gone = append(gone, i)
		}
	}
	if len(gone) > 0 {
		m.filesMissing(t, hash, r, gone, paths)
	}
	return n
}

// onDisk tells whether a file is there to be read.
func onDisk(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// filesMissing stops a torrent whose files are gone and makes the engine stop counting their pieces as had.
func (m *Manager) filesMissing(t *torrent.Torrent, hash string, r record, gone []int, paths []string) {
	e := torrentError{kind: ErrKindMissing, files: gone, paused: r.Paused}
	e.msg, e.code, e.args = missingText(paths, gone)
	fmt.Fprintf(os.Stderr, "files of %s are missing: %s\n", t.Name(), e.msg)
	m.mu.Lock()
	m.errs[hash] = e
	m.mu.Unlock()
	if !r.Paused {
		m.setPaused(t, hash, true)
	}
	m.store.Restat(t.InfoHash())
	files := t.Files()
	for _, i := range gone {
		for p := files[i].BeginPieceIndex(); p < files[i].EndPieceIndex(); p++ {
			t.Piece(p).UpdateCompletion()
		}
	}
}

// missingText is the error of the files not found: the first of them, and how many more.
func missingText(paths []string, gone []int) (msg, code string, args []string) {
	first := paths[gone[0]]
	if len(gone) == 1 {
		return fmt.Sprintf("файл не найден: «%s»", first), "torrent.file_missing", []string{first}
	}
	more := strconv.Itoa(len(gone) - 1)
	return fmt.Sprintf("файлы не найдены: «%s» и ещё %s", first, more), "torrent.files_missing", []string{first, more}
}

// missingChecked ends a check of a torrent stopped for files gone (startRecheck calls it when a check ends): with the
// files all back it goes on as it was before they went missing, otherwise it waits for them again.
func (m *Manager) missingChecked(t *torrent.Torrent, hash string) {
	m.mu.Lock()
	e, ok := m.errs[hash]
	if !ok || e.kind != ErrKindMissing || !e.recheck {
		m.mu.Unlock()
		return
	}
	e.recheck = false
	m.errs[hash] = e
	m.mu.Unlock()
	info := t.Info()
	if info == nil {
		return
	}
	paths, err := store.Paths(m.saveDir(hash), info)
	if err != nil {
		return
	}
	for _, i := range e.files {
		if !onDisk(paths[i]) {
			return
		}
	}
	m.clearError(hash)
	if !e.paused {
		m.setPaused(t, hash, false)
	}
}

// takeMissing returns the files a torrent was stopped for, if it was stopped for files gone ("Resume" is about to clear
// the error and has them checked, see recheckGone).
func (m *Manager) takeMissing(hash string) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.errs[hash]; ok && e.kind == ErrKindMissing {
		return e.files
	}
	return nil
}

// recheckGone checks the files a resumed torrent was stopped for: what came back is had again, what did not is
// downloaded anew (while the check runs the torrent does not download, see syncPerm).
func (m *Manager) recheckGone(hash string, files []int) {
	if len(files) == 0 {
		return
	}
	if err := m.RecheckFiles(hash, files); err != nil && !errors.Is(err, ErrBusy) {
		fmt.Fprintf(os.Stderr, "checking the missing files of %s: %v\n", hash, err)
	}
}
