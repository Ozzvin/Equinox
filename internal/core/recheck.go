package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/Ozzvin/equinox/internal/atomicfile"
)

// A recheck goes piece by piece, the way the engine's own VerifyData does, but here, so that it knows exactly how far
// it got: the progress is the share of the bytes to check already checked, the Files tab can show each file's part of
// it, and the place is written to checks.json in the state folder. A check cut short by closing the program goes on
// from about that place at the next start, instead of from the beginning. A check of chosen files goes only through
// the pieces those files lie in (a piece shared with a file not chosen is checked too, it is the same piece).

const checkSaveEvery = 5 * time.Second

// checkPlace is one torrent's recheck under way: the files it checks (none: the whole torrent) and how many of its
// pieces are done.
type checkPlace struct {
	Pos   int   `json:"pos"`
	Files []int `json:"files,omitempty"`
}

// UnmarshalJSON also takes the older form of checks.json, where a place was just the number of pieces done.
func (c *checkPlace) UnmarshalJSON(b []byte) error {
	var n int
	if json.Unmarshal(b, &n) == nil {
		*c = checkPlace{Pos: n}
		return nil
	}
	type plain checkPlace
	return json.Unmarshal(b, (*plain)(c))
}

// checkPlaces is what checks.json holds, by torrent.
type checkPlaces map[string]checkPlace

var checkFileMu sync.Mutex // one writer of checks.json at a time

func (m *Manager) checksPath() string { return filepath.Join(m.stateDir, "checks.json") }

func (m *Manager) loadCheckPlaces() checkPlaces {
	out := checkPlaces{}
	b, err := os.ReadFile(m.checksPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

// saveCheckPlace writes (pos >= 0) or forgets (pos < 0) the place of one torrent's recheck of files (nil: all).
func (m *Manager) saveCheckPlace(hash string, pos int, files []int) {
	checkFileMu.Lock()
	defer checkFileMu.Unlock()
	places := m.loadCheckPlaces()
	if pos < 0 {
		if _, ok := places[hash]; !ok {
			return
		}
		delete(places, hash)
	} else {
		places[hash] = checkPlace{Pos: pos, Files: files}
	}
	if len(places) == 0 {
		_ = os.Remove(m.checksPath())
		return
	}
	b, err := json.Marshal(places)
	if err != nil {
		return
	}
	if err := atomicfile.Write(m.checksPath(), b, 0o644, false); err != nil {
		fmt.Fprintf(os.Stderr, "saving the place of a check: %v\n", err)
	}
}

// checkPieces is the pieces a check of the given files goes through, in order: those the files lie in (files out of
// range are left out); no files means every piece.
func checkPieces(t *torrent.Torrent, files []int) []int {
	n := t.NumPieces()
	if len(files) == 0 {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	fs := t.Files()
	want := make([]bool, n)
	for _, i := range files {
		if i < 0 || i >= len(fs) {
			continue
		}
		for p := fs[i].BeginPieceIndex(); p < fs[i].EndPieceIndex() && p < n; p++ {
			want[p] = true
		}
	}
	var out []int
	for p, w := range want {
		if w {
			out = append(out, p)
		}
	}
	return out
}

// pieceBytes is the length of piece i of t.
func pieceBytes(t *torrent.Torrent, i int) int64 {
	info := t.Info()
	if info == nil {
		return 0
	}
	return info.Piece(i).Length()
}

// setCheckPos records how far a recheck got (pos of its pieces are done), for the status and the Files tab.
func (m *Manager) setCheckPos(t *torrent.Torrent, hash string, pieces []int, pos int) {
	var done, total int64
	for k, p := range pieces {
		b := pieceBytes(t, p)
		total += b
		if k < pos {
			done += b
		}
	}
	m.mu.Lock()
	m.checkPos[hash] = pos
	if total > 0 {
		m.checkProg[hash] = float64(done) / float64(total)
	} else {
		m.checkProg[hash] = 0
	}
	m.mu.Unlock()
}

// verifyFrom hashes the pieces of the given files of t (nil: all), from the start-th of them on, keeping the place in
// checks.json. It returns ctx's error when it was stopped (the program closes, the torrent is removed), and the place
// is kept for the next start then.
func (m *Manager) verifyFrom(ctx context.Context, t *torrent.Torrent, hash string, files []int, start int) error {
	pieces := checkPieces(t, files)
	n := len(pieces)
	start = max(0, min(start, n))
	m.mu.Lock()
	m.checkSel[hash] = checkSel{files: files, pieces: pieces}
	m.mu.Unlock()
	m.setCheckPos(t, hash, pieces, start)
	m.saveCheckPlace(hash, start, files)
	saved := time.Now()
	for k := start; k < n; k++ {
		if err := t.Piece(pieces[k]).VerifyDataContext(ctx); err != nil {
			if ctx.Err() == nil { // the torrent closed under it: nothing to resume
				m.saveCheckPlace(hash, -1, nil)
			} else {
				m.saveCheckPlace(hash, k, files)
			}
			return err
		}
		m.setCheckPos(t, hash, pieces, k+1)
		if time.Since(saved) >= checkSaveEvery {
			m.saveCheckPlace(hash, k+1, files)
			saved = time.Now()
		}
	}
	m.saveCheckPlace(hash, -1, nil)
	return nil
}

// checkSel is what a recheck of ours goes through: the files asked for (nil: all) and their pieces, in order.
type checkSel struct {
	files  []int
	pieces []int
}

// resumeChecks goes on with the rechecks that the last run of the program did not finish, each from where it got
// to, once its torrent has its info. Called once, after the torrents are back.
func (m *Manager) resumeChecks() {
	places := m.loadCheckPlaces()
	for hash, pl := range places {
		t, err := m.get(hash)
		if err != nil { // the torrent is gone
			m.saveCheckPlace(hash, -1, nil)
			continue
		}
		go func(t *torrent.Torrent, hash string, pl checkPlace) {
			select {
			case <-t.GotInfo():
			case <-m.done:
				return
			}
			if err := m.startRecheck(t, hash, pl.Files, pl.Pos); err != nil {
				fmt.Fprintf(os.Stderr, "resuming the check of %s: %v\n", t.Name(), err)
			}
		}(t, hash, pl)
	}
}
