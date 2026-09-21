package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"

	"github.com/Ozzvin/equinox/internal/config"
)

// Status is a point-in-time view of one torrent.
type Status struct {
	Hash          string    `json:"hash"`
	Name          string    `json:"name"`
	Size          int64     `json:"size"`      // of the selected (not skipped) files
	TotalSize     int64     `json:"totalSize"` // of the whole torrent
	Done          int64     `json:"done"`
	Progress      float64   `json:"progress"`
	DownRate      int64     `json:"downRate"` // bytes/s
	Added         time.Time `json:"added"`    // when the torrent was added
	UpRate        int64     `json:"upRate"`
	Active        bool      `json:"active"`     // moved data in the last few seconds
	Downloaded    int64     `json:"downloaded"` // lifetime payload bytes
	Uploaded      int64     `json:"uploaded"`
	Ratio         float64   `json:"ratio"`
	RatioLimit    float64   `json:"ratioLimit"`  // effective limit, 0 = none
	SeedSeconds   int64     `json:"seedSeconds"` // time spent seeding
	SeedLimit     int       `json:"seedLimit"`   // effective seeding time limit in minutes, 0 = none
	Peers         int       `json:"peers"`
	Seeds         int       `json:"seeds"`
	Paused        bool      `json:"paused"`
	Label         string    `json:"label"`
	SavePath      string    `json:"savePath"`
	Error         string    `json:"error"`         // why the engine stopped this torrent, "" if it did not
	ErrorKind     string    `json:"errorKind"`     // disk_full | write | other
	Checking      bool      `json:"checking"`      // files are being checked against their hashes
	CheckProgress float64   `json:"checkProgress"` // 0..1 while Checking
	CheckQueued   int       `json:"checkQueued"`   // 0 = not waiting; N = place in the line for a check of local files (1 = next)
	Moving        float64   `json:"moving"`        // > 0 while files are being moved (fraction copied; -1 = a plain rename)
	MoveError     string    `json:"moveError"`     // last failed move, "" if none
	MoveNote      string    `json:"moveNote"`      // a move that worked but left something behind
	Queued        int       `json:"queued"`        // 0 = not waiting; N = position in the wait line (1 = next)
	Sequential    bool      `json:"sequential"`
	HasMeta       bool      `json:"hasMetadata"`
	CopyPath      string    `json:"copyPath,omitempty"`
}

// ---------------------------------------------------------------- speed limits

// applyLimits pushes the active limit set (normal or "turtle") to the engine.
func (m *Manager) applyLimits() {
	s := m.cfg.Get()
	down, up := s.DownLimitKBps, s.UpLimitKBps
	if s.AltSpeedActive {
		down, up = s.AltDownLimitKBps, s.AltUpLimitKBps
	}
	setLimit(m.downLim, down)
	setLimit(m.upLim, up)
}

func setLimit(l *rate.Limiter, kbps int) {
	if kbps <= 0 {
		l.SetLimit(rate.Inf)
		return
	}
	bps := kbps << 10
	l.SetLimit(rate.Limit(bps))
	// Burst must fit at least one chunk, or the limiter would stall transfers.
	burst := bps
	if burst < 64<<10 {
		burst = 64 << 10
	}
	l.SetBurst(burst)
}

// SetAltSpeed toggles the turtle mode.
func (m *Manager) SetAltSpeed(on bool) error {
	if err := m.cfg.Update(func(s *config.Settings) { s.AltSpeedActive = on }); err != nil {
		return err
	}
	m.applyLimits()
	return nil
}

// SetLimits changes the normal and alternative limits (KiB/s, 0 = unlimited).
func (m *Manager) SetLimits(down, up, altDown, altUp int) error {
	if err := m.cfg.Update(func(s *config.Settings) {
		s.DownLimitKBps, s.UpLimitKBps = down, up
		s.AltDownLimitKBps, s.AltUpLimitKBps = altDown, altUp
	}); err != nil {
		return err
	}
	m.applyLimits()
	return nil
}

// ---------------------------------------------------------------- per-torrent controls

// parseHash converts a hex info hash that came from outside. The library's own parser
// panics on bad input, so the length and alphabet are checked first.
func parseHash(s string) (metainfo.Hash, bool) {
	var h metainfo.Hash
	if len(s) != 40 {
		return h, false
	}
	if _, err := hex.Decode(h[:], []byte(s)); err != nil {
		return h, false
	}
	return h, true
}

func (m *Manager) get(hash string) (*torrent.Torrent, error) {
	h, ok := parseHash(hash)
	if !ok {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.torrents[h]
	if !ok {
		if j := m.moves[hash]; j != nil && j.running {
			return nil, ErrBusy
		}
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *Manager) SetPaused(hash string, paused bool) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	if !paused {
		m.clearError(hash) // resuming is the user's retry: forget the old failure
	}
	m.setPaused(t, hash, paused)
	return nil
}

func (m *Manager) setPaused(t *torrent.Torrent, hash string, paused bool) {
	_ = m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.Paused = paused
		}
	})
	m.applyPause(t, hash, paused)
}

func (m *Manager) applyPause(t *torrent.Torrent, hash string, paused bool) {
	m.syncPerm(t, hash) // the record already says whether it is paused
	if !paused && t.Info() != nil {
		m.begin(t, hash)
	}
}

// SetSequential enables in-order download with prioritised file edges, for playing
// media while it downloads.
func (m *Manager) SetSequential(hash string, on bool) error {
	t, err := m.get(hash)
	if err != nil {
		return err
	}
	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.Sequential = on
		}
	}); err != nil {
		return err
	}
	if on {
		m.prioritise(t, true)
	}
	return nil
}

// MaxLabelLen bounds label length (in runes).
const MaxLabelLen = 40

// SetLabel puts the torrent in a category; an empty label clears it.
func (m *Manager) SetLabel(hash, label string) error {
	if _, err := m.get(hash); err != nil {
		return err
	}
	label = strings.TrimSpace(label)
	if r := []rune(label); len(r) > MaxLabelLen {
		return fmt.Errorf("%w: label is longer than %d characters", ErrInvalidInput, MaxLabelLen)
	}
	return m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.Label = label
		}
	})
}

// SetRatioLimit sets the per-torrent stop-seeding ratio (0 = use the global default).
func (m *Manager) SetRatioLimit(hash string, limit float64) error {
	if _, err := m.get(hash); err != nil {
		return err
	}
	return m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			r.RatioLimit = limit
		}
	})
}

// prioritise raises the first and last pieces of every file (media containers keep
// their header and index there) and, with withWindow, a sliding window of the next pieces in order.
func (m *Manager) prioritise(t *torrent.Torrent, withWindow bool) {
	if t.Info() == nil {
		return
	}
	s := m.cfg.Get()
	edge, window := s.EdgePieces, s.SequentialWindow
	prios := m.filePrios(t.InfoHash().HexString())
	wanted := wantedPieces(t, prios) // never fetch pieces that only belong to skipped files

	for fi, f := range t.Files() {
		if prioAt(prios, fi) == PrioSkip {
			continue
		}
		b, e := f.BeginPieceIndex(), f.EndPieceIndex() // [b, e)
		for i := b; i < e && i < b+edge; i++ {
			raise(t, i, torrent.PiecePriorityHigh)
		}
		for i := e - 1; i >= b && i >= e-edge; i-- {
			raise(t, i, torrent.PiecePriorityHigh)
		}
	}

	if !withWindow {
		return // only the file edges were asked for
	}

	// Sliding window: the first missing pieces get priority in order.
	n := t.NumPieces()
	raised := 0
	for i := 0; i < n && raised < window; i++ {
		if !wanted[i] || t.Piece(i).State().Complete {
			continue
		}
		prio := torrent.PiecePriorityReadahead
		if raised < 2 {
			prio = torrent.PiecePriorityNow
		}
		raise(t, i, prio)
		raised++
	}
}

func raise(t *torrent.Torrent, i int, prio torrent.PiecePriority) {
	p := t.Piece(i)
	if !p.State().Complete {
		p.SetPriority(prio)
	}
}

// FileStatus describes one file of a torrent.
type FileStatus struct {
	Index    int     `json:"index"`
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	Done     int64   `json:"done"`
	Progress float64 `json:"progress"`
	Priority string  `json:"priority"` // skip | normal | high
}

// Files lists the files of a torrent whose metadata is known.
func (m *Manager) Files(hash string) ([]FileStatus, error) {
	t, err := m.get(hash)
	if err != nil {
		return nil, err
	}
	if t.Info() == nil {
		return nil, nil
	}
	fs := t.Files()
	prios := m.filePrios(hash)
	out := make([]FileStatus, len(fs))
	// the engine reports the path inside the torrent; a torrent made from a folder keeps that folder as its root
	root := ""
	if info := t.Info(); info != nil && info.IsDir() {
		root = t.Name() + "/"
	}
	for i, f := range fs {
		done := f.BytesCompleted()
		p := 0.0
		if f.Length() > 0 {
			p = float64(done) / float64(f.Length())
		}
		name := "normal"
		switch prioAt(prios, i) {
		case PrioSkip:
			name = "skip"
		case PrioHigh:
			name = "high"
		case PrioLow:
			name = "low"
		}
		out[i] = FileStatus{Index: i, Path: root + f.DisplayPath(), Size: f.Length(), Done: done, Progress: p, Priority: name}
	}
	return out, nil
}

// Stream is a seekable reader over one file that downloads what is being read first.
type Stream struct {
	torrent.Reader
	Name string
	Size int64
}

// OpenStream opens file idx for playback while it downloads. The reader prioritises
// the pieces around the read position, so seeking works before the download is done.
func (m *Manager) OpenStream(hash string, idx int) (*Stream, error) {
	t, err := m.get(hash)
	if err != nil {
		return nil, err
	}
	if t.Info() == nil {
		return nil, ErrNoMetadata
	}
	fs := t.Files()
	if idx < 0 || idx >= len(fs) {
		return nil, ErrNotFound
	}
	f := fs[idx]
	r := f.NewReader()
	r.SetResponsive() // return data as soon as it is available
	r.SetReadahead(int64(m.cfg.Get().SequentialWindow) * t.Info().PieceLength)
	// Keep the edges of the file hot: players read the header and the index first.
	m.prioritise(t, true)
	return &Stream{Reader: r, Name: filepath.Base(f.DisplayPath()), Size: f.Length()}, nil
}

// ---------------------------------------------------------------- counters & status

func counters(t *torrent.Torrent) (down, up int64) {
	st := t.Stats()
	return st.BytesReadUsefulData.Int64(), st.BytesWrittenData.Int64()
}

// syncCounters folds the engine's session counters into the persisted records.
func (m *Manager) syncCounters() {
	m.flushSeedTime()
	m.mu.Lock()
	type delta struct {
		hash     string
		down, up int64
	}
	var ds []delta
	for h, t := range m.torrents {
		d, u := counters(t)
		if dd, du := d-m.seenDown[h], u-m.seenUp[h]; dd != 0 || du != 0 {
			ds = append(ds, delta{h.HexString(), dd, du})
			m.seenDown[h], m.seenUp[h] = d, u
		}
	}
	m.mu.Unlock()
	if len(ds) == 0 {
		return
	}
	_ = m.state.with(func(s *state) {
		for _, d := range ds {
			if r := s.Torrents[d.hash]; r != nil {
				r.Downloaded += d.down
				r.Uploaded += d.up
			}
		}
	})
}

// finalCounters returns the not-yet-persisted traffic of a torrent about to be removed.
func (m *Manager) finalCounters(t *torrent.Torrent, hash string) (down, up int64) {
	d, u := counters(t)
	h := t.InfoHash()
	m.mu.Lock()
	down, up = d-m.seenDown[h], u-m.seenUp[h]
	m.mu.Unlock()
	var r record
	m.state.view(func(s *state) {
		if x := s.Torrents[hash]; x != nil {
			r = *x
		}
	})
	return down + r.Downloaded, up + r.Uploaded
}

func ratio(down, up, size int64) float64 {
	den := down
	if den == 0 {
		den = size // seeding data that was not downloaded here
	}
	if den <= 0 {
		return 0
	}
	return float64(up) / float64(den)
}

// List returns the status of every torrent, newest first.
func (m *Manager) List() []Status {
	m.syncCounters()
	global := m.cfg.Get().RatioLimit

	recs := map[string]record{}
	m.state.view(func(s *state) {
		for h, r := range s.Torrents {
			recs[h] = *r
		}
	})

	m.mu.Lock()
	ts := make([]*torrent.Torrent, 0, len(m.torrents))
	for _, t := range m.torrents {
		ts = append(ts, t)
	}
	rates := map[metainfo.Hash][2]int64{}
	for h, r := range m.rates {
		rates[h] = r
	}
	activeAt := map[metainfo.Hash]time.Time{}
	for h, at := range m.activeAt {
		activeAt[h] = at
	}
	queued := map[metainfo.Hash]int{}
	for h, q := range m.queued {
		queued[h] = q
	}
	type moveView struct {
		name      string
		sel       int64
		running   bool
		frac      float64
		err, note string
	}
	errs := map[string]torrentError{}
	for h, e := range m.errs {
		errs[h] = e
	}
	checkProg := map[string]float64{}
	for h, p := range m.checkProg {
		checkProg[h] = p
	}
	checking := map[string]bool{}
	for h := range m.checking {
		checking[h] = true
	}
	moves := map[string]moveView{}
	for h, j := range m.moves {
		v := moveView{name: j.name, sel: j.selSize, running: j.running, frac: j.fraction(), err: j.err, note: j.warning}
		if j.running {
			switch {
			case j.copySize == 0:
				v.frac = -1 // rename in progress: no meaningful percentage
			case v.frac < 0.001:
				v.frac = 0.001 // 0 is reserved for "not moving"
			}
		}
		moves[h] = v
	}
	m.mu.Unlock()

	out := make([]Status, 0, len(ts))
	for _, t := range ts {
		h := t.InfoHash()
		r := recs[h.HexString()]
		st := Status{
			Hash: h.HexString(), Name: t.Name(), Downloaded: r.Downloaded, Uploaded: r.Uploaded,
			Added: r.Added, Paused: r.Paused, Sequential: r.Sequential, Label: r.Label, SavePath: m.saveDir(h.HexString()), CopyPath: r.CopyPath,
			DownRate: rates[h][0], UpRate: rates[h][1], Queued: queued[h],
			Active: time.Since(activeAt[h]) < activeHold,
		}
		if t.Info() != nil {
			st.HasMeta = true
			st.TotalSize = t.Length()
			st.Size, st.Done = selection(t, r.FilePrios)
			if st.Size > 0 {
				st.Progress = float64(st.Done) / float64(st.Size)
			}
		} else if pos, size := m.checkQueuePos(st.Hash); pos > 0 {
			// waiting for a free slot to have its local files checked: the size is known, the info is not handed over yet
			st.CheckQueued, st.Size, st.TotalSize = pos, size, size
		}
		ts := t.Stats()
		st.Peers, st.Seeds = ts.ActivePeers, ts.ConnectedSeeders
		st.Ratio = ratio(r.Downloaded, r.Uploaded, st.Size)
		st.RatioLimit = r.RatioLimit
		if st.RatioLimit == 0 {
			st.RatioLimit = global
		}
		st.SeedSeconds, st.SeedLimit = m.seedSeconds(st.Hash, r), m.seedLimit(r)
		st.CheckProgress = checkProg[st.Hash]
		_, checkingNow := checkProg[st.Hash]
		st.Checking = checking[st.Hash] || checkingNow
		st.Error, st.ErrorKind = errs[st.Hash].msg, errs[st.Hash].kind
		if v, ok := moves[st.Hash]; ok {
			st.MoveError, st.MoveNote = v.err, v.note
		}
		out = append(out, st)
	}
	// Torrents whose files are being moved are out of the engine for a moment; keep showing them.
	seen := map[string]bool{}
	for _, st := range out {
		seen[st.Hash] = true
	}
	for hsh, v := range moves {
		if seen[hsh] || !v.running {
			continue
		}
		r := recs[hsh]
		out = append(out, Status{
			Hash: hsh, Name: v.name, Size: v.sel, TotalSize: v.sel, Done: v.sel, Progress: 1,
			Downloaded: r.Downloaded, Uploaded: r.Uploaded, Ratio: ratio(r.Downloaded, r.Uploaded, v.sel),
			Paused: r.Paused, Label: r.Label, SavePath: m.saveDir(hsh), HasMeta: true, Moving: v.frac,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := recs[out[i].Hash], recs[out[j].Hash]
		return orderLess(&a, &b)
	})
	return out
}

// GlobalRatio is the lifetime share ratio across all torrents, including removed ones.
func (m *Manager) GlobalRatio() (down, up int64, r float64) {
	m.syncCounters()
	m.state.view(func(s *state) {
		down, up = s.RemovedDownloaded, s.RemovedUploaded
		for _, t := range s.Torrents {
			down += t.Downloaded
			up += t.Uploaded
		}
	})
	if down > 0 {
		r = float64(up) / float64(down)
	}
	return
}

// ---------------------------------------------------------------- background loop

func (m *Manager) loop(ctx context.Context) {
	defer close(m.done)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastDown, lastUp := map[metainfo.Hash]int64{}, map[metainfo.Hash]int64{}
	hist := map[metainfo.Hash]*window{}
	flush, tickN := 0, 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		m.mu.Lock()
		ts := make([]*torrent.Torrent, 0, len(m.torrents))
		for _, t := range m.torrents {
			ts = append(ts, t)
		}
		m.mu.Unlock()

		rates := map[metainfo.Hash][2]int64{}
		moved := map[metainfo.Hash]bool{}
		for _, t := range ts {
			h := t.InfoHash()
			d, u := counters(t)
			w := hist[h]
			if w == nil {
				w = &window{}
				hist[h] = w
			}
			w.add(d-lastDown[h], u-lastUp[h])
			moved[h] = d != lastDown[h] || u != lastUp[h]
			rd, ru := w.mean()
			rates[h] = [2]int64{rd, ru}
			lastDown[h], lastUp[h] = d, u
		}
		now := time.Now()
		m.mu.Lock()
		m.rates = rates
		for h, ok := range moved {
			if ok {
				m.activeAt[h] = now
			}
		}
		m.mu.Unlock()

		if flush++; flush%15 == 0 {
			m.syncCounters()
		}
		m.updateChecks(ts)
		m.refreshLow(ts)
		m.runGate()
		m.applySchedule(time.Now())
		if tickN++; tickN%5 == 0 {
			m.scanWatch()
		}
		m.applyQueue(ts, m.snapshotRecords())
		m.enforce(ts)
	}
}

// snapshotRecords copies all persisted torrent records.
func (m *Manager) snapshotRecords() map[string]record {
	out := map[string]record{}
	m.state.view(func(s *state) {
		for h, r := range s.Torrents {
			out[h] = *r
		}
	})
	return out
}

// enforce applies sequential priorities and the stop-seeding ratio limit.
func (m *Manager) enforce(ts []*torrent.Torrent) {
	global := m.cfg.Get().RatioLimit
	now := time.Now()
	dt := now.Sub(m.lastEnforce)
	if m.lastEnforce.IsZero() || dt > 10*time.Second { // the first tick, or the machine slept
		dt = 0
	}
	m.lastEnforce = now
	for _, t := range ts {
		hash := t.InfoHash().HexString()
		var r record
		m.state.view(func(s *state) {
			if x := s.Torrents[hash]; x != nil {
				r = *x
			}
		})
		if r.Paused || t.Info() == nil {
			continue
		}
		m.addActiveTime(hash, dt)
		if r.Sequential {
			m.prioritise(t, true)
		}
		m.handleCompletion(t, hash, r)
		limit := r.RatioLimit
		if limit == 0 {
			limit = global
		}
		size, done := selection(t, r.FilePrios)
		if done < size {
			continue
		}
		secs := m.addSeedTime(hash, r, dt)
		if limit > 0 {
			d, u := counters(t)
			m.mu.Lock()
			down := r.Downloaded + d - m.seenDown[t.InfoHash()]
			up := r.Uploaded + u - m.seenUp[t.InfoHash()]
			m.mu.Unlock()
			if ratio(down, up, t.Length()) >= limit {
				m.setPaused(t, hash, true)
				m.emit(Event{Kind: "limit", Hash: hash, Name: t.Name(), Detail: limitReason("ratio", limit)})
				continue
			}
		}
		if lim := m.seedLimit(r); lim > 0 && secs >= int64(lim)*60 {
			m.setPaused(t, hash, true)
			m.emit(Event{Kind: "limit", Hash: hash, Name: t.Name(), Detail: limitReason("time", float64(lim))})
		}
	}
}
