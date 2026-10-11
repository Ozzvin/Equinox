package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/atomicfile"
)

// The part file. A piece is downloaded whole (or its hash would not match), so a piece that a wanted file shares
// with a file marked "do not download" also brings bytes of that file. Written the plain way they would make the
// skipped file appear on the disk, small files (subtitles, covers) even complete, and deleting it would spoil the
// piece of the wanted file and fetch it again. Those bytes are kept here instead: one file per torrent in the
// state folder (<dir>/<hash>.parts), holding whole-piece slots, and an index (<dir>/<hash>.json) of which piece is
// in which slot and which files are kept here. A file goes here when it is skipped and not on the disk; a skipped
// file that is on the disk (downloaded before it was skipped) goes on being used there. When a file is wanted
// again, its bytes are moved from here into the file itself.

// UseParts turns the part file on: dir is where the part files live, skipped tells which files of a torrent are
// marked "do not download" (by index; a missing entry means wanted).
func (s *Storage) UseParts(dir string, skipped func(metainfo.Hash) []bool) {
	s.partsDir, s.skipped = dir, skipped
}

// FilesChanged is called when the files a torrent skips have changed: the files that are wanted again get their
// bytes from the part file.
func (s *Storage) FilesChanged(ih metainfo.Hash) error {
	s.regMu.Lock()
	t := s.reg[ih]
	s.regMu.Unlock()
	if t == nil || t.parts == nil {
		return nil // a torrent that is not open does it when it opens
	}
	return t.syncParts(s.skipList(ih, len(t.files)))
}

// DropParts deletes the part file of a torrent (it is being removed).
func (s *Storage) DropParts(ih metainfo.Hash) {
	if s.partsDir == "" {
		return
	}
	s.regMu.Lock()
	t := s.reg[ih]
	s.regMu.Unlock()
	if t != nil && t.parts != nil {
		p := t.parts
		p.mu.Lock()
		p.dropped = true
		p.closeFile()
		p.files, p.slots = map[int]bool{}, map[int]int{}
		p.mu.Unlock()
	}
	base := filepath.Join(s.partsDir, ih.HexString())
	_ = os.Remove(base + ".parts")
	_ = os.Remove(base + ".json")
}

// InParts tells, by index, which files of an open torrent are kept in its part file (nil when there are none).
func (s *Storage) InParts(ih metainfo.Hash) []bool {
	s.regMu.Lock()
	t := s.reg[ih]
	s.regMu.Unlock()
	if t == nil || t.parts == nil {
		return nil
	}
	p := t.parts
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.files) == 0 {
		return nil
	}
	out := make([]bool, len(t.files))
	for i := range p.files {
		if i < len(out) {
			out[i] = true
		}
	}
	return out
}

func (s *Storage) skipList(ih metainfo.Hash, n int) []bool {
	out := make([]bool, n)
	if s.skipped != nil {
		copy(out, s.skipped(ih))
	}
	return out
}

type partsIndex struct {
	PieceLength int64       `json:"pieceLength"`
	Files       []int       `json:"files"` // the files kept here
	Slots       map[int]int `json:"slots"` // piece index -> slot
}

type parts struct {
	mu        sync.Mutex
	data, idx string // the part file and its index
	pieceLen  int64
	skip      []bool       // the files the torrent skips now
	files     map[int]bool // the files kept here
	slots     map[int]int  // piece index -> slot in the part file
	f         *os.File     // the part file, opened when first needed
	dropped   bool         // the torrent is being removed: nothing is written any more
	closed    bool         // the torrent's storage is closed
}

func loadParts(dir string, ih metainfo.Hash, pieceLen int64) *parts {
	base := filepath.Join(dir, ih.HexString())
	p := &parts{data: base + ".parts", idx: base + ".json", pieceLen: pieceLen, files: map[int]bool{}, slots: map[int]int{}}
	b, err := os.ReadFile(p.idx)
	if err != nil {
		return p
	}
	var x partsIndex
	if json.Unmarshal(b, &x) != nil || x.PieceLength != pieceLen {
		return p // not ours, or from another version of the torrent: nothing in it can be used
	}
	if _, err := os.Stat(p.data); err != nil {
		x.Slots = nil // the data is gone: the files are still kept here, their pieces are downloaded again
	}
	for _, i := range x.Files {
		p.files[i] = true
	}
	for piece, slot := range x.Slots {
		p.slots[piece] = slot
	}
	return p
}

// save writes the index; with nothing in it both files are deleted.
func (p *parts) save() error {
	if p.dropped {
		return nil
	}
	if len(p.files) == 0 && len(p.slots) == 0 {
		p.closeFile()
		_ = os.Remove(p.data)
		if err := os.Remove(p.idx); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	x := partsIndex{PieceLength: p.pieceLen, Slots: p.slots}
	for i := range p.files {
		x.Files = append(x.Files, i)
	}
	sort.Ints(x.Files)
	b, err := json.Marshal(&x)
	if err != nil {
		return err
	}
	return atomicfile.Write(p.idx, b, 0o644, false)
}

func (p *parts) closeFile() {
	if p.f != nil {
		p.f.Close()
		p.f = nil
	}
}

func (p *parts) file() (*os.File, error) {
	if p.f == nil {
		if err := os.MkdirAll(filepath.Dir(p.data), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(p.data, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, err
		}
		p.f = f
	}
	return p.f, nil
}

// slot returns the slot of a piece, giving it the lowest free one when create is set.
func (p *parts) slot(piece int, create bool) (int, bool) {
	if s, ok := p.slots[piece]; ok {
		return s, true
	}
	if !create {
		return 0, false
	}
	used := map[int]bool{}
	for _, s := range p.slots {
		used[s] = true
	}
	s := 0
	for used[s] {
		s++
	}
	p.slots[piece] = s
	return s, true
}

// ---------------------------------------------------------------- the torrent's side

// partsIO decides whether a chunk of file i goes to the part file, and does it there when it does. It is called
// for every chunk, so a wanted file costs only a look at two maps.
func (t *torrentStore) partsIO(i int, f fileSpan, b []byte, off int64, write bool) (handled bool, n int, err error) {
	p := t.parts
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return true, 0, errClosed
	}
	if !p.files[i] {
		if i >= len(p.skip) || !p.skip[i] || t.onDisk(f.path) {
			return false, 0, nil
		}
		p.files[i] = true // skipped and not on the disk: from now on its bytes are kept here
		if err := p.save(); err != nil {
			return true, 0, err
		}
	}
	if p.dropped {
		if write {
			return true, len(b), nil
		}
		return true, 0, &fs.PathError{Op: "open", Path: f.path, Err: fs.ErrNotExist}
	}
	for len(b) > 0 {
		piece := int(off / p.pieceLen)
		within := off - int64(piece)*p.pieceLen
		chunk := int64(len(b))
		if rem := p.pieceLen - within; chunk > rem {
			chunk = rem
		}
		_, had := p.slots[piece]
		s, ok := p.slot(piece, write)
		if !ok { // never written: as if the file was missing
			return true, n, &fs.PathError{Op: "open", Path: f.path, Err: fs.ErrNotExist}
		}
		if write && !had {
			if err := p.save(); err != nil {
				return true, n, err
			}
		}
		fh, err := p.file()
		if err != nil {
			return true, n, err
		}
		var m int
		if write {
			m, err = fh.WriteAt(b[:chunk], int64(s)*p.pieceLen+within)
		} else {
			m, err = fh.ReadAt(b[:chunk], int64(s)*p.pieceLen+within)
		}
		n += m
		if err != nil {
			return true, n, err
		}
		b, off = b[chunk:], off+chunk
	}
	return true, n, nil
}

// onDisk reports whether a file is on the disk (or open, which on Windows may be a file being deleted).
func (t *torrentStore) onDisk(path string) bool {
	t.mu.Lock()
	_, open := t.open[path]
	t.mu.Unlock()
	if open {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}

// syncParts takes the files a torrent skips now: the files kept in the part file that are wanted again get their
// bytes written into the files themselves, and the slots nobody needs any more are let go.
func (t *torrentStore) syncParts(skip []bool) error {
	p := t.parts
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skip = skip
	if p.closed {
		return nil // it is done when the torrent is opened again
	}
	var errs []error
	changed := false
	for i := range p.files {
		if i < len(skip) && skip[i] {
			continue
		}
		if i >= len(t.files) {
			delete(p.files, i)
			changed = true
			continue
		}
		if err := t.exportFile(i); err != nil {
			errs = append(errs, err)
			continue // stays here, the next change or opening tries again
		}
		delete(p.files, i)
		changed = true
	}
	for piece := range p.slots {
		if !t.pieceInParts(piece) {
			delete(p.slots, piece)
			changed = true
		}
	}
	if changed {
		errs = append(errs, p.save())
	}
	return errors.Join(errs...)
}

// pieceInParts tells whether some file kept in the part file has bytes in the piece. p.mu is held.
func (t *torrentStore) pieceInParts(piece int) bool {
	p := t.parts
	start, end := int64(piece)*p.pieceLen, int64(piece+1)*p.pieceLen
	for i := range p.files {
		if i >= len(t.files) {
			continue
		}
		f := t.files[i]
		if f.length > 0 && f.offset < end && f.offset+f.length > start {
			return true
		}
	}
	return false
}

// exportFile writes the bytes of file i that are in the part file into the file itself. p.mu is held.
func (t *torrentStore) exportFile(i int) error {
	p := t.parts
	f := t.files[i]
	pieces := make([]int, 0, len(p.slots))
	for piece := range p.slots {
		pieces = append(pieces, piece)
	}
	sort.Ints(pieces)
	var buf []byte
	for _, piece := range pieces {
		start, end := int64(piece)*p.pieceLen, int64(piece+1)*p.pieceLen
		lo, hi := max(start, f.offset), min(end, f.offset+f.length)
		if lo >= hi {
			continue
		}
		fh, err := p.file()
		if err != nil {
			return err
		}
		if int64(cap(buf)) < hi-lo {
			buf = make([]byte, hi-lo)
		}
		b := buf[:hi-lo]
		m, err := fh.ReadAt(b, int64(p.slots[piece])*p.pieceLen+(lo-start))
		if m == 0 {
			continue // a slot never filled this far: nothing to move
		}
		if err != nil && m < len(b) {
			b = b[:m]
		}
		h, err := t.acquire(f.path, true)
		if err != nil {
			return err
		}
		_, err = h.f.WriteAt(b, lo-f.offset)
		t.release(f.path, h)
		if err != nil {
			return err
		}
	}
	if fi, err := os.Stat(f.path); err == nil {
		t.files[i].size = fi.Size()
	}
	return nil
}

// has tells whether the piece has its bytes in the part file. p.mu is held.
func (p *parts) has(piece int) bool {
	_, ok := p.slots[piece]
	return ok
}

// HasParts tells whether a torrent has a part file (whether it is open or not).
func (s *Storage) HasParts(ih metainfo.Hash) bool {
	if s.partsDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(s.partsDir, ih.HexString()+".json"))
	return err == nil
}
