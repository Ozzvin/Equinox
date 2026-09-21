package core

import (
	"fmt"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// TorrentOptions are the settings a torrent can be given when it is added (the "Add torrents"
// dialog). Zero values mean "as in the settings".
type TorrentOptions struct {
	SavePath    string   `json:"savePath"`    // download folder, "" = the default one (or the label's)
	Label       string   `json:"label"`       //
	Paused      bool     `json:"paused"`      // add stopped
	Sequential  bool     `json:"sequential"`  // download in order (for playing while it downloads)
	EdgePieces  bool     `json:"edgePieces"`  // raise the first and last pieces of every file
	SkipCheck   bool     `json:"skipCheck"`   // take files already on disk as complete, without hashing them
	MoveDone    string   `json:"moveDone"`    // move here when finished, "" = the global setting
	Preallocate *bool    `json:"preallocate"` // reserve the disk space at once, nil = the global setting
	MaxConns    int      `json:"maxConns"`    // peer connections, 0 = the global setting
	Files       []string `json:"files"`       // per file: "skip", "low", "normal" or "high" (torrent files only)
}

// WithOptions applies all the options to the record of a torrent being added. The values are
// checked when the torrent is added (see resolveSaveDir); file priorities are converted by
// filePriorities.
func WithOptions(o TorrentOptions) AddOption {
	return func(r *record) {
		r.SavePath, r.Label, r.Paused = o.SavePath, o.Label, o.Paused
		r.pausedExplicit = true
		r.Sequential, r.EdgePieces, r.SkipCheck = o.Sequential, o.EdgePieces, o.SkipCheck
		r.MoveDone, r.Prealloc, r.MaxConns = o.MoveDone, o.Preallocate, o.MaxConns
	}
}

// WithFilePriorities sets the per-file priorities of a torrent whose files are known.
func WithFilePriorities(p []int8) AddOption { return func(r *record) { r.FilePrios = p } }

// filePriorities converts the names in TorrentOptions.Files, checking the count against the
// torrent's number of files (0 = unknown, as for a magnet link: the list must then be empty).
func filePriorities(names []string, nFiles int) ([]int8, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if nFiles == 0 {
		return nil, fmt.Errorf("%w: file priorities need a torrent whose files are known", ErrInvalidInput)
	}
	if len(names) != nFiles {
		return nil, fmt.Errorf("%w: %d file priorities for %d files", ErrInvalidInput, len(names), nFiles)
	}
	out := make([]int8, len(names))
	allNormal := true
	for i, n := range names {
		p, err := ParsePriority(n)
		if err != nil {
			return nil, err
		}
		out[i] = p
		allNormal = allNormal && p == PrioNormal
	}
	if allNormal {
		return nil, nil // nothing to remember
	}
	return out, nil
}

// checkOptions validates what resolveSaveDir does not: numbers and folders of the extras.
func (m *Manager) checkOptions(rec *record) error {
	if rec.MaxConns < 0 || rec.MaxConns > 1000 {
		return errInvalid("connections must be between 0 and 1000")
	}
	if d := strings.TrimSpace(rec.MoveDone); d != "" {
		abs, err := ensureDir(d)
		if err != nil {
			return err
		}
		rec.MoveDone = abs
	} else {
		rec.MoveDone = ""
	}
	return nil
}

// trustExisting tells the storage to take the files already on disk as complete when the
// torrent is opened, instead of hashing them.
func (m *Manager) trustExisting(hash string) {
	if h, ok := parseHash(hash); ok {
		m.store.Trust(metainfo.Hash(h))
	}
}

// preallocEnabled tells whether disk space is reserved for this torrent: its own choice, or
// else the global setting.
func (m *Manager) preallocEnabled(hash string) bool {
	if rec, ok := m.record(hash); ok && rec.Prealloc != nil {
		return *rec.Prealloc
	}
	return m.cfg.Get().Preallocate
}

// applyAddPaused makes a torrent that was added without a say on it start paused when the settings ask
// for that ("add torrents paused"). The "Add torrents" dialog always says (its box shows the setting).
func (m *Manager) applyAddPaused(rec *record) {
	if !rec.pausedExplicit && m.cfg.Get().Add.Paused {
		rec.Paused = true
	}
}
