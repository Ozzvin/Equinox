package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ozzvin/equinox/internal/config"
)

// ensureDir makes sure dir exists, is a directory and can be written to, and returns
// its absolute path.
func ensureDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%w: folder %q: %v", ErrInvalidInput, dir, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("%w: cannot create folder %q: %v", ErrInvalidInput, abs, err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: %q is not a folder", ErrInvalidInput, abs)
	}
	probe, err := os.CreateTemp(abs, ".equinox-write-test-*")
	if err != nil {
		return "", fmt.Errorf("%w: folder %q is not writable: %v", ErrInvalidInput, abs, err)
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return abs, nil
}

// saveDir returns the download folder of a torrent: its own folder if it has one,
// otherwise the default folder (which is what torrents from older versions use).
func (m *Manager) saveDir(hash string) string {
	var p string
	m.state.view(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			p = r.SavePath
		}
	})
	if p == "" {
		p = m.cfg.Get().DataDir
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// A label's folder is where its torrents end up, as qBittorrent's categories: with finished torrents moved (a folder of
// their own or the general one), a torrent is downloaded into the general download folder and moved to its label's
// folder when it finishes; without moving, it is downloaded straight into the label's folder.

// labelDir is the folder of a label, "" when it has none.
func labelDir(s config.Settings, label string) string {
	if label == "" {
		return ""
	}
	return strings.TrimSpace(s.LabelPaths[label])
}

// movesWhenDone tells whether a finished torrent is moved: not switched off for it, and a folder for it to go to (its
// own or the general one).
func movesWhenDone(s config.Settings, r *record) bool {
	return !r.MoveDoneOff && (r.MoveDone != "" || strings.TrimSpace(s.MoveCompletedDir) != "")
}

// doneDir is where a finished torrent is moved, "" for nowhere: its own folder, else its label's folder, else the
// general folder for finished torrents.
func doneDir(s config.Settings, r *record) string {
	if !movesWhenDone(s, r) {
		return ""
	}
	if r.MoveDone != "" {
		return r.MoveDone
	}
	if l := labelDir(s, r.Label); l != "" {
		return l
	}
	return strings.TrimSpace(s.MoveCompletedDir)
}

// resolveSaveDir fills in the record's folder for a torrent that is being added: the folder asked for, else the
// label's folder when the torrent is not moved when finished (when it is, it goes there at the end), else the default
// one. The folder is created.
func (m *Manager) resolveSaveDir(rec *record) error {
	if err := m.checkOptions(rec); err != nil {
		return err
	}
	rec.Label = strings.TrimSpace(rec.Label)
	if len([]rune(rec.Label)) > MaxLabelLen {
		return fmt.Errorf("%w: label is longer than %d characters", ErrInvalidInput, MaxLabelLen)
	}
	s := m.cfg.Get()
	dir := strings.TrimSpace(rec.SavePath)
	if dir == "" && !movesWhenDone(s, rec) {
		dir = labelDir(s, rec.Label)
	}
	if dir == "" {
		dir = s.DataDir
	}
	abs, err := ensureDir(dir)
	if err != nil {
		return err
	}
	rec.SavePath = abs
	return nil
}

// FoldersUpdate is a change of the global folders.
type FoldersUpdate struct {
	DataDir        string
	TorrentCopyDir string // empty switches copies off
	// MoveCompletedDir is where finished downloads are moved; empty switches it off.
	MoveCompletedDir string
	// WatchDir is scanned for .torrent files to add; empty switches it off.
	WatchDir   string
	LabelPaths map[string]string
}

// SetFolders changes where new torrents are saved and where .torrent copies go. Torrents
// that are already added keep their folders. Every folder is created and checked first,
// and nothing is changed if one of them is unusable.
func (m *Manager) SetFolders(u FoldersUpdate) error {
	data, err := ensureDir(u.DataDir)
	if err != nil {
		return err
	}
	copyDir := strings.TrimSpace(u.TorrentCopyDir)
	if copyDir != "" {
		if copyDir, err = ensureDir(copyDir); err != nil {
			return err
		}
	}
	moveDir := strings.TrimSpace(u.MoveCompletedDir)
	if moveDir != "" {
		if moveDir, err = ensureDir(moveDir); err != nil {
			return err
		}
	}
	watchDir := strings.TrimSpace(u.WatchDir)
	if watchDir != "" {
		if watchDir, err = ensureDir(watchDir); err != nil {
			return err
		}
	}
	labels := map[string]string{}
	for l, p := range u.LabelPaths {
		l, p = strings.TrimSpace(l), strings.TrimSpace(p)
		if l == "" {
			continue
		}
		if len([]rune(l)) > MaxLabelLen {
			return fmt.Errorf("%w: label %q is longer than %d characters", ErrInvalidInput, l, MaxLabelLen)
		}
		if p == "" { // a label without a folder: it exists, new torrents go to the general folder
			labels[l] = ""
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("%w: folder %q: %v", ErrInvalidInput, p, err)
		}
		labels[l] = abs // created when first used
	}
	return m.cfg.Update(func(s *config.Settings) {
		s.DataDir, s.TorrentCopyDir, s.LabelPaths, s.MoveCompletedDir = data, copyDir, labels, moveDir
		s.WatchDir = watchDir
	})
}

// SetListenPort stores the port for the next start. The engine keeps listening on its
// current port until the application is restarted.
func (m *Manager) SetListenPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidInput)
	}
	return m.cfg.Update(func(s *config.Settings) { s.ListenPort = port })
}
