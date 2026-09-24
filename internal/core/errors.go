package core

import (
	"fmt"
	"os"

	"github.com/anacrolix/torrent"
)

// Kinds of failure that stop a torrent.
const (
	ErrKindDiskFull = "disk_full"
	ErrKindWrite    = "write"
	ErrKindOther    = "other"
)

type torrentError struct{ kind, msg string }

// diskFree reports the free space of the drive holding dir. It is a variable so tests can
// pretend that the disk is full.
var diskFree = freeSpace

func (m *Manager) setError(hash, kind, msg string) {
	m.mu.Lock()
	m.errs[hash] = torrentError{kind: kind, msg: msg}
	m.mu.Unlock()
}

func (m *Manager) clearError(hash string) {
	m.mu.Lock()
	delete(m.errs, hash)
	m.mu.Unlock()
}

// clearSetupError forgets a failure of the disk preparation (no room, an allocation that failed), but keeps a
// write error: begin runs in the background after the torrent is added, and the engine can report a failing write
// while it is still preparing the disk; a finished preparation must not wipe that report.
func (m *Manager) clearSetupError(hash string) {
	m.mu.Lock()
	if e, ok := m.errs[hash]; ok && e.kind != ErrKindWrite {
		delete(m.errs, hash)
	}
	m.mu.Unlock()
}

// watchWrites makes a failing write to disk visible: the engine stops the download on its
// own but says nothing, so the user would just see a torrent that does not move.
func (m *Manager) watchWrites(t *torrent.Torrent, hash string) {
	t.SetOnWriteChunkError(func(err error) { m.onWriteError(t, hash, err) })
}

func (m *Manager) onWriteError(t *torrent.Torrent, hash string, err error) {
	m.mu.Lock()
	_, already := m.errs[hash]
	m.mu.Unlock()
	if already {
		return // one report per failure; the engine may call this for every chunk
	}
	fmt.Fprintf(os.Stderr, "write error in %s: %v\n", t.Name(), err)
	m.setError(hash, ErrKindWrite, err.Error())
	m.setPaused(t, hash, true)
}

// errInvalid builds an ErrInvalidInput error with a message.
func errInvalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, a...)...)
}
