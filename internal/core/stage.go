package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/config"
)

// The "Add torrents" dialog can hold several torrents at once, each with its own options.
// A .torrent file is parsed when it is put on the list (so its files can be shown and chosen)
// and kept here, briefly, until the whole list is added.

const (
	maxStaged      = 100
	maxStagedBytes = 64 << 20
	stagedTTL      = 30 * time.Minute
)

type stagedEntry struct {
	mi    *metainfo.MetaInfo
	at    time.Time
	bytes int
}

// StagedFile is one file of a staged torrent.
type StagedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// StagedTorrent describes a .torrent file that was put on the list.
type StagedTorrent struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Hash     string       `json:"hash"`
	Size     int64        `json:"size"`
	Files    []StagedFile `json:"files"`
	Private  bool         `json:"private"`
	Comment  string       `json:"comment"`
	Trackers int          `json:"trackers"`
	Exists   bool         `json:"exists"` // it is already in the client
}

// Stage parses a torrent and keeps it for AddBatch.
func (m *Manager) Stage(mi *metainfo.MetaInfo) (StagedTorrent, error) {
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return StagedTorrent{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	hash := mi.HashInfoBytes().HexString()
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	st := StagedTorrent{
		ID: hex.EncodeToString(raw), Name: info.BestName(), Hash: hash, Size: info.TotalLength(),
		Private: info.Private != nil && *info.Private, Comment: mi.Comment,
		Trackers: len(mi.UpvertedAnnounceList().DistinctValues()), Exists: m.exists(hash),
	}
	for _, f := range info.UpvertedFiles() {
		st.Files = append(st.Files, StagedFile{Path: strings.Join(f.BestPath(), "/"), Size: f.Length})
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	total := len(mi.InfoBytes)
	for id, e := range m.staged {
		if now.Sub(e.at) > stagedTTL {
			delete(m.staged, id)
		} else {
			total += e.bytes
		}
	}
	if len(m.staged) >= maxStaged || total > maxStagedBytes {
		return StagedTorrent{}, fmt.Errorf("%w: too many torrents on the list at once, add or remove some first", ErrInvalidInput)
	}
	m.staged[st.ID] = &stagedEntry{mi: mi, at: now, bytes: len(mi.InfoBytes)}
	return st, nil
}

// Unstage forgets a staged torrent (it was removed from the list).
func (m *Manager) Unstage(id string) {
	m.mu.Lock()
	delete(m.staged, id)
	m.mu.Unlock()
}

// BatchItem is one entry of the list in the "Add torrents" dialog: exactly one of Stage
// (a staged .torrent), Magnet or Infohash, plus its options.
type BatchItem struct {
	Stage    string         `json:"stage"`
	Magnet   string         `json:"magnet"`
	Infohash string         `json:"infohash"`
	Options  TorrentOptions `json:"options"`
}

// BatchResult reports what happened to one entry.
type BatchResult struct {
	Index  int    `json:"index"`
	OK     bool   `json:"ok"`
	Hash   string `json:"hash"`
	Name   string `json:"name"`
	Exists bool   `json:"exists"` // it was already in the client, nothing was changed
	Error  string `json:"error"`
}

const maxBatch = 200

var (
	hexHash    = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	base32Hash = regexp.MustCompile(`^[A-Za-z2-7]{32}$`)
)

// AddBatch adds every entry with its own options. A failing entry does not stop the others.
func (m *Manager) AddBatch(items []BatchItem) ([]BatchResult, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: nothing to add", ErrInvalidInput)
	}
	if len(items) > maxBatch {
		return nil, fmt.Errorf("%w: at most %d torrents at a time", ErrInvalidInput, maxBatch)
	}
	out := make([]BatchResult, len(items))
	for i, it := range items {
		r := BatchResult{Index: i}
		hash, name, err := m.addOne(it)
		switch {
		case err == nil:
			r.OK, r.Hash, r.Name = true, hash, name
		case err == errAlreadyAdded:
			r.OK, r.Exists, r.Hash, r.Name = true, true, hash, name
		default:
			r.Error = err.Error()
		}
		out[i] = r
	}
	return out, nil
}

var errAlreadyAdded = fmt.Errorf("already in the client")

func (m *Manager) addOne(it BatchItem) (hash, name string, err error) {
	kinds := 0
	for _, s := range []string{it.Stage, it.Magnet, it.Infohash} {
		if strings.TrimSpace(s) != "" {
			kinds++
		}
	}
	if kinds != 1 {
		return "", "", fmt.Errorf("%w: an entry needs exactly one of a file, a magnet link or an infohash", ErrInvalidInput)
	}

	switch {
	case it.Stage != "":
		m.mu.Lock()
		e := m.staged[it.Stage]
		m.mu.Unlock()
		if e == nil {
			return "", "", fmt.Errorf("%w: the file is no longer on the list, add it again", ErrInvalidInput)
		}
		info, err := e.mi.UnmarshalInfo()
		if err != nil {
			return "", "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		hash, name = e.mi.HashInfoBytes().HexString(), info.BestName()
		if m.exists(hash) {
			m.Unstage(it.Stage)
			return hash, name, errAlreadyAdded
		}
		prios, err := filePriorities(it.Options.Files, len(info.UpvertedFiles()))
		if err != nil {
			return hash, name, err
		}
		if _, err := m.AddMetaInfo(e.mi, WithOptions(it.Options), WithFilePriorities(prios)); err != nil {
			return hash, name, err
		}
		m.Unstage(it.Stage)
		return hash, name, nil

	case it.Magnet != "":
		uri := strings.TrimSpace(it.Magnet)
		if !strings.HasPrefix(strings.ToLower(uri), "magnet:") {
			return "", "", fmt.Errorf("%w: %q is not a magnet link", ErrInvalidInput, uri)
		}
		return m.addMagnetWith(uri, it.Options)

	default:
		h := strings.TrimSpace(it.Infohash)
		if !hexHash.MatchString(h) && !base32Hash.MatchString(h) {
			return "", "", fmt.Errorf("%w: %q is not an infohash (40 hex or 32 base32 characters)", ErrInvalidInput, h)
		}
		return m.addMagnetWith("magnet:?xt=urn:btih:"+h, it.Options)
	}
}

// addMagnetWith adds a magnet link; file priorities cannot be given before its files are known.
func (m *Manager) addMagnetWith(uri string, o TorrentOptions) (hash, name string, err error) {
	if len(o.Files) > 0 {
		o.Files = nil
	}
	hash, err = m.AddMagnet(uri, WithOptions(o))
	return hash, hash, err
}

// ---------------------------------------------------------------- dialog defaults

// AddDialogInfo is what the dialog needs to start with: the saved defaults and the global
// settings they fall back to.
type AddDialogInfo struct {
	Defaults         config.AddDefaults `json:"defaults"`
	DataDir          string             `json:"dataDir"`
	Preallocate      bool               `json:"preallocate"`
	MoveCompletedDir string             `json:"moveCompletedDir"`
	MaxConns         int                `json:"maxConnsPerTorrent"`
	Labels           map[string]string  `json:"labels"` // label -> its folder
}

func (m *Manager) AddDialog() AddDialogInfo {
	s := m.cfg.Get()
	return AddDialogInfo{
		Defaults: s.Add, DataDir: s.DataDir, Preallocate: s.Preallocate,
		MoveCompletedDir: s.MoveCompletedDir, MaxConns: s.Network.MaxConnsPerTorrent, Labels: s.LabelPaths,
	}
}

// SetAddDefaults saves the dialog's starting choices.
func (m *Manager) SetAddDefaults(d config.AddDefaults) error {
	d.MoveDone = strings.TrimSpace(d.MoveDone)
	if d.MoveDoneEnabled && d.MoveDone != "" {
		abs, err := ensureDir(d.MoveDone)
		if err != nil {
			return err
		}
		d.MoveDone = abs
	}
	return m.cfg.Update(func(s *config.Settings) { s.Add = d })
}
