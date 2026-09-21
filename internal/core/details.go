package core

import (
	"context"
	"sort"
	"time"

	"github.com/anacrolix/torrent"
)

// Details is the static information about a torrent.
type Details struct {
	Hash            string    `json:"hash"`
	Name            string    `json:"name"`
	TotalSize       int64     `json:"totalSize"`
	Files           int       `json:"files"`
	Pieces          int       `json:"pieces"`
	PieceLength     int64     `json:"pieceLength"`
	PiecesDone      int       `json:"piecesDone"`
	Private         bool      `json:"private"`
	Comment         string    `json:"comment"`
	CreatedBy       string    `json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"` // zero if the torrent does not say
	Added           time.Time `json:"added"`
	SavePath        string    `json:"savePath"`
	CopyPath        string    `json:"copyPath"`
	Magnet          string    `json:"magnet"`
	Trackers        []Tracker `json:"trackers"`
	MaxConns        int       `json:"maxConns"`        // this torrent's own connection limit, 0 = the global setting
	ConnLimit       int       `json:"connLimit"`       // the limit actually in force
	RatioLimit      float64   `json:"ratioLimit"`      // this torrent's own stop-seeding ratio, 0 = the global setting
	RatioInForce    float64   `json:"ratioInForce"`    // the ratio limit actually in force, 0 = none
	SeedTimeLimit   int       `json:"seedTimeLimit"`   // this torrent's own seeding time limit in minutes, 0 = the global setting
	SeedTimeInForce int       `json:"seedTimeInForce"` // the seeding time limit actually in force, 0 = none
	SeedSeconds     int64     `json:"seedSeconds"`
	// options of the torrent
	Sequential bool   `json:"sequential"`
	EdgePieces bool   `json:"edgePieces"`
	MoveDone   string `json:"moveDone"` // this torrent's own "move when finished" folder, "" = the global setting
	Label      string `json:"label"`
}

// Tracker is one announce URL. The engine does not report tracker health, so there is no
// status here, only the list.
type Tracker struct {
	URL   string `json:"url"`
	Tier  int    `json:"tier"`
	Added bool   `json:"added"` // added by the user rather than by the torrent itself
}

// Details returns the general information about a torrent whose metadata is known.
func (m *Manager) Details(hash string) (Details, error) {
	t, err := m.get(hash)
	if err != nil {
		return Details{}, err
	}
	info := t.Info()
	if info == nil {
		return Details{}, ErrNoMetadata
	}
	mi := t.Metainfo()
	rec := m.snapshotRecords()[hash]

	d := Details{
		Hash: hash, Name: t.Name(), TotalSize: t.Length(), Files: len(t.Files()),
		Pieces: t.NumPieces(), PieceLength: info.PieceLength,
		Private: info.Private != nil && *info.Private,
		Comment: mi.Comment, CreatedBy: mi.CreatedBy, Added: rec.Added,
		SavePath: m.saveDir(hash), CopyPath: rec.CopyPath,
		Magnet:   mi.Magnet(nil, info).String(),
		MaxConns: rec.MaxConns, ConnLimit: m.connLimit(rec),
		RatioLimit: rec.RatioLimit, RatioInForce: rec.RatioLimit,
		SeedTimeLimit: rec.SeedTimeLimit, SeedTimeInForce: m.seedLimit(rec), SeedSeconds: m.seedSeconds(hash, rec),
		Sequential: rec.Sequential, EdgePieces: rec.EdgePieces, MoveDone: rec.MoveDone, Label: rec.Label,
	}
	if mi.CreationDate > 0 {
		d.CreatedAt = time.Unix(mi.CreationDate, 0)
	}
	d.PiecesDone = t.Stats().PiecesComplete
	if d.RatioInForce == 0 {
		d.RatioInForce = m.cfg.Get().RatioLimit
	}

	extra := map[string]bool{}
	for _, u := range rec.ExtraTrackers {
		extra[u] = true
	}
	for tier, urls := range mi.UpvertedAnnounceList() {
		for _, u := range urls {
			d.Trackers = append(d.Trackers, Tracker{URL: u, Tier: tier + 1, Added: extra[u]})
		}
	}
	return d, nil
}

// Peer is one connection of a torrent.
type Peer struct {
	Addr       string  `json:"addr"`
	Client     string  `json:"client"`
	Progress   float64 `json:"progress"`
	DownRate   int64   `json:"downRate"`
	UpRate     int64   `json:"upRate"`
	Downloaded int64   `json:"downloaded"`
	Uploaded   int64   `json:"uploaded"`
	Incoming   bool    `json:"incoming"`
	Source     string  `json:"source"` // tracker, dht, pex, incoming, other
	Network    string  `json:"network"`
}

const maxPeers = 200

func sourceName(s torrent.PeerSource) string {
	switch s {
	case torrent.PeerSourceTracker:
		return "tracker"
	case torrent.PeerSourceDhtGetPeers, torrent.PeerSourceDhtAnnouncePeer:
		return "dht"
	case torrent.PeerSourcePex:
		return "pex"
	case torrent.PeerSourceIncoming:
		return "incoming"
	}
	return "other"
}

// Peers lists the current connections, the busiest first.
func (m *Manager) Peers(hash string) ([]Peer, error) {
	t, err := m.get(hash)
	if err != nil {
		return nil, err
	}
	n := t.NumPieces()
	conns := t.PeerConns()
	out := make([]Peer, 0, len(conns))
	for _, pc := range conns {
		st := pc.Stats()
		p := Peer{
			Addr: pc.RemoteAddr.String(), Network: pc.Network,
			Downloaded: st.BytesReadUsefulData.Int64(), Uploaded: st.BytesWrittenData.Int64(),
			Source: sourceName(pc.Discovery),
		}
		p.DownRate, p.UpRate = m.peerSpeed(hash, p.Addr, p.Downloaded, p.Uploaded)
		if v, ok := pc.PeerClientName.Load().(string); ok {
			p.Client = v
		}
		if n > 0 {
			p.Progress = float64(st.RemotePieceCount) / float64(n)
			if p.Progress > 1 {
				p.Progress = 1
			}
		}
		if out, ok := isOutgoing(pc); ok {
			p.Incoming = !out
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := a.DownRate+a.UpRate, b.DownRate+b.UpRate; ra != rb {
			return ra > rb
		}
		return a.Addr < b.Addr
	})
	if len(out) > maxPeers {
		out = out[:maxPeers]
	}
	return out, nil
}

// AddTracker adds an announce URL to a torrent. It is remembered across restarts.
func (m *Manager) AddTracker(hash, raw string) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	raw, err = validateTracker(raw)
	if err != nil {
		return err
	}
	added := false
	if err := m.state.with(func(s *state) {
		r := s.Torrents[hash]
		if r == nil {
			return
		}
		for _, x := range r.ExtraTrackers {
			if x == raw {
				return
			}
		}
		r.ExtraTrackers = append(r.ExtraTrackers, raw)
		added = true
	}); err != nil {
		return err
	}
	if added {
		t.AddTrackers([][]string{{raw}})
	}
	return nil
}

// reapplyTrackers gives a restored torrent the trackers the user added earlier.
func (m *Manager) reapplyTrackers(t *torrent.Torrent, r record) {
	if len(r.ExtraTrackers) > 0 {
		t.AddTrackers([][]string{append([]string(nil), r.ExtraTrackers...)})
	}
}

// Recheck re-hashes all data of a torrent and corrects what the engine believes about it.
// It runs in the background; Status.Checking is true meanwhile.
func (m *Manager) Recheck(hash string) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	if t.Info() == nil {
		return ErrNoMetadata
	}
	m.mu.Lock()
	if m.checking[hash] {
		m.mu.Unlock()
		return ErrBusy
	}
	m.checking[hash] = true
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.checking, hash)
			// The loop only refreshes progress of torrents it watches, which this one no
			// longer is: without this a last percentage below 100 could stay on the status.
			if m.phase[hash] == nil {
				delete(m.checkProg, hash)
			}
			m.mu.Unlock()
		}()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { // stop waiting when the manager shuts down
			select {
			case <-m.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		// With a limit on simultaneous checks this waits its turn behind the torrents that are being checked.
		release, ok := m.acquireRecheck(hash, ctx.Done())
		if !ok {
			return
		}
		defer release()
		_ = t.VerifyDataContext(ctx)
	}()
	return nil
}
