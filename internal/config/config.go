// Package config holds the daemon settings and their JSON persistence.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// CopyRemovePolicy decides when the saved .torrent copy is deleted.
type CopyRemovePolicy string

const (
	// CopyRemoveWithData deletes the copy only when the torrent is removed together with its data.
	CopyRemoveWithData CopyRemovePolicy = "with_data"
	// CopyRemoveAlways deletes the copy on any removal.
	CopyRemoveAlways CopyRemovePolicy = "always"
	// CopyRemoveNever keeps the copy forever.
	CopyRemoveNever CopyRemovePolicy = "never"
)

// AltSchedule switches the turtle mode on and off by the clock. The window runs from
// From to To (HH:MM, may cross midnight) on the listed weekdays (0 = Sunday; empty = every
// day). For a window crossing midnight the day is the one it starts on.
type AltSchedule struct {
	Enabled bool   `json:"enabled"`
	From    string `json:"from"`
	To      string `json:"to"`
	Days    []int  `json:"days"`
}

// Encryption policies for peer connections.
const (
	EncryptionPrefer  = "prefer"  // use it when the peer supports it (default)
	EncryptionRequire = "require" // refuse peers that do not encrypt
	EncryptionOff     = "off"     // do not ask for it
)

// Network holds the connection settings. MaxConnsPerTorrent applies at once; the other
// fields are read when the engine starts, so changing them needs a restart.
type Network struct {
	MaxConnsPerTorrent    int    `json:"maxConnsPerTorrent"`    // established peers per torrent
	MaxHalfOpenPerTorrent int    `json:"maxHalfOpenPerTorrent"` // simultaneous connection attempts per torrent
	DHT                   bool   `json:"dht"`
	PEX                   bool   `json:"pex"`
	UTP                   bool   `json:"utp"`
	TCP                   bool   `json:"tcp"`
	IPv6                  bool   `json:"ipv6"`
	Webseeds              bool   `json:"webseeds"`
	AcceptIncoming        bool   `json:"acceptIncoming"`
	Encryption            string `json:"encryption"`
}

// AddDefaults are the initial choices of the "Add torrents" dialog.
type AddDefaults struct {
	Paused          bool   `json:"paused"`
	Sequential      bool   `json:"sequential"`
	EdgePieces      bool   `json:"edgePieces"` // prioritise the first and last pieces of files
	SkipCheck       bool   `json:"skipCheck"`  // do not hash files that are already on disk
	MoveDoneEnabled bool   `json:"moveDoneEnabled"`
	MoveDone        string `json:"moveDone"`
}

// Settings is everything the user can tune. Speeds are in KiB/s, 0 means unlimited.
type Settings struct {
	// DataDir is where new torrents are saved unless they are given another folder.
	DataDir string `json:"dataDir"`
	// MoveCompletedDir, if set, is where a torrent is moved once its download has finished
	// (download in one place, keep the finished files in another).
	MoveCompletedDir string `json:"moveCompletedDir"`
	// WatchDir is scanned for new .torrent files, which are added automatically (empty = off).
	WatchDir string `json:"watchDir"`
	// Add holds what the "Add torrents" dialog starts with.
	Add AddDefaults `json:"add"`
	// LabelPaths maps a label to the folder for new torrents added with that label.
	LabelPaths map[string]string `json:"labelPaths,omitempty"`
	// LabelColors maps a label to the colour of its dot (an id from LabelPalette).
	LabelColors map[string]string `json:"labelColors,omitempty"`

	// Copy of every added .torrent file is stored here (empty disables the feature).
	TorrentCopyDir   string           `json:"torrentCopyDir"`
	CopyRemovePolicy CopyRemovePolicy `json:"copyRemovePolicy"`

	ListenPort int     `json:"listenPort"`
	Network    Network `json:"network"`
	// PortMapping keeps the port forwarded on the router (UPnP / NAT-PMP) and renews it.
	PortMapping bool `json:"portMapping"`

	// Preallocate reserves the full file size on disk when a torrent is added.
	Preallocate bool `json:"preallocate"`
	// PreallocateZeroFill additionally writes zeros so the space is physically allocated.
	PreallocateZeroFill bool `json:"preallocateZeroFill"`

	DownLimitKBps int `json:"downLimitKBps"`
	UpLimitKBps   int `json:"upLimitKBps"`

	// "Turtle" mode: alternative limits toggled with one click.
	AltDownLimitKBps int         `json:"altDownLimitKBps"`
	AltUpLimitKBps   int         `json:"altUpLimitKBps"`
	AltSpeedActive   bool        `json:"altSpeedActive"`
	AltSchedule      AltSchedule `json:"altSchedule"`

	// MaxActiveDownloads limits simultaneous downloads; the rest wait in the queue (0 = no limit).
	MaxActiveDownloads int `json:"maxActiveDownloads"`

	// Default stop-seeding ratio, 0 disables. A torrent may override it.
	RatioLimit float64 `json:"ratioLimit"`

	// Default stop-seeding time in minutes, 0 disables. A torrent may override it.
	SeedTimeLimitMinutes int `json:"seedTimeLimitMinutes"`

	// MaxConcurrentChecks limits how many torrents have their local files checked at once; the rest wait
	// in a line (0 = no limit).
	MaxConcurrentChecks int `json:"maxConcurrentChecks"`

	// SpeedUnit is how speeds are shown: "bytes" (КБ/с, МБ/с) or "bits" (Кбит/с, Мбит/с).
	SpeedUnit string `json:"speedUnit"`

	// NotifyOnComplete makes the desktop application show a Windows notification when a download finishes.
	NotifyOnComplete bool `json:"notifyOnComplete"`

	// Desktop application behaviour. StartHidden: the autostart entry opens the app in the tray only.
	// CloseToTray: closing the window keeps the app running in the tray (otherwise it exits).
	// MinimizeToTray: minimising the window sends it to the tray.
	StartHidden    bool `json:"startHidden"`
	CloseToTray    bool `json:"closeToTray"`
	MinimizeToTray bool `json:"minimizeToTray"`
	// RememberWindow: the window opens at the size and place it had when it was closed (off: the default size
	// of the interface density, in the middle of the screen).
	RememberWindow bool `json:"rememberWindow"`

	// SetupDone is set once the first-run setup guide was finished or skipped.
	SetupDone bool `json:"setupDone"`

	// Streaming: how many pieces at each end of a file get top priority.
	EdgePieces int `json:"edgePieces"`
	// Streaming: size of the sliding window (in pieces) for sequential mode.
	SequentialWindow int `json:"sequentialWindow"`
}

// Default returns settings with sane values; dir is the daemon's state directory.
func Default(dir string) Settings {
	return Settings{
		DataDir:             filepath.Join(dir, "downloads"),
		TorrentCopyDir:      filepath.Join(dir, "torrents"),
		CopyRemovePolicy:    CopyRemoveWithData,
		ListenPort:          51413,
		NotifyOnComplete:    true,
		StartHidden:         true,
		CloseToTray:         true,
		SpeedUnit:           "bytes",
		MaxConcurrentChecks: 2,
		Network: Network{
			MaxConnsPerTorrent: 50, MaxHalfOpenPerTorrent: 25,
			DHT: true, PEX: true, UTP: true, TCP: true, IPv6: true, Webseeds: true, AcceptIncoming: true,
			Encryption: EncryptionPrefer,
		},
		PortMapping:      true,
		Preallocate:      true,
		AltDownLimitKBps: 50,
		AltUpLimitKBps:   50,
		AltSchedule:      AltSchedule{From: "23:00", To: "07:00"},
		Add:              AddDefaults{EdgePieces: true},
		EdgePieces:       4,
		SequentialWindow: 16,
	}
}

// Store is a concurrency-safe settings holder backed by a JSON file.
type Store struct {
	mu   sync.RWMutex
	path string
	s    Settings
}

// Load reads path, creating it with defaults when missing.
func Load(path, stateDir string) (*Store, error) {
	st := &Store{path: path, s: Default(stateDir)}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st, st.save()
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return nil, err
	}
	return st, nil
}

// Get returns a copy of the current settings.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

// Update applies fn to the settings and persists the result.
func (st *Store) Update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	return st.save()
}

func (st *Store) save() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// LabelPalette lists the colours a label can have. They are chosen so that none of them looks like
// a state colour of the interface: green (downloading), blue (seeding), yellow (checking), grey
// (stopped) and red (error).
var LabelPalette = []string{"violet", "purple", "pink", "cyan", "brown"}

// ValidLabelColor tells whether id is one of LabelPalette.
func ValidLabelColor(id string) bool {
	for _, c := range LabelPalette {
		if c == id {
			return true
		}
	}
	return false
}
