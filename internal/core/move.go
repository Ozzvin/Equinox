package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/Ozzvin/equinox/internal/store"
)

// ErrBusy is returned for a torrent whose files are being moved.
var ErrBusy = &CodedError{Code: "torrent.busy", Msg: "раздача сейчас перемещается, подождите"}

// moveJob tracks one storage move. Its fields are guarded by Manager.mu, except done.
type moveJob struct {
	name      string
	selSize   int64 // size of the wanted files, for the placeholder row in the list
	copySize  int64 // bytes to copy when the move crosses drives, 0 for a plain rename
	done      atomic.Int64
	queued    bool   // waiting for a free slot (MaxConcurrentMoves); the torrent is untouched meanwhile
	reqDir    string // the folder asked for, used to start the move once queued runs its turn
	running   bool
	err       string // last failure, kept until the next move of this torrent
	errCode   string // its code and arguments (see Coder), "" if it has none
	errArgs   []string
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
	return j != nil && (j.running || j.queued)
}

// activeMoves counts the moves actually copying/renaming right now (not the ones waiting in line).
// Must be called with m.mu held.
func (m *Manager) activeMovesLocked() int {
	n := 0
	for _, j := range m.moves {
		if j.running {
			n++
		}
	}
	return n
}

// pathKey is a path as the file system tells paths apart: Windows ignores the case of letters, Linux does not
// (/data/Movies and /data/movies are two folders there).
func pathKey(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func samePath(a, b string) bool { return pathKey(a) == pathKey(b) }

// isInside reports whether child is the same as or below parent.
func isInside(child, parent string) bool {
	child, parent = pathKey(child), pathKey(parent)
	return child == parent || strings.HasPrefix(child, parent+string(filepath.Separator))
}

// resolveMove validates a move and works out its paths without touching anything. samePath
// means there is nothing to do: the caller should treat that as success, not as an error.
func (m *Manager) resolveMove(hash, dir string) (t *torrent.Torrent, src, dst, target, cur string, same bool, err error) {
	t, err = m.get(hash)
	if err != nil {
		return
	}
	if t.Info() == nil {
		err = ErrNoMetadata
		return
	}
	target, err = ensureDir(dir)
	if err != nil {
		return
	}
	cur = m.saveDir(hash)
	if samePath(target, cur) {
		same = true
		return
	}
	name := store.RootName(t.Info())
	if name == "" { // no folder of its own: its files go one by one, from one save folder to the other (moveFiles)
		files, ferr := filesToMove(t.Info(), cur, target)
		if ferr != nil {
			err = fmt.Errorf("%w: %v", ErrInvalidInput, ferr)
			return
		}
		for _, f := range files {
			if _, e := os.Lstat(f.to); e == nil {
				err = fmt.Errorf("%w: %q already exists in the target folder", ErrInvalidInput, f.to)
				return
			}
		}
		src, dst = cur, target
		return
	}
	src, dst = filepath.Join(cur, name), filepath.Join(target, name)
	if isInside(dst, src) || isInside(src, dst) {
		err = fmt.Errorf("%w: the folders overlap", ErrInvalidInput)
		return
	}
	if _, e := os.Lstat(dst); e == nil {
		err = fmt.Errorf("%w: %q already exists in the target folder", ErrInvalidInput, name)
		return
	}
	return
}

// MoveStorage moves the downloaded files of a torrent to another folder and keeps seeding
// from there. The check of the target is immediate. The move itself, if a slot is free under
// MaxConcurrentMoves, starts in the background at once (watch Status.Moving); otherwise it
// waits in line, and the torrent is left untouched until its turn comes, same as a torrent
// that has not been asked to move at all. On any failure the files stay where they were.
func (m *Manager) MoveStorage(hash, dir string) error {
	t, src, dst, target, cur, same, err := m.resolveMove(hash, dir)
	if err != nil || same {
		return err
	}
	rec := m.snapshotRecords()[hash]
	sel, _ := selection(t, rec.FilePrios)
	job := &moveJob{name: t.Name(), selSize: sel, reqDir: dir, updatedAt: time.Now()}

	m.mu.Lock()
	if j := m.moves[hash]; j != nil && (j.running || j.queued) {
		m.mu.Unlock()
		return ErrBusy
	}
	if m.moveSlotFreeLocked() {
		job.running = true
		m.moves[hash] = job
		m.mu.Unlock()
		return m.beginMove(hash, t, src, dst, target, cur, job)
	}
	job.queued = true
	m.moves[hash] = job
	m.moveQueue = append(m.moveQueue, hash)
	m.mu.Unlock()
	return nil
}

// moveSlotFreeLocked reports whether a move can start right now. Must be called with m.mu held.
func (m *Manager) moveSlotFreeLocked() bool {
	limit := m.cfg.Get().MaxConcurrentMoves
	return limit <= 0 || m.activeMovesLocked() < limit
}

// beginMove writes the crash-recovery trail and starts the background copy/rename. job.running
// must already be true and job already in m.moves.
func (m *Manager) beginMove(hash string, t *torrent.Torrent, src, dst, target, cur string, job *moveJob) error {
	// Write the trail before the first file is touched: if the process dies during the copy,
	// recoverMoves finds the half-written destination on the next start (see runMove, which
	// clears it, and recoverMoves, which cleans up after it).
	files := t.Info() != nil && store.RootName(t.Info()) == ""
	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.MoveFrom, r.MoveTo, r.MoveDir, r.MoveFiles = src, dst, target, files
		}
	}); err != nil {
		m.mu.Lock()
		job.running = false
		delete(m.moves, hash)
		m.mu.Unlock()
		return err
	}
	go m.runMove(hash, t, src, dst, target, cur, job)
	return nil
}

// runMoveQueue starts as many queued moves as fit under MaxConcurrentMoves. Called once a
// tick from the main loop, and right after a move finishes so a freed slot is used at once.
func (m *Manager) runMoveQueue() {
	for {
		if m.ctx.Err() != nil { // closing: the queued moves wait for the next start
			return
		}
		m.mu.Lock()
		if len(m.moveQueue) == 0 || !m.moveSlotFreeLocked() {
			m.mu.Unlock()
			return
		}
		hash := m.moveQueue[0]
		m.moveQueue = m.moveQueue[1:]
		job := m.moves[hash]
		if job == nil || !job.queued { // removed or already handled meanwhile
			m.mu.Unlock()
			continue
		}
		m.mu.Unlock()

		t, src, dst, target, cur, same, err := m.resolveMove(hash, job.reqDir)
		m.mu.Lock()
		switch {
		case err != nil:
			job.queued = false
			job.err = err.Error()
			job.updatedAt = time.Now()
		case same: // the torrent is already where it should be: nothing to do
			job.queued = false
			delete(m.moves, hash)
		default:
			job.queued = false
			job.running = true
		}
		m.mu.Unlock()
		if err == nil && !same {
			if err := m.beginMove(hash, t, src, dst, target, cur, job); err != nil {
				m.mu.Lock()
				job.err = err.Error()
				m.mu.Unlock()
			}
		}
	}
}

func (m *Manager) runMove(hash string, t *torrent.Torrent, src, dst, target, old string, job *moveJob) {
	defer func() {
		if r := recover(); r != nil { // the trail is still there: the next start finishes or undoes the move (recoverMoves)
			survivePanic("moving "+hash, r)
			m.mu.Lock()
			job.running = false
			job.err = fmt.Sprintf("the move broke off (%v): restart the program, it finishes the move or undoes it", r)
			job.updatedAt = time.Now()
			m.moves[hash] = job
			m.mu.Unlock()
		}
	}()
	m.syncCounters() // fold traffic counters into the record before the engine forgets them
	m.dropForMove(hash, t)

	newDir := target
	var moveErr, leftover error
	if _, err := os.Lstat(src); err == nil {
		// Once the copy is whole the trail says so: a source half deleted when the process ends is then not taken
		// for an intact one, with the copy thrown away as unfinished (see recoverMoves).
		copied := func() error {
			return m.state.with(func(s *state) {
				if r := s.Torrents[hash]; r != nil {
					r.MoveCopied = true
				}
			})
		}
		if store.RootName(t.Info()) == "" { // no folder of its own: src and dst are the save folders (see resolveMove)
			if files, err := filesToMove(t.Info(), src, dst); err != nil {
				moveErr = err
			} else {
				moveErr, leftover = m.moveFiles(m.ctx, files, src, dst, job, copied)
			}
		} else {
			moveErr, leftover = m.moveTree(m.ctx, src, dst, job, copied)
		}
	} // else nothing was downloaded yet: only the folder changes
	if moveErr != nil {
		newDir = old // data is still (or again) in the old folder
	}

	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.SavePath = newDir
			r.MoveFrom, r.MoveTo, r.MoveDir, r.MoveCopied, r.MoveFiles = "", "", "", false, false // the move is over, one way or the other
		}
	}); err != nil {
		fmt.Fprintf(os.Stderr, "saving the result of moving %s: %v\n", hash, err)
	}
	if err := m.restoreByHash(hash); err != nil {
		moveErr = errors.Join(moveErr, fmt.Errorf("re-adding after the move: %w", err))
	}

	m.mu.Lock()
	job.running = false
	job.updatedAt = time.Now()
	if moveErr != nil {
		job.err = moveErr.Error()
		var c Coder
		if errors.As(moveErr, &c) {
			job.errCode, job.errArgs = c.ErrCode()
		}
	} else {
		job.err = ""
		delete(m.moves, hash) // success: nothing to show
	}
	if leftover != nil {
		job.warning = "files were copied, but the old ones could not be deleted: " + leftover.Error()
		m.moves[hash] = job
	}
	m.mu.Unlock()
	m.runMoveQueue() // a slot just freed up: let the next queued move use it right away
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
	delete(m.stalls, h)
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
//
// A copy stops when ctx is cancelled, and what it wrote is removed. Once it is whole and on
// the disk, copied (if given) records that before the source is touched; if it cannot, the
// copy is given up the same way.
func (m *Manager) moveTree(ctx context.Context, src, dst string, job *moveJob, copied func() error) (moveErr, leftover error) {
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
		return noSpace(CodeNoSpaceMove, uint64(need), 0, free), nil
	}
	m.mu.Lock()
	job.copySize = need
	m.mu.Unlock()

	if err := copyTree(ctx, src, dst, &job.done); err != nil {
		_ = removeAll(dst) // never leave half a copy behind
		return fmt.Errorf("copy failed (%v after rename failed: %v)", err, renameErr), nil
	}
	if copied != nil {
		if err := copied(); err != nil {
			_ = removeAll(dst)
			return fmt.Errorf("saving that the copy is done: %w", err), nil
		}
	}
	return nil, removeAll(src)
}

// waitForMoves gives the moves under way up to d to end. A copy stops as soon as Close cancels it, but a source being
// deleted after its copy is let through: interrupted, it is finished on the next start (see recoverMoves).
func (m *Manager) waitForMoves(d time.Duration) {
	end := time.Now().Add(d)
	for {
		m.mu.Lock()
		n := m.activeMovesLocked()
		m.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(end) {
			fmt.Fprintln(os.Stderr, "closing with a move still under way: it is finished on the next start")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
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

func copyTree(ctx context.Context, src, dst string, done *atomic.Int64) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
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
		return copyFile(ctx, p, out, done)
	})
}

// copyFile copies one file and makes sure it is on the disk: the source is deleted once the copy is done, and a power
// cut must not find the new copy shorter than the old one was.
func copyFile(ctx context.Context, src, dst string, done *atomic.Int64) error {
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
		if err := ctx.Err(); err != nil {
			out.Close()
			return err
		}
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
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// recoverMoves finishes what a move interrupted by a crash, a forced kill or a power cut left
// behind. It runs once at start, before any torrent is restored.
//
// moveTree either renames (atomic: the tree is wholly at one end or the other) or copies and
// then deletes the source, so at the moment of the interruption exactly one of these holds:
//
//   - the copy was whole (the trail says so, MoveCopied) and the destination is there: the
//     source was being deleted — adopt the destination and delete what is left of the source;
//   - the source is still there: the copy had not finished, and whatever reached the
//     destination is a partial tree nobody will ever complete — delete it and stay put;
//   - the source is gone and the destination exists: the rename went through, or the copy
//     finished and the source was removed, but the record never learned the new folder —
//     adopt the destination;
//   - neither exists: nothing had been downloaded yet, so only the folder changes.
func (m *Manager) recoverMoves() {
	type fix struct {
		hash, from, to, dir string
		copied, files       bool
	}
	var todo []fix
	m.state.view(func(s *state) {
		for h, r := range s.Torrents {
			if r.MoveTo != "" {
				todo = append(todo, fix{h, r.MoveFrom, r.MoveTo, r.MoveDir, r.MoveCopied, r.MoveFiles})
			}
		}
	})
	for _, f := range todo {
		_, srcErr := os.Lstat(f.from)
		_, dstErr := os.Lstat(f.to)
		adopt := dstErr == nil && (srcErr != nil || f.copied)
		switch {
		case f.files: // a torrent without a folder of its own: file by file
			adopt = m.recoverFileMove(f.hash, f.from, f.to, f.copied)
		case f.copied && dstErr == nil:
			fmt.Fprintf(os.Stderr, "move of %s had copied everything, adopting %s\n", f.hash, f.dir)
			if srcErr == nil {
				if err := removeAll(f.from); err != nil {
					fmt.Fprintf(os.Stderr, "cannot remove what is left at %s: %v\n", f.from, err)
				}
			}
		case srcErr == nil && dstErr == nil:
			fmt.Fprintf(os.Stderr, "move of %s was interrupted, removing the partial copy at %s\n", f.hash, f.to)
			if err := removeAll(f.to); err != nil {
				fmt.Fprintf(os.Stderr, "cannot remove the partial copy at %s: %v\n", f.to, err)
			}
		case adopt:
			fmt.Fprintf(os.Stderr, "move of %s had finished, adopting %s\n", f.hash, f.dir)
		}
		if err := m.state.with(func(s *state) {
			r := s.Torrents[f.hash]
			if r == nil {
				return
			}
			if adopt && f.dir != "" {
				r.SavePath = f.dir
			}
			r.MoveFrom, r.MoveTo, r.MoveDir, r.MoveCopied, r.MoveFiles = "", "", "", false, false
		}); err != nil {
			fmt.Fprintf(os.Stderr, "clearing the recovered move trail for %s: %v\n", f.hash, err)
		}
	}
}
