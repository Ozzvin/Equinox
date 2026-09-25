package core

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Ozzvin/equinox/internal/atomicfile"
)

// record is the persisted, per-torrent state that the engine itself does not keep.
type record struct {
	InfoHash string    `json:"infoHash"`
	Magnet   string    `json:"magnet,omitempty"`
	CopyPath string    `json:"copyPath,omitempty"` // saved .torrent copy, if any
	Added    time.Time `json:"added"`
	Order    int64     `json:"order"` // queue position, lower runs first (0 = fall back to Added)

	Paused         bool         `json:"paused"`
	Sequential     bool         `json:"sequential"`
	Label          string       `json:"label,omitempty"`         // free-form category, "" = none
	WasIncomplete  bool         `json:"wasIncomplete,omitempty"` // seen unfinished; set until the completion is handled
	SkipCheck      bool         `json:"-"`                       // only while adding: trust the files on disk
	pausedExplicit bool         `json:"-"`                       // only while adding: the caller chose paused or not, so the "add paused" setting does not apply
	EdgePieces     bool         `json:"edgePieces,omitempty"`    // raise the first and last pieces of every file
	MoveDone       string       `json:"moveDone,omitempty"`      // where to move this torrent when it finishes, overrides the global folder
	Prealloc       *bool        `json:"prealloc,omitempty"`      // per-torrent preallocation choice, nil = the global setting
	MaxConns       int          `json:"maxConns,omitempty"`      // connection limit of this torrent, 0 = the global setting
	ExtraTrackers  []string     `json:"extraTrackers,omitempty"` // announce URLs the user added
	SavePath       string       `json:"savePath,omitempty"`      // download folder of this torrent ("" = the default folder)
	Source         *sourceMeta  `json:"source,omitempty"`        // what the .torrent file said: comment, creator, date, publisher page
	rawFile        []byte       `json:"-"`                       // only while adding: the file as it was given
	ManualPeers    []manualPeer `json:"manualPeers,omitempty"`   // peers the user added by hand: tried again after a restart
	// A storage move that has started but not finished. It is written before the first file is
	// touched and cleared when the move ends, so a crash or a forced kill in the middle leaves a
	// trail for recoverMoves to follow instead of a half-copied tree nobody knows about.
	MoveFrom      string  `json:"moveFrom,omitempty"`      // the data tree being moved away from
	MoveTo        string  `json:"moveTo,omitempty"`        // where that tree is being copied to
	MoveDir       string  `json:"moveDir,omitempty"`       // the save folder the torrent gets when it succeeds
	RatioLimit    float64 `json:"ratioLimit"`              // 0 = use the global default
	SeedTimeLimit int     `json:"seedTimeLimit,omitempty"` // minutes of seeding after which it stops, 0 = the global default
	SeedSeconds   int64   `json:"seedSeconds,omitempty"`   // time spent seeding, adds up across restarts
	ActiveSeconds int64   `json:"activeSeconds,omitempty"` // time the torrent has been running (not paused), adds up across restarts

	// Per-file priority (see PrioSkip/PrioLow/PrioNormal/PrioHigh); missing entries mean normal.
	FilePrios []int8 `json:"filePrios,omitempty"`

	// Lifetime traffic, accumulated across daemon restarts (payload bytes).
	Downloaded int64 `json:"downloaded"`
	Uploaded   int64 `json:"uploaded"`
}

type state struct {
	Torrents map[string]*record `json:"torrents"`
	// Totals of torrents that were removed, so the global ratio survives removals.
	RemovedDownloaded int64 `json:"removedDownloaded"`
	RemovedUploaded   int64 `json:"removedUploaded"`
}

type stateStore struct {
	mu    sync.Mutex
	path  string
	st    state
	dirty bool // touched since the last save; flush writes it out
}

func loadState(path string) (*stateStore, error) {
	s := &stateStore{path: path, st: state{Torrents: map[string]*record{}}}
	var parsed state
	ok, fromBackup, err := atomicfile.ReadWithBackup(path, func(b []byte) error {
		parsed = state{}
		return json.Unmarshal(b, &parsed)
	})
	if err != nil {
		return nil, err
	}
	if ok {
		s.st = parsed
		if fromBackup {
			fmt.Fprintf(os.Stderr, "state.json was unusable, fell back to state.json.bak\n")
		}
	}
	if s.st.Torrents == nil {
		s.st.Torrents = map[string]*record{}
	}
	return s, nil
}

// with runs fn under the lock and writes the result to the disk before returning. It is for
// changes the user made and would notice losing: a label, a queue move, a torrent added or
// removed. Counters that merely tick along use touch instead.
func (s *stateStore) with(fn func(*state)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
	return s.saveLocked()
}

// touch runs fn under the lock and only marks the state as needing a save. It is for the
// counters the engine updates every second (traffic, seeding and running time): writing the
// whole file that often rewrote it several times a second and wore the disk for nothing.
// flush, called on a timer and at shutdown, does the actual write.
func (s *stateStore) touch(fn func(*state)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
	s.dirty = true
}

// flush writes the state if touch has marked it since the last save.
func (s *stateStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

// view runs fn under the lock without persisting.
func (s *stateStore) view(fn func(*state)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
}

func (s *stateStore) saveLocked() error {
	b, err := json.MarshalIndent(&s.st, "", "  ")
	if err != nil {
		return err
	}
	// Keep a backup: this one file holds every torrent record, so a state.json that cannot be
	// parsed after a crash would otherwise mean starting from an empty list.
	if err := atomicfile.Write(s.path, b, 0o644, true); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
