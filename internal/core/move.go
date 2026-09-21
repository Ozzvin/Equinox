package core

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
)

// ErrBusy is returned for a torrent whose files are being moved.
var ErrBusy = errors.New("torrent is being moved")

// moveJob tracks one storage move. Its fields are guarded by Manager.mu, except done.
type moveJob struct {
	name      string
	selSize   int64 // size of the wanted files, for the placeholder row in the list
	copySize  int64 // bytes to copy when the move crosses drives, 0 for a plain rename
	done      atomic.Int64
	running   bool
	err       string    // last failure, kept until the next move of this torrent
	warning   string    // the move worked but something was left behind
	updatedAt time.Time // when the job last changed state
}

func (j *moveJob) fraction() float64 {
	if j.copySize <= 0 {
		return 0
	}
	f := float64(j.done.Load()) / float64(j.copySize)
	if f > 1 {
		f = 1
	}
	return f
}

func (m *Manager) isMoving(hash string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.moves[hash]
	return j != nil && j.running
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return strings.EqualFold(a, b)
}

// isInside reports whether child is the same as or below parent.
func isInside(child, parent string) bool {
	child, parent = strings.ToLower(filepath.Clean(child)), strings.ToLower(filepath.Clean(parent))
	return child == parent || strings.HasPrefix(child, parent+string(filepath.Separator))
}

// MoveStorage moves the downloaded files of a torrent to another folder and keeps
// seeding from there. The check of the target is immediate; the move itself runs in the
// background (watch Status.Moving). On any failure the files stay where they were.
func (m *Manager) MoveStorage(hash, dir string) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	if t.Info() == nil {
		return ErrNoMetadata
	}
	target, err := ensureDir(dir)
	if err != nil {
		return err
	}
	cur := m.saveDir(hash)
	if samePath(target, cur) {
		return nil
	}
	name := t.Info().BestName()
	src, dst := filepath.Join(cur, name), filepath.Join(target, name)
	if isInside(dst, src) || isInside(src, dst) {
		return fmt.Errorf("%w: the folders overlap", ErrInvalidInput)
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %q already exists in the target folder", ErrInvalidInput, name)
	}

	rec := m.snapshotRecords()[hash]
	sel, _ := selection(t, rec.FilePrios)
	job := &moveJob{name: t.Name(), selSize: sel, running: true, updatedAt: time.Now()}

	m.mu.Lock()
	if j := m.moves[hash]; j != nil && j.running {
		m.mu.Unlock()
		return ErrBusy
	}
	m.moves[hash] = job
	m.mu.Unlock()

	go m.runMove(hash, t, src, dst, target, cur, job)
	return nil
}

func (m *Manager) runMove(hash string, t *torrent.Torrent, src, dst, target, old string, job *moveJob) {
	m.syncCounters() // fold traffic counters into the record before the engine forgets them
	m.dropForMove(hash, t)

	newDir := target
	var moveErr, leftover error
	if _, err := os.Lstat(src); err == nil {
		moveErr, leftover = m.moveTree(src, dst, job)
	} // else nothing was downloaded yet: only the folder changes
	if moveErr != nil {
		newDir = old // data is still (or again) in the old folder
	}

	_ = m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.SavePath = newDir
		}
	})
	if err := m.restoreByHash(hash); err != nil {
		moveErr = errors.Join(moveErr, fmt.Errorf("re-adding after the move: %w", err))
	}

	m.mu.Lock()
	job.running = false
	job.updatedAt = time.Now()
	if moveErr != nil {
		job.err = moveErr.Error()
	} else {
		job.err = ""
		delete(m.moves, hash) // success: nothing to show
	}
	if leftover != nil {
		job.warning = "files were copied, but the old ones could not be deleted: " + leftover.Error()
		m.moves[hash] = job
	}
	m.mu.Unlock()
}

// dropForMove releases the engine's hold on the torrent's files.
func (m *Manager) dropForMove(hash string, t *torrent.Torrent) {
	h, _ := parseHash(hash)
	t.Drop()
	m.mu.Lock()
	delete(m.torrents, h)
	delete(m.seenDown, h)
	delete(m.seenUp, h)
	delete(m.perm, h)
	delete(m.queued, h)
	m.mu.Unlock()
}

func (m *Manager) restoreByHash(hash string) error {
	rec, ok := m.snapshotRecords()[hash]
	if !ok {
		return ErrNotFound
	}
	return m.restore(&rec)
}

// moveTree moves src to dst. A rename is tried first (instant within one drive); if that
// is impossible the tree is copied and the source removed afterwards. It returns the
// error of the move itself and, separately, a failure to delete the source after a
// successful copy (the data is then safe in the new place).
func (m *Manager) moveTree(src, dst string, job *moveJob) (moveErr, leftover error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err, nil
	}
	// The engine may still hold files for a moment after the torrent was dropped.
	var renameErr error
	for i := 0; i < 6; i++ {
		if renameErr = os.Rename(src, dst); renameErr == nil {
			return nil, nil
		}
		if _, err := os.Lstat(src); err != nil { // the source vanished: nothing left to move
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Different drive (or the rename was refused): copy.
	need, err := treeSize(src)
	if err != nil {
		return err, nil
	}
	if free, err := freeSpace(filepath.Dir(dst)); err == nil && uint64(need) > free {
		return fmt.Errorf("%w: moving needs %d bytes, %d free in the target folder", ErrNoDiskSpace, need, free), nil
	}
	m.mu.Lock()
	job.copySize = need
	m.mu.Unlock()

	if err := copyTree(src, dst, &job.done); err != nil {
		_ = removeAll(dst) // never leave half a copy behind
		return fmt.Errorf("copy failed (%v after rename failed: %v)", err, renameErr), nil
	}
	return nil, removeAll(src)
}

func treeSize(root string) (int64, error) {
	var n int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			n += fi.Size()
		}
		return nil
	})
	return n, err
}

func copyTree(src, dst string, done *atomic.Int64) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		return copyFile(p, out, done)
	})
}

func copyFile(src, dst string, done *atomic.Int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
			done.Add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	return out.Close()
}
