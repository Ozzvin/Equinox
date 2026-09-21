package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// record is the persisted, per-torrent state that the engine itself does not keep.
type record struct {
	InfoHash string    `json:"infoHash"`
	Magnet   string    `json:"magnet,omitempty"`
	CopyPath string    `json:"copyPath,omitempty"` // saved .torrent copy, if any
	Added    time.Time `json:"added"`
	Order    int64     `json:"order"` // queue position, lower runs first (0 = fall back to Added)

	Paused         bool     `json:"paused"`
	Sequential     bool     `json:"sequential"`
	Label          string   `json:"label,omitempty"`         // free-form category, "" = none
	WasIncomplete  bool     `json:"wasIncomplete,omitempty"` // seen unfinished; set until the completion is handled
	SkipCheck      bool     `json:"-"`                       // only while adding: trust the files on disk
	pausedExplicit bool     `json:"-"`                       // only while adding: the caller chose paused or not, so the "add paused" setting does not apply
	EdgePieces     bool     `json:"edgePieces,omitempty"`    // raise the first and last pieces of every file
	MoveDone       string   `json:"moveDone,omitempty"`      // where to move this torrent when it finishes, overrides the global folder
	Prealloc       *bool    `json:"prealloc,omitempty"`      // per-torrent preallocation choice, nil = the global setting
	MaxConns       int      `json:"maxConns,omitempty"`      // connection limit of this torrent, 0 = the global setting
	ExtraTrackers  []string `json:"extraTrackers,omitempty"` // announce URLs the user added
	SavePath       string   `json:"savePath,omitempty"`      // download folder of this torrent ("" = the default folder)
	RatioLimit     float64  `json:"ratioLimit"`              // 0 = use the global default
	SeedTimeLimit  int      `json:"seedTimeLimit,omitempty"` // minutes of seeding after which it stops, 0 = the global default
	SeedSeconds    int64    `json:"seedSeconds,omitempty"`   // time spent seeding, adds up across restarts

	// Per-file priority (see PrioSkip/PrioNormal/PrioHigh); missing entries mean normal.
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
	mu   sync.Mutex
	path string
	st   state
}

func loadState(path string) (*stateStore, error) {
	s := &stateStore{path: path, st: state{Torrents: map[string]*record{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.st); err != nil {
		return nil, err
	}
	if s.st.Torrents == nil {
		s.st.Torrents = map[string]*record{}
	}
	return s, nil
}

// with runs fn under the lock and persists afterwards.
func (s *stateStore) with(fn func(*state)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
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
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
