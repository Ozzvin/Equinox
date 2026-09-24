// Package core is the torrent daemon engine: it wraps the BitTorrent client and adds
// .torrent copies, safe removal, disk preallocation, share ratio and media-friendly
// piece prioritisation.
package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"github.com/Ozzvin/equinox/internal/buildinfo"
	"github.com/Ozzvin/equinox/internal/config"
	"github.com/Ozzvin/equinox/internal/portmap"
	"github.com/Ozzvin/equinox/internal/store"
)

var (
	ErrNotFound     = errors.New("torrent not found")
	ErrNoDiskSpace  = errors.New("not enough free disk space")
	ErrInvalidInput = errors.New("invalid torrent or magnet")
	ErrNoMetadata   = errors.New("torrent metadata is not available yet")
)

// Manager owns the engine client and everything layered on top of it.
type Manager struct {
	cfg        *config.Store
	state      *stateStore
	stateDir   string
	store      *store.Storage // the engine's file storage; kept to be told which files to trust
	startNet   config.Network // connection settings the engine was started with
	wantedPort int            // the port asked for at start; the engine may have had to use another
	cl         *torrent.Client
	comp       storage.PieceCompletion
	portMu     sync.Mutex
	ports      *portmap.Manager
	inbound    *inboundTracker
	self       selfIPs // our own address as the peers report it

	upLim, downLim *rate.Limiter

	mu       sync.Mutex
	torrents map[metainfo.Hash]*torrent.Torrent
	// counters already folded into the persisted record, per torrent (session-relative).
	seenDown, seenUp map[metainfo.Hash]int64
	rates            map[metainfo.Hash][2]int64  // bytes/s: down, up (averaged over a few seconds)
	activeAt         map[metainfo.Hash]time.Time // when each torrent last moved data
	lowMu            sync.Mutex
	lowOn            map[metainfo.Hash]bool // per torrent: were the low-priority files allowed to download at the last apply
	peerRates        map[string]*peerRate   // smoothed speeds of connections, by torrent and address
	peerSeen         map[string]time.Time
	perm             map[metainfo.Hash]permState // what the engine was last told each torrent may do
	queued           map[metainfo.Hash]int       // waiting torrents: 1 = next in line
	schedIn          bool                        // last evaluation of the turtle schedule window
	schedKnown       bool
	moves            map[string]*moveJob      // storage moves in progress or just failed, by hash
	checking         map[string]bool          // torrents being re-hashed
	errs             map[string]torrentError  // why a torrent was stopped by the engine, by hash
	watchSeen        map[string]watchStamp    // files seen in the watch folder on the previous scan
	creates          map[string]*CreateJob    // torrent creations, by job id
	staged           map[string]*stagedEntry  // .torrent files on the "Add torrents" list, by id
	pending          []PendingAdd             // magnets/staged files opened from outside, waiting for the dialog
	phase            map[string]*checkPhase   // torrents in their first check of local files
	checkProg        map[string]float64       // check progress (0..1) of torrents being checked
	held             map[string]*heldInfo     // torrents whose info waits for a free check slot
	gateActive       map[string]bool          // released torrents that are still in their first check
	rechecking       map[string]bool          // rechecks the user asked for that hold a slot
	recheckQueue     []string                 // rechecks waiting for a slot; index 0 goes next
	seedPend         map[string]time.Duration // seeding time counted but not yet written to the records
	activePend       map[string]time.Duration // running time counted but not yet written to the records
	lastEnforce      time.Time
	evMu             sync.Mutex
	onEvent          func(Event)

	cancel context.CancelFunc
	done   chan struct{}
}

// New starts the engine. stateDir holds state.json and the piece-completion database.
func New(cfg *config.Store, stateDir string) (*Manager, error) {
	st, err := loadState(filepath.Join(stateDir, "state.json"))
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	s := cfg.Get()
	if err := ValidateNetwork(s.Network); err != nil { // a hand-edited settings file must not stop the engine
		fmt.Fprintf(os.Stderr, "ignoring invalid network settings: %v\n", err)
		s.Network = config.Default(stateDir).Network
		if err := cfg.Update(func(x *config.Settings) { x.Network = s.Network }); err != nil {
			fmt.Fprintf(os.Stderr, "saving reset network settings: %v\n", err)
		}
	}
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return nil, err
	}
	comp, err := storage.NewBoltPieceCompletion(stateDir)
	if err != nil {
		return nil, fmt.Errorf("piece completion db: %w", err)
	}

	m := &Manager{
		cfg:        cfg,
		state:      st,
		stateDir:   stateDir,
		startNet:   s.Network,
		comp:       comp,
		upLim:      rate.NewLimiter(rate.Inf, 256<<10),
		downLim:    rate.NewLimiter(rate.Inf, 256<<10),
		torrents:   map[metainfo.Hash]*torrent.Torrent{},
		seenDown:   map[metainfo.Hash]int64{},
		seenUp:     map[metainfo.Hash]int64{},
		perm:       map[metainfo.Hash]permState{},
		queued:     map[metainfo.Hash]int{},
		activeAt:   map[metainfo.Hash]time.Time{},
		lowOn:      map[metainfo.Hash]bool{},
		peerRates:  map[string]*peerRate{},
		peerSeen:   map[string]time.Time{},
		moves:      map[string]*moveJob{},
		checking:   map[string]bool{},
		errs:       map[string]torrentError{},
		watchSeen:  map[string]watchStamp{},
		creates:    map[string]*CreateJob{},
		staged:     map[string]*stagedEntry{},
		phase:      map[string]*checkPhase{},
		checkProg:  map[string]float64{},
		seedPend:   map[string]time.Duration{},
		activePend: map[string]time.Duration{},
		held:       map[string]*heldInfo{},
		gateActive: map[string]bool{},
		rechecking: map[string]bool{},
		done:       make(chan struct{}),
		inbound:    &inboundTracker{},
	}
	m.applyLimits()

	m.store = store.NewResolver(func(h metainfo.Hash) string { return m.saveDir(h.HexString()) }, comp)
	newConfig := func(port int) *torrent.ClientConfig {
		cc := torrent.NewDefaultClientConfig()
		cc.ListenPort = port
		// What other clients see: our own name instead of the engine's module path.
		cc.ExtendedHandshakeClientVersion = buildinfo.ClientVersion()
		cc.Bep20 = buildinfo.PeerIDPrefix()
		cc.HTTPUserAgent = buildinfo.Name + "/" + buildinfo.Version
		cc.UpnpID = buildinfo.ClientVersion()
		cc.Seed = true
		cc.NoDefaultPortForwarding = true // portmap does it, with renewal and verification
		cc.Callbacks.CompletedHandshake = m.inbound.onHandshake
		cc.Callbacks.ReadExtendedHandshake = func(_ *torrent.PeerConn, msg *pp.ExtendedHandshakeMessage) { m.self.note(net.IP(msg.YourIp)) }
		applyNetwork(cc, s.Network)
		cc.DefaultStorage = m.store
		cc.UploadRateLimiter = m.upLim
		cc.DownloadRateLimiter = m.downLim
		return cc
	}
	// The wanted port may be taken, or fall into a range Windows keeps for itself (Hyper-V,
	// WSL); a random port may hit such a range too. Do not refuse to start over that.
	m.wantedPort = s.ListenPort
	ports := []int{s.ListenPort, 0, 0, 0}
	if s.ListenPort != 0 && !portFree(s.ListenPort) {
		// The engine would happily share a port that another program holds (Windows lets
		// listeners overlap), so look for a conflict ourselves.
		fmt.Fprintf(os.Stderr, "port %d is in use or not allowed, choosing another one\n", s.ListenPort)
		ports = []int{0, 0, 0, 0}
	}
	for _, p := range ports {
		m.cl, err = torrent.NewClient(newConfig(p))
		if err == nil {
			break
		}
		fmt.Fprintf(os.Stderr, "cannot listen on port %d: %v\n", p, err)
	}
	if err != nil {
		comp.Close()
		return nil, fmt.Errorf("start engine: %w", err)
	}

	if s.PortMapping {
		m.ports = portmap.New(m.cl.LocalPort(), portmap.Options{})
		m.ports.Start()
	}

	// A move interrupted by a crash or a forced kill must be settled before the torrents are
	// restored: until then a record may still point at a folder whose tree is half-copied.
	m.recoverMoves()

	// Restore torrents from the previous run.
	var recs []*record
	st.view(func(s *state) {
		for _, r := range s.Torrents {
			c := *r
			recs = append(recs, &c)
		}
	})
	// In queue order, so that the torrents that take the first slots for their check are the ones at the top.
	sort.SliceStable(recs, func(i, j int) bool { return orderLess(recs[i], recs[j]) })
	for _, r := range recs {
		if err := m.restore(r); err != nil {
			fmt.Fprintf(os.Stderr, "restore %s: %v\n", r.InfoHash, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.loop(ctx)
	return m, nil
}

// Close flushes counters and stops the engine.
func (m *Manager) Close() {
	m.cancel()
	<-m.done
	m.syncCounters()
	if err := m.state.flush(); err != nil {
		fmt.Fprintf(os.Stderr, "saving state on shutdown: %v\n", err)
	}
	m.portMu.Lock()
	if m.ports != nil {
		m.ports.Stop()
		m.ports = nil
	}
	m.portMu.Unlock()
	m.cl.Close()
	m.comp.Close()
}

// PortStatus reports the router port-forwarding state.
func (m *Manager) PortStatus() portmap.Status {
	m.portMu.Lock()
	defer m.portMu.Unlock()
	if m.ports == nil {
		return portmap.Status{Port: m.cl.LocalPort()}
	}
	return m.ports.Status()
}

// RefreshPort forces an immediate port-mapping check.
func (m *Manager) RefreshPort() {
	m.portMu.Lock()
	defer m.portMu.Unlock()
	if m.ports != nil {
		m.ports.Refresh()
	}
}

// Port returns the port the engine actually listens on.
func (m *Manager) Port() int { return m.cl.LocalPort() }

// ---------------------------------------------------------------- adding

// AddOption tweaks how a torrent is added.
type AddOption func(*record)

// WithPaused adds the torrent stopped, so files can be chosen before anything downloads.
func WithPaused() AddOption { return func(r *record) { r.Paused = true } }

// WithSavePath saves the torrent in the given folder instead of the default one.
func WithSavePath(dir string) AddOption { return func(r *record) { r.SavePath = dir } }

// WithLabel puts the torrent in a category. If the label has a folder configured, that is
// where the torrent is saved (unless WithSavePath says otherwise).
func WithLabel(label string) AddOption { return func(r *record) { r.Label = label } }

// AddFile adds a .torrent file: the file is copied into the configured copy folder.
func (m *Manager) AddFile(path string, opts ...AddOption) (string, error) {
	mi, err := metainfo.LoadFromFile(path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return m.AddMetaInfo(mi, opts...)
}

// AddMetaInfo adds torrent metadata and saves its .torrent copy.
func (m *Manager) AddMetaInfo(mi *metainfo.MetaInfo, opts ...AddOption) (string, error) {
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	hash := spec.InfoHash.HexString()
	if m.exists(hash) {
		return hash, nil
	}

	if err := m.saveMeta(mi, hash); err != nil {
		return "", err
	}
	copyPath, err := m.saveCopy(mi, hash)
	if err != nil {
		_ = os.Remove(m.metaPath(hash))
		return "", err
	}
	rec := &record{InfoHash: hash, CopyPath: copyPath, Added: time.Now(), Order: newOrder()}
	for _, o := range opts {
		o(rec)
	}
	m.applyAddPaused(rec)
	if err := m.resolveSaveDir(rec); err != nil {
		if copyPath != "" {
			_ = os.Remove(copyPath)
		}
		_ = os.Remove(m.metaPath(hash))
		return "", err
	}
	if rec.SkipCheck {
		m.trustExisting(hash)
		rec.SkipCheck = false
	}
	if err := m.track(spec, rec); err != nil {
		// Do not leave orphan copies behind when the add itself fails.
		if copyPath != "" {
			_ = os.Remove(copyPath)
		}
		_ = os.Remove(m.metaPath(hash))
		return "", err
	}
	return hash, nil
}

// AddMagnet adds a magnet link. Its .torrent copy is written once metadata arrives.
func (m *Manager) AddMagnet(uri string, opts ...AddOption) (string, error) {
	spec, err := torrent.TorrentSpecFromMagnetUri(uri)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	hash := spec.InfoHash.HexString()
	if m.exists(hash) {
		return hash, nil
	}
	rec := &record{InfoHash: hash, Magnet: uri, Added: time.Now(), Order: newOrder()}
	for _, o := range opts {
		o(rec)
	}
	m.applyAddPaused(rec)
	if err := m.resolveSaveDir(rec); err != nil {
		return "", err
	}
	if rec.SkipCheck {
		m.trustExisting(hash) // applies when the metadata arrives and the files are opened
		rec.SkipCheck = false
	}
	if err := m.track(spec, rec); err != nil {
		return "", err
	}
	return hash, nil
}

func (m *Manager) exists(hash string) bool {
	var ok bool
	m.state.view(func(s *state) { _, ok = s.Torrents[hash] })
	return ok
}

func (m *Manager) restore(r *record) error {
	// The internal metadata copy comes first (it always exists for torrents added from a
	// .torrent), then the user-visible copy, then the magnet link.
	var spec *torrent.TorrentSpec
	for _, p := range []string{m.metaPath(r.InfoHash), r.CopyPath} {
		if p == "" || spec != nil {
			continue
		}
		if mi, err := metainfo.LoadFromFile(p); err == nil {
			if sp, err := torrent.TorrentSpecFromMetaInfoErr(mi); err == nil {
				spec = sp
			}
		}
	}
	if spec == nil {
		if r.Magnet == "" {
			return fmt.Errorf("no metadata to restore %s from", r.InfoHash)
		}
		var err error
		if spec, err = torrent.TorrentSpecFromMagnetUri(r.Magnet); err != nil {
			return err
		}
	}
	t, _, err := m.cl.AddTorrentSpec(m.holdBack(spec))
	if err != nil {
		m.dropHeld(r.InfoHash)
		return err
	}
	m.register(t)
	m.watchWrites(t, r.InfoHash)
	m.applyConnLimit(t, r.InfoHash, *r)
	m.reapplyTrackers(t, *r)
	if r.Paused {
		m.applyPause(t, r.InfoHash, true)
	}
	go m.onMetadata(t, r.InfoHash)
	return nil
}

// track registers a new torrent in the engine and persists its record.
func (m *Manager) track(spec *torrent.TorrentSpec, rec *record) error {
	// The record goes first: the storage asks for the torrent's folder as soon as the
	// engine knows the file list, which can happen inside AddTorrentSpec.
	if err := m.state.with(func(s *state) { s.Torrents[rec.InfoHash] = rec }); err != nil {
		return err
	}
	t, _, err := m.cl.AddTorrentSpec(m.holdBack(spec))
	if err != nil {
		m.dropHeld(rec.InfoHash)
		if serr := m.state.with(func(s *state) { delete(s.Torrents, rec.InfoHash) }); serr != nil {
			fmt.Fprintf(os.Stderr, "removing failed torrent %s from state: %v\n", rec.InfoHash, serr)
		}
		return err
	}
	m.register(t)
	m.watchWrites(t, rec.InfoHash)
	m.applyConnLimit(t, rec.InfoHash, *rec)
	if rec.Paused {
		m.applyPause(t, rec.InfoHash, true)
	}
	go m.onMetadata(t, rec.InfoHash)
	return nil
}

func (m *Manager) register(t *torrent.Torrent) {
	m.mu.Lock()
	m.torrents[t.InfoHash()] = t
	m.mu.Unlock()
}

// onMetadata runs once the torrent's info is known: it saves the .torrent copy for
// magnets, preallocates disk space and only then starts the download.
func (m *Manager) onMetadata(t *torrent.Torrent, hash string) {
	select {
	case <-t.GotInfo():
	case <-m.done:
		return
	}

	m.startCheckPhase(hash) // the engine now hashes whatever is already on disk
	var needCopy bool
	var paused bool
	m.state.view(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			needCopy = r.CopyPath == ""
			paused = r.Paused
		}
	})
	if !m.hasMeta(hash) {
		mi := t.Metainfo()
		_ = m.saveMeta(&mi, hash)
	}
	if needCopy {
		mi := t.Metainfo()
		if p, err := m.saveCopy(&mi, hash); err == nil && p != "" {
			if err := m.state.with(func(s *state) {
				if r := s.Torrents[hash]; r != nil {
					r.CopyPath = p
				}
			}); err != nil {
				fmt.Fprintf(os.Stderr, "saving .torrent copy path for %s: %v\n", hash, err)
			}
		}
	}

	if !paused {
		m.begin(t, hash)
	}
}

// begin reserves disk space for the wanted files and starts downloading them. Torrents
// added paused reserve nothing until they are resumed, so skipped files never cost space.
func (m *Manager) begin(t *torrent.Torrent, hash string) {
	if removed(t) {
		return
	}
	if err := m.preallocate(t, m.filePrios(hash)); err != nil {
		fmt.Fprintf(os.Stderr, "preallocate %s: %v\n", t.Name(), err)
		kind := ErrKindOther
		if errors.Is(err, ErrNoDiskSpace) {
			kind = ErrKindDiskFull
		}
		m.setError(hash, kind, err.Error())
		m.setPaused(t, hash, true) // do not download into a disk that cannot hold it
		return
	}
	m.clearError(hash)
	m.applyFiles(t, hash)
}

// saveCopy writes the .torrent copy to the configured folder and returns its path.
func (m *Manager) saveCopy(mi *metainfo.MetaInfo, hash string) (string, error) {
	dir := m.cfg.Get().TorrentCopyDir
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, copyFileName(mi, dir, hash))
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// copyFileName picks the file name for a saved .torrent copy: the torrent's own name, so the
// folder stays browsable, falling back to its hash when that name is empty or already taken
// by a different torrent's copy in the same folder (two different torrents can share a display
// name; re-adding the same torrent under its own name is fine, since it is its own copy).
func copyFileName(mi *metainfo.MetaInfo, dir, hash string) string {
	if info, err := mi.UnmarshalInfo(); err == nil {
		if name := sanitizeFileName(info.Name); name != "" {
			p := filepath.Join(dir, name+".torrent")
			if existing, err := metainfo.LoadFromFile(p); err != nil || existing.HashInfoBytes().HexString() == hash {
				return name + ".torrent"
			}
		}
	}
	return hash + ".torrent"
}

// sanitizeFileName turns a torrent's display name into a name safe to use as a file name on
// Windows: no reserved characters, no trailing dot or space, and not unreasonably long.
func sanitizeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20:
			return -1
		case strings.ContainsRune(`<>:"/\|?*`, r):
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, " .")
	if len(name) > 150 {
		name = name[:150]
	}
	return name
}

// ---------------------------------------------------------------- removing

// Remove drops a torrent. With deleteData the downloaded files and the saved .torrent
// copy are deleted too (subject to the copy-removal policy).
func (m *Manager) Remove(hash string, deleteData bool) error {
	h, valid := parseHash(hash)
	if !valid {
		return ErrNotFound
	}
	m.mu.Lock()
	t, ok := m.torrents[h]
	busy := m.moves[hash] != nil && m.moves[hash].running
	m.mu.Unlock()
	if busy {
		return ErrBusy
	}
	if !ok {
		return ErrNotFound
	}

	// Collect everything we need before the engine forgets the torrent.
	var dataPaths []string
	if deleteData && t.Info() != nil {
		dataPaths = m.dataPaths(t)
	}
	down, up := m.finalCounters(t, hash)

	t.Drop()
	m.dropHeld(hash)
	m.mu.Lock()
	delete(m.torrents, h)
	delete(m.seenDown, h)
	delete(m.seenUp, h)
	delete(m.perm, h)
	delete(m.queued, h)
	delete(m.activeAt, h) // flushActiveTime and List walk these maps on every refresh: what stays only grows
	delete(m.moves, hash)
	m.recheckQueue = dropString(m.recheckQueue, hash)
	m.mu.Unlock()
	m.forgetSeedTime(hash) // the uncounted seeding and running time (seedPend, activePend)
	m.lowMu.Lock()
	delete(m.lowOn, h)
	m.lowMu.Unlock()

	var copyPath string
	var errs []error
	if err := m.state.with(func(s *state) {
		if r := s.Torrents[hash]; r != nil {
			copyPath = r.CopyPath
		}
		delete(s.Torrents, hash)
		s.RemovedDownloaded += down
		s.RemovedUploaded += up
	}); err != nil {
		errs = append(errs, fmt.Errorf("saving removal to state: %w", err))
	}

	if deleteData {
		for _, p := range dataPaths {
			if err := removeAll(p); err != nil {
				errs = append(errs, err)
			}
		}
	}
	_ = os.Remove(m.metaPath(hash)) // the internal copy has no use once the torrent is gone
	m.clearError(hash)
	m.forgetChecks(hash)
	policy := m.cfg.Get().CopyRemovePolicy
	if copyPath != "" && (policy == config.CopyRemoveAlways ||
		(policy == config.CopyRemoveWithData && deleteData)) {
		if err := os.Remove(copyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// dataPaths returns the on-disk root of the torrent's data, refusing anything that is
// not strictly inside the download directory.
func (m *Manager) dataPaths(t *torrent.Torrent) []string {
	base, err := filepath.Abs(m.saveDir(t.InfoHash().HexString()))
	if err != nil {
		return nil
	}
	root, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(t.Info().BestName())))
	if err != nil || !strings.HasPrefix(root, base+string(filepath.Separator)) {
		return nil
	}
	return []string{root}
}

// removeAll deletes path, retrying briefly: on Windows the engine may still hold the
// file open for a moment after the torrent was dropped.
func removeAll(path string) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return err
}

// ---------------------------------------------------------------- preallocation

// preallocate reserves the full size of every wanted file. It fails early, before any
// data is downloaded, if the disk cannot hold the torrent.
func (m *Manager) preallocate(t *torrent.Torrent, prios []int8) error {
	s := m.cfg.Get()
	if !m.preallocEnabled(t.InfoHash().HexString()) {
		return nil
	}
	base, err := filepath.Abs(m.saveDir(t.InfoHash().HexString()))
	if err != nil {
		return err
	}

	type job struct {
		path string
		size int64
	}
	var jobs []job
	var need int64
	paths, err := store.Paths(base, t.Info())
	if err != nil {
		return err
	}
	for i, f := range t.Files() {
		if prioAt(prios, i) == PrioSkip {
			continue
		}
		var have int64
		if fi, err := os.Stat(paths[i]); err == nil {
			have = fi.Size()
		}
		if have < f.Length() {
			need += f.Length() - have
			jobs = append(jobs, job{paths[i], f.Length()})
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	if free, err := diskFree(base); err == nil && uint64(need) > free {
		return fmt.Errorf("%w: need %d bytes, %d free", ErrNoDiskSpace, need, free)
	}
	for _, j := range jobs {
		// A torrent removed meanwhile (with its data) must not get its files back.
		if removed(t) {
			return nil
		}
		if err := allocateFile(j.path, j.size, s.PreallocateZeroFill); err != nil {
			return err
		}
	}
	return nil
}

// removed reports whether the torrent has been dropped from the engine.
func removed(t *torrent.Torrent) bool {
	select {
	case <-t.Closed():
		return true
	default:
		return false
	}
}

// allocateFile grows path to size bytes without touching existing content.
func allocateFile(path string, size int64, zeroFill bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	old := fi.Size()
	if old >= size {
		return nil
	}
	if !zeroFill {
		markSparse(f) // see markSparse: without it the first far-off write zero-fills everything before it
	}
	if err := f.Truncate(size); err != nil { // sets the length (SetEndOfFile on NTFS)
		return err
	}
	if !zeroFill {
		return nil
	}
	if _, err := f.Seek(old, 0); err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for left := size - old; left > 0; {
		n := int64(len(buf))
		if left < n {
			n = left
		}
		if _, err := f.Write(buf[:n]); err != nil {
			return err
		}
		left -= n
	}
	return nil
}

// retryRemove runs a removal, retrying briefly: on Windows a file the engine just closed may stay busy for a moment.
func retryRemove(remove func() error) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = remove(); err == nil || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return err
}
