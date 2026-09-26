package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/store"
)

// filePaths returns the on-disk location of every file of a torrent whose info is known.
func (m *Manager) filePaths(hash string) ([]string, error) {
	t, err := m.get(hash)
	if err != nil {
		return nil, err
	}
	info := t.Info()
	if info == nil {
		return nil, ErrNoMetadata
	}
	paths, err := store.Paths(m.saveDir(hash), info)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return paths, nil
}

// OpenFile shows one file of a torrent in the file manager (its folder opens with the file highlighted). A
// file that is not on the disk yet opens its folder, if that exists.
func (m *Manager) OpenFile(hash string, index int) error {
	paths, err := m.filePaths(hash)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(paths) {
		return fmt.Errorf("%w: file index %d out of range", ErrInvalidInput, index)
	}
	target := paths[index]
	if _, err := os.Stat(target); err != nil {
		target = filepath.Dir(target)
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("%w: the file is not on the disk yet", ErrNotFound)
		}
	}
	return openInFileManager(target)
}

// DeleteFiles takes files out of a torrent for good: they are marked "do not download" and deleted from the
// disk. The engine is told afterwards that the pieces that held them are gone, so the torrent does not go on
// believing it has them. It returns how many files were deleted.
func (m *Manager) DeleteFiles(hash string, indexes []int) (int, error) {
	if len(indexes) == 0 {
		return 0, errInvalid("no files given")
	}
	paths, err := m.filePaths(hash)
	if err != nil {
		return 0, err
	}
	for _, i := range indexes {
		if i < 0 || i >= len(paths) {
			return 0, fmt.Errorf("%w: file index %d out of range", ErrInvalidInput, i)
		}
	}
	if m.isMoving(hash) {
		return 0, ErrBusy
	}
	// First stop wanting them, or the engine would fetch the data again.
	if err := m.SetFilePriorities(hash, indexes, PrioSkip); err != nil {
		return 0, err
	}
	// Let go of the files the engine may still hold open.
	if h, ok := parseHash(hash); ok {
		var victims []string
		for _, i := range indexes {
			victims = append(victims, paths[i])
		}
		unblock := m.store.BlockFiles(metainfo.Hash(h), victims)
		defer unblock()
	}
	deleted := 0
	var first error
	for _, i := range indexes {
		if _, err := os.Stat(paths[i]); err != nil {
			continue // nothing on the disk
		}
		if err := removeFile(paths[i]); err != nil {
			if first == nil {
				first = deleteError(paths[i], err)
			}
			continue
		}
		deleted++
	}
	m.reverifyFiles(hash, indexes)
	return deleted, first
}

// removeFile deletes one file, retrying briefly: the engine may still hold it open for a moment.
func removeFile(path string) error {
	return retryRemove(func() error { return os.Remove(path) })
}

// reverifyFiles makes the engine look at the pieces of the given files again, in the background: the files
// are gone, so those pieces stop counting as complete.
func (m *Manager) reverifyFiles(hash string, indexes []int) {
	t, err := m.get(hash)
	if err != nil || t.Info() == nil {
		return
	}
	files := t.Files()
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-m.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		for _, i := range indexes {
			if i < 0 || i >= len(files) {
				continue
			}
			for p := files[i].BeginPieceIndex(); p < files[i].EndPieceIndex(); p++ {
				if ctx.Err() != nil {
					return
				}
				_ = t.Piece(p).VerifyDataContext(ctx)
			}
		}
	}()
}

// deleteError explains why a file could not be deleted; a file held by another program is the usual reason.
func deleteError(path string, err error) error {
	name := filepath.Base(path)
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == 32 || strings.Contains(err.Error(), "used by another process") {
		return &CodedError{Code: "file.busy", Args: []string{name},
			Msg: fmt.Sprintf("не удалось удалить «%s»: файл занят другой программой (плеером, предпросмотром в Проводнике, антивирусом). Закройте её и повторите", name)}
	}
	return &CodedError{Code: "file.delete", Args: []string{name, err.Error()}, Msg: fmt.Sprintf("не удалось удалить «%s»: %v", name, err), Err: err}
}
