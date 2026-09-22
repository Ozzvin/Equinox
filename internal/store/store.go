// Package store is the torrent file storage. It replaces the engine's default storage,
// which memory-maps files and keeps them mapped until shutdown: on Windows that makes it
// impossible to delete or move a downloaded file while the daemon runs. Here every file
// is opened on demand, cached briefly and closed when idle or when the torrent is dropped.
package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

const idleClose = 2 * time.Second

// Storage implements storage.ClientImpl. Files live at <base>/<torrent name>/<path>, where
// base is chosen per torrent.
type Storage struct {
	baseFor func(metainfo.Hash) string
	comp    storage.PieceCompletion
	disk    *diskPriority // downloading (writes) goes ahead of seeding (reads) on the same disk

	trustMu sync.Mutex
	trust   map[metainfo.Hash]bool // torrents whose files on disk are to be taken as complete

	regMu sync.Mutex
	reg   map[metainfo.Hash]*torrentStore // the torrents that are open, so files can be released on request
}

// BlockFiles makes the given files of a torrent unreachable for the engine until the returned function is called: reads
// fail as if the file was missing and writes are dropped, and the open handles are closed as soon as nobody uses them. It is
// how a file is deleted while the torrent is being checked or is downloading: without it the engine opens the file again
// at once and the deletion fails with "used by another process".
func (s *Storage) BlockFiles(ih metainfo.Hash, paths []string) (unblock func()) {
	s.regMu.Lock()
	t := s.reg[ih]
	s.regMu.Unlock()
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	if t.blocked == nil {
		t.blocked = map[string]bool{}
	}
	for _, p := range paths {
		t.blocked[p] = true
	}
	t.mu.Unlock()
	t.dropHandles(paths)
	return func() {
		t.mu.Lock()
		for _, p := range paths {
			delete(t.blocked, p)
		}
		t.mu.Unlock()
	}
}

// CloseFiles closes the open handles of the given files of a torrent (a handle that is in use goes on the moment it is
// free), so that the files can be deleted or moved at once instead of after the idle timeout.
func (s *Storage) CloseFiles(ih metainfo.Hash, paths []string) {
	s.regMu.Lock()
	t := s.reg[ih]
	s.regMu.Unlock()
	if t != nil {
		t.dropHandles(paths)
	}
}

// Trust makes the next opening of the torrent take the files that are already on disk, at
// full size, as finished without hashing them ("skip the hash check"). It applies once.
func (s *Storage) Trust(ih metainfo.Hash) {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	if s.trust == nil {
		s.trust = map[metainfo.Hash]bool{}
	}
	s.trust[ih] = true
}

func (s *Storage) consumeTrust(ih metainfo.Hash) bool {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	ok := s.trust[ih]
	delete(s.trust, ih)
	return ok
}

// New creates a storage that keeps every torrent under one directory; comp remembers
// which pieces are verified (may be nil).
func New(base string, comp storage.PieceCompletion) *Storage {
	return NewResolver(func(metainfo.Hash) string { return base }, comp)
}

// NewResolver creates a storage whose directory is decided per torrent.
func NewResolver(baseFor func(metainfo.Hash) string, comp storage.PieceCompletion) *Storage {
	if comp == nil {
		comp = storage.NewMapPieceCompletion()
	}
	return &Storage{baseFor: baseFor, comp: comp, disk: newDiskPriority()}
}

// Paths returns the on-disk location of every file of the torrent, in torrent order.
func Paths(base string, info *metainfo.Info) ([]string, error) {
	root, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	files := info.UpvertedFiles()
	out := make([]string, len(files))
	for i, f := range files {
		parts := []string{root}
		if info.BestName() != metainfo.NoName {
			parts = append(parts, info.BestName())
		}
		p := filepath.Join(append(parts, f.BestPath()...)...)
		if !strings.HasPrefix(p, root+string(filepath.Separator)) {
			return nil, fmt.Errorf("unsafe path in torrent: %q", p)
		}
		out[i] = p
	}
	return out, nil
}

type fileSpan struct {
	path   string
	offset int64 // offset of the file inside the torrent
	length int64
	size   int64 // at open time: size on disk, 0 if the file does not exist
}

func (s *Storage) OpenTorrent(_ context.Context, info *metainfo.Info, ih metainfo.Hash) (storage.TorrentImpl, error) {
	paths, err := Paths(s.baseFor(ih), info)
	if err != nil {
		return storage.TorrentImpl{}, err
	}
	t := &torrentStore{
		comp: s.comp, ih: ih, owner: s,
		open: map[string]*handle{},
		stop: make(chan struct{}),
	}
	var off int64
	for i, f := range info.UpvertedFiles() {
		var size int64
		if fi, err := os.Stat(paths[i]); err == nil {
			size = fi.Size()
		}
		t.files = append(t.files, fileSpan{path: paths[i], offset: off, length: f.Length, size: size})
		off += f.Length
		if f.Length == 0 { // the engine never writes these
			if err := os.MkdirAll(filepath.Dir(paths[i]), 0o755); err != nil {
				return storage.TorrentImpl{}, err
			}
			if fh, err := os.OpenFile(paths[i], os.O_CREATE|os.O_RDWR, 0o644); err == nil {
				fh.Close()
			}
		}
	}
	t.healStaleCompletion(info)
	switch {
	case s.consumeTrust(ih):
		t.trustPresent(info)
	case filesAbsent(t.files):
		// A fresh destination: nothing to verify, so skip the hash check and go straight to
		// downloading instead of hashing pieces that are certainly missing.
		t.trustAbsent(info)
	}
	s.regMu.Lock()
	if s.reg == nil {
		s.reg = map[metainfo.Hash]*torrentStore{}
	}
	s.reg[ih] = t
	s.regMu.Unlock()
	go t.janitor()
	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl { return &piece{t: t, p: p} },
		Close: t.close,
	}, nil
}

// ---------------------------------------------------------------- per-torrent state

type handle struct {
	f    *os.File
	refs int
	last time.Time
	drop bool // to be closed as soon as nobody uses it
}

type torrentStore struct {
	comp  storage.PieceCompletion
	ih    metainfo.Hash
	owner *Storage
	files []fileSpan

	mu      sync.Mutex
	open    map[string]*handle
	blocked map[string]bool // files that are being deleted (see BlockFiles)
	closed  bool
	stop    chan struct{}
}

var errClosed = errors.New("torrent storage is closed")

// acquire returns an open handle for path; create allows creating it (writes only).
func (t *torrentStore) acquire(path string, create bool) (*handle, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errClosed
	}
	if t.blocked[path] {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	if h, ok := t.open[path]; ok {
		h.refs++
		return h, nil
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, err
	}
	h := &handle{f: f, refs: 1}
	t.open[path] = h
	return h, nil
}

func (t *torrentStore) release(path string, h *handle) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h.refs--
	h.last = time.Now()
	if (t.closed || h.drop) && h.refs == 0 {
		h.f.Close()
		delete(t.open, path)
	}
}

func (t *torrentStore) isBlocked(path string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.blocked[path]
}

// dropHandles closes the handles of the given files now, or as soon as they are free.
func (t *torrentStore) dropHandles(paths []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range paths {
		h, ok := t.open[p]
		if !ok {
			continue
		}
		if h.refs == 0 {
			h.f.Close()
			delete(t.open, p)
		} else {
			h.drop = true
		}
	}
}

// janitor closes handles that have been idle, so files can be moved or deleted by the
// user while the torrent keeps seeding.
func (t *torrentStore) janitor() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			t.mu.Lock()
			for p, h := range t.open {
				if h.refs == 0 && time.Since(h.last) > idleClose {
					h.f.Close()
					delete(t.open, p)
				}
			}
			t.mu.Unlock()
		}
	}
}

func (t *torrentStore) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.stop)
	if t.owner != nil {
		t.owner.regMu.Lock()
		if t.owner.reg[t.ih] == t {
			delete(t.owner.reg, t.ih)
		}
		t.owner.regMu.Unlock()
	}
	var errs []error
	for p, h := range t.open {
		if h.refs == 0 { // busy ones are closed by release
			errs = append(errs, h.f.Close())
			delete(t.open, p)
		}
	}
	return errors.Join(errs...)
}

// io walks the files overlapping [off, off+len(b)) of the torrent.
func (t *torrentStore) io(b []byte, off int64, write bool) (n int, err error) {
	if write {
		t.owner.disk.beginWrite()
		defer t.owner.disk.endWrite()
	} else {
		t.owner.disk.waitForWrites()
	}
	for _, f := range t.files {
		if len(b) == 0 {
			break
		}
		end := f.offset + f.length
		if off >= end || f.length == 0 {
			continue
		}
		if off < f.offset {
			return n, io.ErrUnexpectedEOF
		}
		chunk := int64(len(b))
		if rem := end - off; chunk > rem {
			chunk = rem
		}
		if write && t.isBlocked(f.path) { // being deleted: the data has nowhere to go and is dropped
			n += int(chunk)
			b, off = b[chunk:], off+chunk
			continue
		}
		h, err := t.acquire(f.path, write)
		if err != nil {
			return n, err
		}
		var m int
		if write {
			m, err = h.f.WriteAt(b[:chunk], off-f.offset)
		} else {
			m, err = h.f.ReadAt(b[:chunk], off-f.offset)
		}
		t.release(f.path, h)
		n += m
		if err != nil {
			return n, err
		}
		b, off = b[chunk:], off+chunk
	}
	if len(b) > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

// ---------------------------------------------------------------- pieces

type piece struct {
	t *torrentStore
	p metainfo.Piece
}

func (p *piece) key() metainfo.PieceKey {
	return metainfo.PieceKey{InfoHash: p.t.ih, Index: p.p.Index()}
}

func (p *piece) ReadAt(b []byte, off int64) (int, error) {
	return p.t.io(b, p.p.Offset()+off, false)
}

func (p *piece) WriteAt(b []byte, off int64) (int, error) {
	return p.t.io(b, p.p.Offset()+off, true)
}

func (p *piece) MarkComplete() error    { return p.t.comp.Set(p.key(), true) }
func (p *piece) MarkNotComplete() error { return p.t.comp.Set(p.key(), false) }

func (p *piece) Completion() storage.Completion {
	c, err := p.t.comp.Get(p.key())
	c.Err = errors.Join(c.Err, err)
	// The completion database outlives the files: after "remove with data", or when the
	// user deletes files by hand, its records are stale. A piece can only be complete if
	// every file it touches was on disk when the torrent was opened.
	if c.Ok && c.Complete && !p.t.present(p.p.Offset(), p.p.Length()) {
		return storage.Completion{Ok: true, Complete: false}
	}
	return c
}

// healStaleCompletion erases "complete" records of pieces whose files are gone. Merely
// hiding them is not enough: once the files are preallocated again (as zeros) a restart
// would find the old record and take the empty data for finished pieces.
func (t *torrentStore) healStaleCompletion(info *metainfo.Info) {
	for i := 0; i < info.NumPieces(); i++ {
		p := info.Piece(i)
		key := metainfo.PieceKey{InfoHash: t.ih, Index: i}
		if c, _ := t.comp.Get(key); c.Ok && c.Complete && !t.present(p.Offset(), p.Length()) {
			_ = t.comp.Set(key, false)
		}
	}
}

// filesAbsent reports whether none of a torrent's non-empty files have any bytes on disk yet: a
// completely fresh destination with nothing to verify.
func filesAbsent(files []fileSpan) bool {
	for _, f := range files {
		if f.length > 0 && f.size > 0 {
			return false
		}
	}
	return true
}

// trustAbsent marks every piece as known-incomplete when nothing of this torrent exists on disk:
// there is nothing to check, so the engine can start downloading right away instead of hashing
// pieces it already knows are missing.
func (t *torrentStore) trustAbsent(info *metainfo.Info) {
	for i := 0; i < info.NumPieces(); i++ {
		_ = t.comp.Set(metainfo.PieceKey{InfoHash: t.ih, Index: i}, false)
	}
}

// trustPresent marks every piece complete whose files all exist at their full length. The
// user asked not to verify them, so the data is taken on trust; pieces that touch a missing
// or short file stay unknown and are handled by the engine as usual.
func (t *torrentStore) trustPresent(info *metainfo.Info) {
	for i := 0; i < info.NumPieces(); i++ {
		p := info.Piece(i)
		if t.fullyPresent(p.Offset(), p.Length()) {
			_ = t.comp.Set(metainfo.PieceKey{InfoHash: t.ih, Index: i}, true)
		}
	}
}

// fullyPresent is present() with the stricter rule that every file the range touches is
// complete on disk, not merely long enough for this range.
func (t *torrentStore) fullyPresent(off, n int64) bool {
	for _, f := range t.files {
		if f.length == 0 || off >= f.offset+f.length || off+n <= f.offset {
			continue
		}
		if f.size < f.length {
			return false
		}
	}
	return true
}

// present reports whether the bytes [off, off+n) can exist on disk: every file the range
// touches must have been at least long enough at open time. (Files grow while they are
// downloaded, so a shorter file is fine as long as it covers this range.)
func (t *torrentStore) present(off, n int64) bool {
	for _, f := range t.files {
		if f.length == 0 || off >= f.offset+f.length || off+n <= f.offset {
			continue
		}
		end := off + n
		if fend := f.offset + f.length; end > fend {
			end = fend
		}
		if f.size < end-f.offset {
			return false
		}
	}
	return true
}
