package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/store"
)

// A torrent without a folder of its own (its name is "-", metainfo.NoName) keeps its files right in the save folder,
// among whatever else is there, so it cannot be moved as one tree: its files are moved one by one. First every file
// gets to the new folder (renamed on the same drive, copied to another one), then the trail says the copy is whole
// (MoveCopied), and only then are the copies' sources deleted. A move cut short is undone or finished at the next
// start the same way (see recoverFileMove).

// fileMove is one file of such a torrent: where it is and where it goes.
type fileMove struct{ from, to string }

// filesToMove pairs every file of the torrent in the save folder cur with its place in target.
func filesToMove(info *metainfo.Info, cur, target string) ([]fileMove, error) {
	from, err := store.Paths(cur, info)
	if err != nil {
		return nil, err
	}
	to, err := store.Paths(target, info)
	if err != nil {
		return nil, err
	}
	out := make([]fileMove, len(from))
	for i := range from {
		out[i] = fileMove{from[i], to[i]}
	}
	return out, nil
}

// moveFiles moves the files one by one, as moveTree moves a tree: on any failure, or when ctx is cancelled, what was
// done is undone and the files stay where they were. leftover is a failure to delete a source after its copy.
func (m *Manager) moveFiles(ctx context.Context, files []fileMove, cur, target string, job *moveJob, copied func() error) (moveErr, leftover error) {
	var present []fileMove
	var need int64
	for _, f := range files {
		if fi, err := os.Lstat(f.from); err == nil && fi.Mode().IsRegular() {
			present = append(present, f)
			need += fi.Size()
		}
	}
	m.mu.Lock()
	job.copySize = need
	m.mu.Unlock()

	var renamed, copies []fileMove
	undo := func() {
		for _, f := range copies {
			_ = os.Remove(f.to)
		}
		for _, f := range renamed {
			if err := os.Rename(f.to, f.from); err != nil {
				fmt.Fprintf(os.Stderr, "moving %s back: %v\n", f.to, err)
			}
		}
		pruneEmptyDirs(toPaths(present), target)
	}
	renames := true // until one fails: the target is on another drive, copy the rest
	for i, f := range present {
		if err := ctx.Err(); err != nil {
			undo()
			return err, nil
		}
		if err := os.MkdirAll(filepath.Dir(f.to), 0o755); err != nil {
			undo()
			return err, nil
		}
		if renames {
			if err := renameHeld(f.from, f.to, i == 0); err == nil {
				if fi, err := os.Stat(f.to); err == nil {
					job.done.Add(fi.Size())
				}
				renamed = append(renamed, f)
				continue
			}
			renames = false
			var left int64
			for _, g := range present[i:] {
				if fi, err := os.Stat(g.from); err == nil {
					left += fi.Size()
				}
			}
			if free, err := freeSpace(target); err == nil && uint64(left) > free {
				undo()
				return fmt.Errorf("%w: moving needs %d bytes, %d free in the target folder", ErrNoDiskSpace, left, free), nil
			}
		}
		copies = append(copies, f) // before the copy: a half-written one is removed by undo too
		if err := copyFile(ctx, f.from, f.to, &job.done); err != nil {
			undo()
			return fmt.Errorf("copy failed: %w", err), nil
		}
	}
	if copied != nil {
		if err := copied(); err != nil {
			undo()
			return fmt.Errorf("saving that the copy is done: %w", err), nil
		}
	}
	var errs []error
	for _, f := range copies {
		if err := removeFile(f.from); err != nil {
			errs = append(errs, err)
		}
	}
	pruneEmptyDirs(fromPaths(present), cur)
	return nil, errors.Join(errs...)
}

// renameHeld renames a file; the first one is tried a few times, as the engine may still hold it for a moment after
// the torrent was dropped.
func renameHeld(from, to string, first bool) error {
	tries := 1
	if first {
		tries = 6
	}
	var err error
	for i := 0; i < tries; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		if i+1 < tries {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return err
}

func fromPaths(fs []fileMove) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.from
	}
	return out
}

func toPaths(fs []fileMove) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.to
	}
	return out
}

// pruneEmptyDirs removes the folders the given files were in, below stop, that are empty now, the deepest first. A
// folder with anything else in it stays, and so does stop itself.
func pruneEmptyDirs(files []string, stop string) {
	sep := string(filepath.Separator)
	dirs := map[string]bool{}
	for _, p := range files {
		for dir := filepath.Dir(p); strings.HasPrefix(dir, stop+sep); dir = filepath.Dir(dir) {
			dirs[dir] = true
		}
	}
	list := make([]string, 0, len(dirs))
	for dir := range dirs {
		list = append(list, dir)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) }) // a folder before the one it is in
	for _, dir := range list {
		_ = os.Remove(dir) // fails for a folder that is not empty: that one stays
	}
}

// recoverFileMove settles a move of a torrent without a folder of its own that the process did not finish (see
// recoverMoves): finished when the copy was whole, undone otherwise. It tells whether the torrent is in the new
// folder now.
func (m *Manager) recoverFileMove(hash, from, to string, copied bool) bool {
	mi, err := metainfo.LoadFromFile(m.metaPath(hash))
	if err != nil {
		fmt.Fprintf(os.Stderr, "move of %s was interrupted, but its file list is not known: %v\n", hash, err)
		return false
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "move of %s was interrupted, but its file list is not known: %v\n", hash, err)
		return false
	}
	files, err := filesToMove(&info, from, to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "move of %s was interrupted: %v\n", hash, err)
		return false
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	if copied {
		fmt.Fprintf(os.Stderr, "move of %s had copied everything, finishing it in %s\n", hash, to)
		for _, f := range files {
			if exists(f.to) && exists(f.from) {
				if err := removeFile(f.from); err != nil {
					fmt.Fprintf(os.Stderr, "cannot remove %s: %v\n", f.from, err)
				}
			}
		}
		pruneEmptyDirs(fromPaths(files), from)
		return true
	}
	fmt.Fprintf(os.Stderr, "move of %s was interrupted, putting its files back in %s\n", hash, from)
	for _, f := range files {
		switch {
		case !exists(f.to):
		case exists(f.from): // a copy, perhaps half written: the source is whole
			_ = os.Remove(f.to)
		default: // renamed: back it goes
			if err := os.Rename(f.to, f.from); err != nil {
				fmt.Fprintf(os.Stderr, "moving %s back: %v\n", f.to, err)
			}
		}
	}
	pruneEmptyDirs(toPaths(files), to)
	return false
}
