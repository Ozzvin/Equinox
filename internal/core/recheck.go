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

// A recheck goes piece by piece from the start of the torrent, the way the engine's own VerifyData does, but here,
// so that it knows exactly how far it got: the progress is the share of the torrent's bytes already checked, the
// Files tab can show each file's part of it, and the place is written to checks.json in the state folder. A check
// cut short by closing the program goes on from about that place at the next start, instead of from the beginning.

const checkSaveEvery = 5 * time.Second

// checkPlaces is what checks.json holds: for each torrent with a recheck under way, the first piece not yet checked.
type checkPlaces map[string]int

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

// saveCheckPlace writes (pos >= 0) or forgets (pos < 0) the place of one torrent's recheck.
func (m *Manager) saveCheckPlace(hash string, pos int) {
	checkFileMu.Lock()
	defer checkFileMu.Unlock()
	places := m.loadCheckPlaces()
	if pos < 0 {
		if _, ok := places[hash]; !ok {
			return
		}
		delete(places, hash)
	} else {
		places[hash] = pos
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

// checkedBytes is how many bytes of a torrent lie in its first pos pieces.
func checkedBytes(t *torrent.Torrent, pos int) int64 {
	info := t.Info()
	if info == nil {
		return 0
	}
	return min(int64(pos)*info.PieceLength, info.TotalLength())
}

// setCheckPos records how far a recheck got, for the status and the Files tab.
func (m *Manager) setCheckPos(t *torrent.Torrent, hash string, pos int) {
	total := int64(0)
	if info := t.Info(); info != nil {
		total = info.TotalLength()
	}
	m.mu.Lock()
	m.checkPos[hash] = pos
	if total > 0 {
		m.checkProg[hash] = float64(checkedBytes(t, pos)) / float64(total)
	} else {
		m.checkProg[hash] = 0
	}
	m.mu.Unlock()
}

// verifyFrom hashes the pieces of t from start on, keeping the place in checks.json. It returns ctx's error when it
// was stopped (the program closes, the torrent is removed), and the place is kept for the next start then.
func (m *Manager) verifyFrom(ctx context.Context, t *torrent.Torrent, hash string, start int) error {
	n := t.NumPieces()
	start = max(0, min(start, n))
	m.setCheckPos(t, hash, start)
	m.saveCheckPlace(hash, start)
	saved := time.Now()
	for i := start; i < n; i++ {
		if err := t.Piece(i).VerifyDataContext(ctx); err != nil {
			if ctx.Err() == nil { // the torrent closed under it: nothing to resume
				m.saveCheckPlace(hash, -1)
			} else {
				m.saveCheckPlace(hash, i)
			}
			return err
		}
		m.setCheckPos(t, hash, i+1)
		if time.Since(saved) >= checkSaveEvery {
			m.saveCheckPlace(hash, i+1)
			saved = time.Now()
		}
	}
	m.saveCheckPlace(hash, -1)
	return nil
}

// resumeChecks goes on with the rechecks that the last run of the program did not finish, each from where it got
// to, once its torrent has its info. Called once, after the torrents are back.
func (m *Manager) resumeChecks() {
	places := m.loadCheckPlaces()
	for hash, pos := range places {
		t, err := m.get(hash)
		if err != nil { // the torrent is gone
			m.saveCheckPlace(hash, -1)
			continue
		}
		go func(t *torrent.Torrent, hash string, pos int) {
			select {
			case <-t.GotInfo():
			case <-m.done:
				return
			}
			if err := m.startRecheck(t, hash, pos); err != nil {
				fmt.Fprintf(os.Stderr, "resuming the check of %s: %v\n", t.Name(), err)
			}
		}(t, hash, pos)
	}
}
