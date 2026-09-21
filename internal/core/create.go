package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// validateTracker checks and normalises an announce URL.
func validateTracker(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%w: %q is not a tracker address", ErrInvalidInput, raw)
	}
	switch u.Scheme {
	case "http", "https", "udp", "ws", "wss":
		return raw, nil
	}
	return "", fmt.Errorf("%w: unsupported tracker scheme %q", ErrInvalidInput, u.Scheme)
}

// CreateRequest describes a .torrent file to be made from local data.
type CreateRequest struct {
	Source      string   `json:"source"`      // a file or a folder
	Output      string   `json:"output"`      // the .torrent to write, or a folder for it; empty = next to the source
	Trackers    []string `json:"trackers"`    // announce URLs, one tier each
	Comment     string   `json:"comment"`     //
	Private     bool     `json:"private"`     // no DHT/PEX for this torrent
	PieceLength int64    `json:"pieceLength"` // 0 = choose from the size
	Seed        bool     `json:"seed"`        // add it to the client and seed from Source
}

// CreateJob is the state of a torrent creation running in the background.
type CreateJob struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Running bool      `json:"running"`
	Error   string    `json:"error"`
	Output  string    `json:"output"` // the written file
	Hash    string    `json:"hash"`
	Seeding bool      `json:"seeding"` // it was added to the client
	Started time.Time `json:"started"`
	Size    int64     `json:"size"` // bytes being hashed
}

const maxCreateJobs = 20

// StartCreate validates the request and builds the torrent in the background. Hashing big
// data takes a while, so the caller follows the job with Create.
func (m *Manager) StartCreate(req CreateRequest) (CreateJob, error) {
	src, err := filepath.Abs(strings.TrimSpace(req.Source))
	if err != nil || req.Source == "" {
		return CreateJob{}, fmt.Errorf("%w: choose a file or a folder", ErrInvalidInput)
	}
	fi, err := os.Stat(src)
	if err != nil {
		return CreateJob{}, fmt.Errorf("%w: %q not found", ErrInvalidInput, src)
	}
	var size int64
	_ = filepath.WalkDir(src, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if i, e := d.Info(); e == nil {
				size += i.Size()
			}
		}
		return nil
	})
	if size == 0 {
		return CreateJob{}, fmt.Errorf("%w: there is no data to make a torrent from", ErrInvalidInput)
	}
	var trackers []string
	for _, t := range req.Trackers {
		if strings.TrimSpace(t) == "" {
			continue
		}
		v, err := validateTracker(t)
		if err != nil {
			return CreateJob{}, err
		}
		trackers = append(trackers, v)
	}
	if req.PieceLength != 0 && (req.PieceLength < 16<<10 || req.PieceLength&(req.PieceLength-1) != 0) {
		return CreateJob{}, fmt.Errorf("%w: the piece size must be a power of two, at least 16 KiB", ErrInvalidInput)
	}
	if len(req.Comment) > 500 {
		return CreateJob{}, fmt.Errorf("%w: the comment is too long", ErrInvalidInput)
	}

	name := filepath.Base(src)
	out := strings.TrimSpace(req.Output)
	switch {
	case out == "":
		out = filepath.Join(filepath.Dir(src), name+".torrent")
	default:
		if o, err := os.Stat(out); err == nil && o.IsDir() {
			out = filepath.Join(out, name+".torrent")
		}
	}
	if out, err = filepath.Abs(out); err != nil {
		return CreateJob{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !strings.EqualFold(filepath.Ext(out), ".torrent") {
		out += ".torrent"
	}
	if _, err := os.Stat(out); err == nil {
		return CreateJob{}, fmt.Errorf("%w: %q already exists", ErrInvalidInput, out)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return CreateJob{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	_ = fi

	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	job := &CreateJob{ID: hex.EncodeToString(raw), Name: name, Running: true, Output: out, Started: time.Now(), Size: size}

	m.mu.Lock()
	if len(m.creates) >= maxCreateJobs { // forget the oldest finished ones
		for id, j := range m.creates {
			if !j.Running && time.Since(j.Started) > 10*time.Minute {
				delete(m.creates, id)
			}
		}
	}
	m.creates[job.ID] = job
	snapshot := *job
	m.mu.Unlock()

	go m.runCreate(job, src, req, trackers)
	return snapshot, nil
}

func (m *Manager) runCreate(job *CreateJob, src string, req CreateRequest, trackers []string) {
	fail := func(err error) {
		m.mu.Lock()
		job.Running, job.Error = false, err.Error()
		m.mu.Unlock()
	}

	info := metainfo.Info{PieceLength: req.PieceLength}
	if req.Private {
		yes := true
		info.Private = &yes
	}
	if err := info.BuildFromFilePath(src); err != nil {
		fail(err)
		return
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		fail(err)
		return
	}
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes, Comment: req.Comment, CreatedBy: "Equinox", CreationDate: time.Now().Unix(),
	}
	if len(trackers) > 0 {
		mi.Announce = trackers[0]
		for _, t := range trackers {
			mi.AnnounceList = append(mi.AnnounceList, []string{t})
		}
	}

	f, err := os.OpenFile(job.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // never overwrite
	if err != nil {
		fail(err)
		return
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		_ = os.Remove(job.Output)
		fail(err)
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(job.Output)
		fail(err)
		return
	}
	hash := mi.HashInfoBytes().HexString()

	seeding := false
	if req.Seed {
		// The data is already on disk, so the folder that holds it becomes the save folder and
		// the engine verifies it and starts seeding.
		if _, err := m.AddMetaInfo(&mi, WithSavePath(filepath.Dir(src))); err != nil {
			m.mu.Lock()
			job.Hash, job.Running = hash, false
			job.Error = "the file was written, but adding it to the client failed: " + err.Error()
			m.mu.Unlock()
			return
		}
		seeding = true
	}
	m.mu.Lock()
	job.Hash, job.Seeding, job.Running = hash, seeding, false
	m.mu.Unlock()
}

// Create returns the state of a creation job.
func (m *Manager) Create(id string) (CreateJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.creates[id]
	if !ok {
		return CreateJob{}, ErrNotFound
	}
	return *j, nil
}
