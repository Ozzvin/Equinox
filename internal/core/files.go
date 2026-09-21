package core

import (
	"fmt"

	"github.com/anacrolix/torrent"
)

// File priorities. The zero value is "normal" so records written before this feature
// existed keep downloading everything.
const (
	PrioSkip   int8 = -1
	PrioNormal int8 = 0
	PrioHigh   int8 = 1
)

// ParsePriority converts the API names to a priority.
func ParsePriority(s string) (int8, error) {
	switch s {
	case "skip":
		return PrioSkip, nil
	case "normal":
		return PrioNormal, nil
	case "high":
		return PrioHigh, nil
	}
	return 0, fmt.Errorf("%w: unknown priority %q", ErrInvalidInput, s)
}

// filePrios returns a copy of the persisted priorities of a torrent.
func (m *Manager) filePrios(hash string) []int8 {
	var out []int8
	m.state.view(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			out = append(out, r.FilePrios...)
		}
	})
	return out
}

func prioAt(prios []int8, i int) int8 {
	if i < len(prios) {
		return prios[i]
	}
	return PrioNormal
}

// applyFiles pushes the file priorities to the engine and starts the download. When the
// torrent asked for its first and last pieces to be prioritised, that is done here too, after
// the file priorities, which would otherwise override it.
// Skipped files are not requested at all.
func (m *Manager) applyFiles(t *torrent.Torrent, hash string) {
	if t.Info() == nil {
		return
	}
	prios := m.filePrios(hash)
	for i, f := range t.Files() {
		switch prioAt(prios, i) {
		case PrioSkip:
			f.SetPriority(torrent.PiecePriorityNone)
		case PrioHigh:
			f.SetPriority(torrent.PiecePriorityHigh)
		default:
			f.SetPriority(torrent.PiecePriorityNormal)
		}
	}
	if rec, ok := m.record(hash); ok {
		m.keepEdges(t, rec)
	}
}

// selection returns the size and completed bytes of the files that are not skipped.
func selection(t *torrent.Torrent, prios []int8) (size, done int64) {
	for i, f := range t.Files() {
		if prioAt(prios, i) == PrioSkip {
			continue
		}
		size += f.Length()
		done += f.BytesCompleted()
	}
	return size, done
}

// wantedPieces marks the pieces that belong to at least one file that is not skipped.
func wantedPieces(t *torrent.Torrent, prios []int8) []bool {
	w := make([]bool, t.NumPieces())
	for i, f := range t.Files() {
		if prioAt(prios, i) == PrioSkip {
			continue
		}
		for p := f.BeginPieceIndex(); p < f.EndPieceIndex() && p < len(w); p++ {
			w[p] = true
		}
	}
	return w
}

// SetFilePriorities sets the priority of the given files. Files that become wanted get
// their disk space reserved first; if the disk is too small nothing is changed.
func (m *Manager) SetFilePriorities(hash string, indexes []int, prio int8) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	if t.Info() == nil {
		return ErrNoMetadata
	}
	n := len(t.Files())
	for _, i := range indexes {
		if i < 0 || i >= n {
			return fmt.Errorf("%w: file index %d out of range", ErrInvalidInput, i)
		}
	}

	old := m.filePrios(hash)
	next := make([]int8, n)
	for i := range next {
		next[i] = prioAt(old, i)
	}
	for _, i := range indexes {
		next[i] = prio
	}

	// Reserve space for newly wanted files before committing.
	if err := m.preallocate(t, next); err != nil {
		return err
	}
	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.FilePrios = next
		}
	}); err != nil {
		return err
	}
	m.applyFiles(t, hash)
	return nil
}
