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
	"sort"
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

	partsDir string                     // where the part files live ("" = no part file, see parts.go)
	skipped  func(metainfo.Hash) []bool // the files a torrent marks "do not download"

	onMissing func(metainfo.Hash) // told when a read finds a file of the torrent gone (see OnMissingFile)
}

// OnMissingFile sets what is called when the engine reads a piece and a file it lies in is not on the disk (deleted or
// taken away by hand): the engine itself only refuses the peer that asked and goes on as if nothing happened. It is
// called on the engine's reading goroutine and must not block. Set it before any torrent is opened.
func (s *Storage) OnMissingFile(f func(metainfo.Hash)) { s.onMissing = f }

// Restat reads the sizes of a torrent's files from the disk again, as at its opening, and forgets the "complete"
// records of the pieces whose files are not there any more (see Completion): after files were deleted or taken away by
// hand, the engine, asked about those pieces again, learns that they are gone. It also comes before a check of files
// that were put back, which would otherwise find the sizes of when they were missing.
func (s *Storage) Restat(ih metainfo.Hash) {
	s.regMu.Lock()
	t := s.reg[ih]
	s.regMu.Unlock()
	if t == nil {
		return
	}
	unlock := t.lockParts()
	for i := range t.files {
		f := &t.files[i]
		if f.pad || f.length == 0 {
			continue
		}
		f.size = 0
		if fi, err := os.Stat(f.path); err == nil {
			f.size = fi.Size()
		}
	}
	unlock()
	t.healStaleCompletion(t.info)
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
	name := RootName(info)
	inside := func(p string) bool { return strings.HasPrefix(p, root+string(filepath.Separator)) }
	for i, f := range files {
		raw, parts := []string{root}, []string{root}
		if name != "" {
			raw, parts = append(raw, info.BestName()), append(parts, name)
		}
		for _, c := range f.BestPath() {
			raw, parts = append(raw, c), append(parts, safeName(c))
		}
		// the path as the torrent gives it must stay inside too: a ".." mended into "_" is still a torrent that tries
		p := filepath.Join(parts...)
		if !inside(filepath.Join(raw...)) || !inside(p) {
			return nil, fmt.Errorf("unsafe path in torrent: %q", filepath.Join(raw...))
		}
		out[i] = p
	}
	return out, nil
}

type fileSpan struct {
	path   string
	offset int64 // offset of the file inside the torrent (in v2 and hybrid torrents every file starts on a piece boundary)
	length int64
	size   int64 // at open time (or the last Restat): size on disk, 0 if the file does not exist
	pad    bool  // a padding file (BEP 47): all zeros, never on the disk
}

// IsPadding tells whether a file of a torrent is a padding file (BEP 47, the "p" attribute): zeros that only align the
// next file to a piece. It is not written to the disk and not shown.
func IsPadding(f metainfo.FileInfo) bool { return strings.Contains(f.Attr, "p") }

func (s *Storage) OpenTorrent(_ context.Context, info *metainfo.Info, ih metainfo.Hash) (storage.TorrentImpl, error) {
	paths, err := Paths(s.baseFor(ih), info)
	if err != nil {
		return storage.TorrentImpl{}, err
	}
	t := &torrentStore{
		comp: s.comp, ih: ih, owner: s, info: info,
		open: map[string]*handle{},
		stop: make(chan struct{}),
	}
	// The engine's offsets: in a torrent with a v2 part (BEP 52, hybrid ones too) every file starts on a piece boundary
	// and the gaps between files are padding, which is not on the disk; summing up the lengths would put every file
	// from the second on at the wrong place.
	for i, f := range info.UpvertedFiles() {
		var size int64
		if fi, err := os.Stat(paths[i]); err == nil {
			size = fi.Size()
		}
		t.files = append(t.files, fileSpan{path: paths[i], offset: f.TorrentOffset, length: f.Length, size: size, pad: IsPadding(f)})
		if f.Length == 0 && !IsPadding(f) { // the engine never writes these
			if err := os.MkdirAll(filepath.Dir(paths[i]), 0o755); err != nil {
				return storage.TorrentImpl{}, err
			}
			if fh, err := os.OpenFile(paths[i], os.O_CREATE|os.O_RDWR, 0o644); err == nil {
				fh.Close()
			}
		}
	}
	if s.partsDir != "" && info.PieceLength > 0 {
		t.parts = loadParts(s.partsDir, ih, info.PieceLength)
		// files wanted again while the torrent was closed get their bytes now, before anything looks at the disk
		_ = t.syncParts(s.skipList(ih, len(t.files)))
	}
	t.healStaleCompletion(info)
	switch {
	case s.consumeTrust(ih):
		t.trustPresent(info)
	case filesAbsent(t.files) && !t.partsHold():
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
	info  *metainfo.Info
	files []fileSpan
	parts *parts // nil without a part file

	sizeMu sync.Mutex // the sizes of files change under it, after the opening (see Restat and lockParts)

	mu      sync.Mutex
	open    map[string]*handle
	blocked map[string]bool // files that are being deleted (see BlockFiles)
	closed  bool
	stop    chan struct{}
}

var errClosed = errors.New("torrent storage is closed")

// busyWaits are the pauses between the tries at a file another program holds (see busyFile), about half a minute in
// all: an antivirus lets a file it scans go within seconds. Only then does the engine hear of the failure, which
// stops the torrent. A variable so tests can wait less.
var busyWaits = []time.Duration{
	100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second,
}

// whileBusy runs op until it does not fail for a file held by another program, waiting busyWaits between the tries;
// it gives up early when the torrent is closed. A failure of another kind is returned at once.
func (t *torrentStore) whileBusy(path string, op func() error) error {
	err := op()
	if err == nil || !busyFile(err) {
		return err
	}
	start := time.Now()
	for _, d := range busyWaits {
		select {
		case <-time.After(d):
		case <-t.stop:
			return err
		}
		if err = op(); err == nil || !busyFile(err) {
			if err == nil {
				fmt.Fprintf(os.Stderr, "%s was in use by another program, got it after %v\n", path, time.Since(start).Round(100*time.Millisecond))
			}
			return err
		}
	}
	return err
}

// acquire returns an open handle for path; create allows creating it (writes only). A file another program holds for
// a moment is tried again (see whileBusy), without holding up the other files of the torrent meanwhile.
func (t *torrentStore) acquire(path string, create bool) (h *handle, err error) {
	err = t.whileBusy(path, func() error {
		h, err = t.tryAcquire(path, create)
		return err
	})
	return h, err
}

func (t *torrentStore) tryAcquire(path string, create bool) (*handle, error) {
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
	if t.parts != nil { // first, and not under t.mu: the part file's lock is taken before it (see exportFile)
		t.parts.mu.Lock()
		t.parts.closed = true
		t.parts.closeFile()
		t.parts.mu.Unlock()
	}
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

// firstFile is the index of the first file that ends past byte off. The files are in the order of their offsets, so a
// torrent of tens of thousands of files is not walked from its first file for every block read or written.
func (t *torrentStore) firstFile(off int64) int {
	return sort.Search(len(t.files), func(i int) bool { return t.files[i].offset+t.files[i].length > off })
}

// io walks the files overlapping [off, off+len(b)) of the torrent.
func (t *torrentStore) io(b []byte, off int64, write bool) (n int, err error) {
	if write {
		t.owner.disk.beginWrite()
		defer t.owner.disk.endWrite()
	} else {
		t.owner.disk.waitForWrites()
	}
	for i := t.firstFile(off); i < len(t.files); i++ {
		f := t.files[i]
		if len(b) == 0 {
			break
		}
		end := f.offset + f.length
		if off >= end || f.length == 0 {
			continue
		}
		if off < f.offset {
			// the padding before this file (v2): nothing of it is on the disk, it is all zeros
			gap := min(f.offset-off, int64(len(b)))
			if !write {
				clear(b[:gap])
			}
			n += int(gap)
			b, off = b[gap:], off+gap
			if len(b) == 0 {
				break
			}
		}
		chunk := int64(len(b))
		if rem := end - off; chunk > rem {
			chunk = rem
		}
		if f.pad { // padding: dropped when written, zeros when read
			if !write {
				clear(b[:chunk])
			}
			n += int(chunk)
			b, off = b[chunk:], off+chunk
			continue
		}
		if t.parts != nil {
			if handled, m, err := t.partsIO(i, f, b[:chunk], off, write); handled {
				n += m
				if err != nil {
					return n, err
				}
				b, off = b[chunk:], off+chunk
				continue
			}
		}
		if write && t.isBlocked(f.path) { // being deleted: the data has nowhere to go and is dropped
			n += int(chunk)
			b, off = b[chunk:], off+chunk
			continue
		}
		h, err := t.acquire(f.path, write)
		if err != nil {
			if !write && errors.Is(err, fs.ErrNotExist) && !t.isBlocked(f.path) && t.owner.onMissing != nil {
				t.owner.onMissing(t.ih)
			}
			return n, err
		}
		var m int
		err = t.whileBusy(f.path, func() (err error) { // a range another program locked
			if write {
				m, err = h.f.WriteAt(b[:chunk], off-f.offset)
			} else {
				m, err = h.f.ReadAt(b[:chunk], off-f.offset)
			}
			return err
		})
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
		if f.length > 0 && f.size > 0 && !f.pad {
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
	unlock := t.lockParts()
	defer unlock()
	for i := t.firstFile(off); i < len(t.files); i++ {
		f := t.files[i]
		if off+n <= f.offset {
			break // this one and all after it begin past the range
		}
		if f.length == 0 || f.pad || off >= f.offset+f.length {
			continue
		}
		if t.keptInParts(i) {
			if !t.parts.has(int(off / t.parts.pieceLen)) {
				return false
			}
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
	unlock := t.lockParts()
	defer unlock()
	for i := t.firstFile(off); i < len(t.files); i++ {
		f := t.files[i]
		if off+n <= f.offset {
			break // this one and all after it begin past the range
		}
		if f.length == 0 || f.pad || off >= f.offset+f.length {
			continue
		}
		if t.keptInParts(i) { // its bytes of a piece are there when the piece has a slot
			if !t.parts.has(int(off / t.parts.pieceLen)) {
				return false
			}
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

// lockParts holds the lock of the files' sizes and the part file's lock (the sizes of files and the slots change under
// them) and returns their unlock.
func (t *torrentStore) lockParts() func() {
	t.sizeMu.Lock()
	if t.parts == nil {
		return t.sizeMu.Unlock
	}
	t.parts.mu.Lock()
	return func() {
		t.parts.mu.Unlock()
		t.sizeMu.Unlock()
	}
}

// keptInParts tells whether file i is kept in the part file. The part file's lock is held.
func (t *torrentStore) keptInParts(i int) bool { return t.parts != nil && t.parts.files[i] }

// partsHold tells whether the part file holds any bytes of the torrent.
func (t *torrentStore) partsHold() bool {
	unlock := t.lockParts()
	defer unlock()
	return t.parts != nil && len(t.parts.slots) > 0
}
