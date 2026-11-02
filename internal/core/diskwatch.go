package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

// Confirm a failure three times over at least two seconds, forgetting it on success.
type diskWatcher struct {
	stat    func(string) (os.FileInfo, error)
	dir     func(string) error
	now     func() time.Time
	mu      sync.Mutex // leaf: never call Manager or the engine with it held
	suspect map[string]diskSuspicion
	writes  map[string]time.Time
}
type diskSuspicion struct {
	since, last time.Time
	count       int
}

func newDiskWatcher() *diskWatcher {
	return &diskWatcher{stat: os.Stat, dir: readableDirectory, now: time.Now, suspect: make(map[string]diskSuspicion), writes: make(map[string]time.Time)}
}

func (w *diskWatcher) noteWrite(hash string) {
	w.mu.Lock()
	w.writes[hash] = w.now()
	w.mu.Unlock()
	w.failed(hash) // count actual failed writes, not repeated observations of one failure
}

func (w *diskWatcher) confirmed(hash string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	x := w.suspect[hash]
	return x.count >= 3 && w.now().Sub(x.since) >= 2*time.Second
}

func (w *diskWatcher) recentWriteFailure(hash string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	at, ok := w.writes[hash]
	return ok && w.now().Sub(at) < 6*time.Second
}

func readableDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (w *diskWatcher) failed(hash string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	x := w.suspect[hash]
	if x.count == 0 {
		x.since = now
	}
	if x.count == 0 || now.Sub(x.last) >= time.Second {
		x.count++
		x.last = now
	}
	w.suspect[hash] = x
	return x.count >= 3 && now.Sub(x.since) >= 2*time.Second
}

func (w *diskWatcher) healthy(hash string) {
	w.mu.Lock()
	delete(w.suspect, hash)
	delete(w.writes, hash)
	w.mu.Unlock()
}

func recoverableStorage(kind string) bool {
	return kind == ErrKindMissing || kind == ErrKindUnavailable
}

func (m *Manager) storageResumeError(hash string) error {
	m.mu.Lock()
	e, failed := m.errs[hash]
	watch := m.diskWatch
	m.mu.Unlock()
	if failed && e.kind == ErrKindUnavailable && watch.dir(m.saveDir(hash)) != nil {
		return &CodedError{Code: e.code, Args: e.args, Msg: e.msg}
	}
	return nil
}

func (m *Manager) rememberUserPause(hash string) {
	m.mu.Lock()
	if e, ok := m.errs[hash]; ok && recoverableStorage(e.kind) {
		e.paused = true
		m.errs[hash] = e
	}
	m.mu.Unlock()
}

func (m *Manager) diskUnavailable(t *torrent.Torrent, hash, path string, r record, files []int) {
	e := torrentError{kind: ErrKindUnavailable, files: files, paused: r.Paused,
		code: "torrent.storage_unavailable", args: []string{path}, msg: fmt.Sprintf("Диск или папка недоступны: «%s». Проверьте подключение и доступ к папке.", path)}
	if buildinfo.Packaging == "umbrel" {
		e.code = "torrent.storage_unavailable_umbrel"
		e.msg += " Если диск подключили заново, перезапустите приложение Equinox."
	}
	m.mu.Lock()
	_, already := m.errs[hash]
	if old, ok := m.errs[hash]; ok && recoverableStorage(old.kind) {
		e.paused = old.paused
	}
	m.errs[hash] = e
	m.mu.Unlock()
	if !already {
		fmt.Fprintln(os.Stderr, e.msg)
	}
	if !r.Paused {
		m.setPaused(t, hash, true)
	}
}
