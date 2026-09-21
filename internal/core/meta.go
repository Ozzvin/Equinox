package core

import (
	"os"
	"path/filepath"

	"github.com/anacrolix/torrent/metainfo"
)

// metaPath is the internal copy of a torrent's metadata. Unlike the copy in the user's
// folder it always exists, so a torrent can be restored (after a restart or a move) no
// matter what the copy settings are.
func (m *Manager) metaPath(hash string) string {
	return filepath.Join(m.stateDir, "meta", hash+".torrent")
}

func (m *Manager) hasMeta(hash string) bool {
	_, err := os.Stat(m.metaPath(hash))
	return err == nil
}

func (m *Manager) saveMeta(mi *metainfo.MetaInfo, hash string) error {
	p := m.metaPath(hash)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}
