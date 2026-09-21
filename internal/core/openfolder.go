package core

import (
	"fmt"
	"os"
	"path/filepath"
)

// openInFileManager shows path in the system file manager. It is a variable so tests do
// not open real windows.
var openInFileManager = openPath

// OpenFolder shows a torrent's data in the file manager: the torrent's own folder or file
// when it exists, otherwise the folder it is saved in. The path comes from the torrent's
// record, never from the request.
func (m *Manager) OpenFolder(hash string) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	base := m.saveDir(hash)
	target := base
	if info := t.Info(); info != nil {
		root := filepath.Join(base, info.BestName())
		if _, err := os.Stat(root); err == nil {
			target = root
		}
	}
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("%w: %q does not exist yet", ErrNotFound, target)
	}
	return openInFileManager(target)
}
