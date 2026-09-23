// Package atomicfile writes a file so that a crash or a power cut can never leave a
// half-written one behind. The daemon's state and settings both live in a single JSON file
// that is rewritten in full, so a torn write there costs the whole torrent list.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces path with data. The new content is flushed to the disk before the old file
// is replaced, so an interrupted write leaves either the previous file or the new one, never
// a truncated mix of the two. When keepBackup is set the file being replaced is kept as
// "<path>.bak", which ReadWithBackup falls back to.
func Write(path string, data []byte, perm os.FileMode, keepBackup bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := writeSynced(tmp, data, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if keepBackup {
		// Best effort: on the very first save there is nothing to back up, and a backup that
		// cannot be written is no reason to refuse the save itself.
		_ = backup(path, perm)
	}
	return os.Rename(tmp, path)
}

// linkFile is a variable so a test can pretend the filesystem has no hard links.
var linkFile = os.Link

// backup keeps the file about to be replaced as "<path>.bak". A hard link costs nothing and is
// what NTFS gives us; filesystems without them (a portable install on a FAT32 stick) fall back
// to a plain copy, because a backup that silently does not exist is worse than a slow one.
func backup(path string, perm os.FileMode) error {
	_ = os.Remove(path + ".bak")
	if err := linkFile(path, path+".bak"); err == nil {
		return nil
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return err // nothing to back up yet, or it cannot be read
	}
	return writeSynced(path+".bak", old, perm)
}

// writeSynced writes the whole file and makes sure it reached the disk before returning.
func writeSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil { // the point of the exercise: no write is left in the cache
		f.Close()
		return err
	}
	return f.Close()
}

// ReadWithBackup reads path, falling back to "<path>.bak" when the main file is missing or
// parse reports it unusable. ok tells whether anything was read at all; fromBackup says the
// backup had to be used, which the caller may want to mention in its log.
func ReadWithBackup(path string, parse func([]byte) error) (ok, fromBackup bool, err error) {
	b, readErr := os.ReadFile(path)
	if readErr == nil {
		if err = parse(b); err == nil {
			return true, false, nil
		}
	} else if !os.IsNotExist(readErr) {
		return false, false, readErr
	}
	// The main file is gone or unusable: the copy from before the last save is the best
	// remaining answer.
	bak, bakErr := os.ReadFile(path + ".bak")
	if bakErr != nil {
		if readErr != nil && os.IsNotExist(readErr) && os.IsNotExist(bakErr) {
			return false, false, nil // a first run: neither file exists yet
		}
		return false, false, err // report why the main file could not be used
	}
	if perr := parse(bak); perr != nil {
		return false, false, err
	}
	return true, true, nil
}
