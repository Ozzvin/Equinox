// Package api exposes the core over HTTP/JSON. Every request needs the API token, so a
// web page opened in the user's browser cannot drive the client through localhost.
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/config"
	"github.com/Ozzvin/equinox/internal/core"
)

const maxTorrentFile = 8 << 20

// Server is the HTTP handler.
type Server struct {
	m     *core.Manager
	cfg   *config.Store
	token string
	ui    http.Handler
	mux   *http.ServeMux
}

// LoadToken returns the persistent API token stored at path, creating it if needed.
func LoadToken(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return tok, os.WriteFile(path, []byte(tok), 0o600)
}

// New builds the handler. ui serves the web interface for non-API paths (may be nil).
func New(m *core.Manager, cfg *config.Store, token string, ui http.Handler) *Server {
	s := &Server{m: m, cfg: cfg, token: token, ui: ui, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Reject DNS-rebinding: only loopback host names are accepted.
	if !loopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	// The UI files hold no secrets; the page itself supplies the token to /api calls.
	if s.ui != nil && !strings.HasPrefix(r.URL.Path, "/api/") {
		s.ui.ServeHTTP(w, r)
		return
	}
	if !s.authorised(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func loopbackHost(h string) bool {
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.HasSuffix(h, "]") {
		h = h[:i]
	}
	h = strings.Trim(h, "[]")
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

// authorised accepts "Authorization: Bearer <token>"; players that cannot set headers
// may use ?token= on the stream URL.
func (s *Server) authorised(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" && strings.HasSuffix(r.URL.Path, "/stream") {
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/torrents", s.list)
	s.mux.HandleFunc("POST /api/torrents", s.add)
	s.mux.HandleFunc("DELETE /api/torrents/{hash}", s.remove)
	s.mux.HandleFunc("POST /api/torrents/{hash}/pause", s.pause(true))
	s.mux.HandleFunc("POST /api/torrents/{hash}/resume", s.pause(false))
	s.mux.HandleFunc("POST /api/torrents/{hash}/sequential", s.sequential)
	s.mux.HandleFunc("POST /api/torrents/{hash}/ratio-limit", s.ratioLimit)
	s.mux.HandleFunc("POST /api/torrents/{hash}/seed-time-limit", s.seedTimeLimit)
	s.mux.HandleFunc("POST /api/torrents/{hash}/queue", s.queueMove)
	s.mux.HandleFunc("POST /api/torrents/{hash}/label", s.label)
	s.mux.HandleFunc("POST /api/torrents/{hash}/move", s.moveStorage)
	s.mux.HandleFunc("GET /api/torrents/{hash}/details", s.details)
	s.mux.HandleFunc("GET /api/torrents/{hash}/peers", s.peers)
	s.mux.HandleFunc("POST /api/torrents/{hash}/trackers", s.addTracker)
	s.mux.HandleFunc("POST /api/torrents/{hash}/recheck", s.recheck)
	s.mux.HandleFunc("POST /api/torrents/{hash}/max-connections", s.maxConns)
	s.mux.HandleFunc("GET /api/restart-required", s.restartRequired)
	s.mux.HandleFunc("GET /api/torrents/{hash}/files", s.files)
	s.mux.HandleFunc("POST /api/torrents/{hash}/files/priority", s.filePriority)
	s.mux.HandleFunc("GET /api/torrents/{hash}/files/{idx}/stream", s.stream)
	s.mux.HandleFunc("POST /api/torrents/{hash}/open-folder", s.openFolder)
	s.mux.HandleFunc("POST /api/torrents/{hash}/files/{idx}/open", s.openFile)
	s.mux.HandleFunc("POST /api/torrents/{hash}/files/delete", s.deleteFiles)
	s.mux.HandleFunc("GET /api/fs", s.fsList)
	s.mux.HandleFunc("POST /api/fs/mkdir", s.fsMkdir)
	s.mux.HandleFunc("POST /api/stage", s.stage)
	s.mux.HandleFunc("DELETE /api/stage/{id}", s.unstage)
	s.mux.HandleFunc("POST /api/add-batch", s.addBatch)
	s.mux.HandleFunc("GET /api/add-dialog", s.addDialog)
	s.mux.HandleFunc("PUT /api/add-defaults", s.putAddDefaults)
	s.mux.HandleFunc("POST /api/create", s.createTorrent)
	s.mux.HandleFunc("GET /api/create/{id}", s.createStatus)
	s.mux.HandleFunc("GET /api/settings", s.getSettings)
	s.mux.HandleFunc("PUT /api/settings", s.putSettings)
	s.mux.HandleFunc("POST /api/altspeed", s.altSpeed)
	s.mux.HandleFunc("GET /api/port", s.port)
	s.mux.HandleFunc("POST /api/port/refresh", s.portRefresh)
	s.mux.HandleFunc("POST /api/port/mapping", s.portMapping)
	s.mux.HandleFunc("GET /api/stats", s.stats)
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, core.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, core.ErrInvalidInput):
		code = http.StatusBadRequest
	case errors.Is(err, core.ErrNoDiskSpace):
		code = http.StatusInsufficientStorage
	case errors.Is(err, core.ErrNoMetadata), errors.Is(err, core.ErrBusy):
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// ---------------------------------------------------------------- torrents

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.m.List())
}

// add accepts either multipart form field "file" (.torrent) or JSON {"magnet": "..."}.
// With ?paused=1 the torrent is added stopped, so files can be chosen before it starts;
// ?save_path=<folder> picks the download folder and ?label=<name> sets the category (whose
// configured folder is used when no save_path is given).
func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	var hash string
	var err error
	var opts []core.AddOption
	q := r.URL.Query()
	if q.Get("paused") == "1" {
		opts = append(opts, core.WithPaused())
	}
	if p := q.Get("save_path"); p != "" {
		opts = append(opts, core.WithSavePath(p))
	}
	if l := q.Get("label"); l != "" {
		opts = append(opts, core.WithLabel(l))
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, maxTorrentFile)
		f, _, ferr := r.FormFile("file")
		if ferr != nil {
			fail(w, errors.Join(core.ErrInvalidInput, ferr))
			return
		}
		defer f.Close()
		mi, merr := metainfo.Load(f)
		if merr != nil {
			fail(w, errors.Join(core.ErrInvalidInput, merr))
			return
		}
		hash, err = s.m.AddMetaInfo(mi, opts...)
	} else {
		var body struct {
			Magnet string `json:"magnet"`
		}
		if derr := decode(r, &body); derr != nil || !strings.HasPrefix(body.Magnet, "magnet:") {
			fail(w, core.ErrInvalidInput)
			return
		}
		hash, err = s.m.AddMagnet(body.Magnet, opts...)
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"hash": hash})
}

// remove deletes a torrent; ?data=1 also deletes files and the saved .torrent copy.
func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	withData := r.URL.Query().Get("data") == "1"
	if err := s.m.Remove(r.PathValue("hash"), withData); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pause(p bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.m.SetPaused(r.PathValue("hash"), p); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) sequential(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetSequential(r.PathValue("hash"), b.Enabled); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ratioLimit(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Limit float64 `json:"limit"`
	}
	if err := decode(r, &b); err != nil || b.Limit < 0 {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetRatioLimit(r.PathValue("hash"), b.Limit); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) seedTimeLimit(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Minutes int `json:"minutes"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetSeedTimeLimit(r.PathValue("hash"), b.Minutes); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) details(w http.ResponseWriter, r *http.Request) {
	d, err := s.m.Details(r.PathValue("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) peers(w http.ResponseWriter, r *http.Request) {
	p, err := s.m.Peers(r.PathValue("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// addTracker adds an announce URL: {"url": "udp://tracker.example:6969/announce"}.
func (s *Server) addTracker(w http.ResponseWriter, r *http.Request) {
	var b struct {
		URL string `json:"url"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.AddTracker(r.PathValue("hash"), b.URL); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxConns limits the peer connections of one torrent: {"limit": 20} (0 = global setting).
func (s *Server) maxConns(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Limit int `json:"limit"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetTorrentMaxConns(r.PathValue("hash"), b.Limit); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// restartRequired reports whether saved settings need a restart to take effect.
func (s *Server) restartRequired(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.m.RestartInfo())
}

// openFolder shows the torrent's files in the system file manager (this machine only).
func (s *Server) openFolder(w http.ResponseWriter, r *http.Request) {
	if err := s.m.OpenFolder(r.PathValue("hash")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// openFile shows one file of a torrent in the file manager.
func (s *Server) openFile(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.OpenFile(r.PathValue("hash"), idx); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteFiles takes files out of a torrent and deletes them from the disk: POST {"files": [0, 3]}.
func (s *Server) deleteFiles(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Files []int `json:"files"`
	}
	if err := decode(r, &b); err != nil || len(b.Files) == 0 {
		fail(w, core.ErrInvalidInput)
		return
	}
	n, err := s.m.DeleteFiles(r.PathValue("hash"), b.Files)
	if err != nil {
		if n > 0 { // some were deleted before the failure: say so
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "deleted": n})
			return
		}
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

// stage parses an uploaded .torrent for the "Add torrents" list: multipart field "file".
func (s *Server) stage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTorrentFile)
	f, _, err := r.FormFile("file")
	if err != nil {
		fail(w, errors.Join(core.ErrInvalidInput, err))
		return
	}
	defer f.Close()
	mi, err := metainfo.Load(f)
	if err != nil {
		fail(w, errors.Join(core.ErrInvalidInput, err))
		return
	}
	st, err := s.m.Stage(mi)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, st)
}

func (s *Server) unstage(w http.ResponseWriter, r *http.Request) {
	s.m.Unstage(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// addBatch adds a whole list, each entry with its own options:
// {"items": [{"stage": "id" | "magnet": "..." | "infohash": "...", "options": {...}}]}.
func (s *Server) addBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []core.BatchItem `json:"items"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	res, err := s.m.AddBatch(body.Items)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

// addDialog returns what the "Add torrents" dialog starts with.
func (s *Server) addDialog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.m.AddDialog())
}

func (s *Server) putAddDefaults(w http.ResponseWriter, r *http.Request) {
	var d config.AddDefaults
	if err := decode(r, &d); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetAddDefaults(d); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.m.AddDialog())
}

// createTorrent starts making a .torrent from local data (202 with the job); follow it with
// GET /api/create/{id}.
func (s *Server) createTorrent(w http.ResponseWriter, r *http.Request) {
	var req core.CreateRequest
	if err := decode(r, &req); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	job, err := s.m.StartCreate(req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) createStatus(w http.ResponseWriter, r *http.Request) {
	job, err := s.m.Create(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// recheck re-hashes the torrent's data in the background (202); see the "checking" field.
func (s *Server) recheck(w http.ResponseWriter, r *http.Request) {
	if err := s.m.Recheck(r.PathValue("hash")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// moveStorage moves a torrent's files to another folder: {"path": "D:\\Media"}. It answers
// 202 at once; the progress shows in the torrent's "moving" field.
func (s *Server) moveStorage(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Path string `json:"path"`
	}
	if err := decode(r, &b); err != nil || b.Path == "" {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.MoveStorage(r.PathValue("hash"), b.Path); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// label sets the category of a torrent: {"label": "Фильмы"} ("" clears it).
func (s *Server) label(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Label string `json:"label"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetLabel(r.PathValue("hash"), b.Label); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// queueMove changes the queue position: {"move": "top|up|down|bottom"}.
func (s *Server) queueMove(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Move string `json:"move"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.MoveInQueue(r.PathValue("hash"), b.Move); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	fs, err := s.m.Files(r.PathValue("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fs)
}

// filePriority sets the priority of files: {"files": [0, 2], "priority": "skip|normal|high"}.
func (s *Server) filePriority(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Files    []int  `json:"files"`
		Priority string `json:"priority"`
	}
	if err := decode(r, &b); err != nil || len(b.Files) == 0 {
		fail(w, core.ErrInvalidInput)
		return
	}
	prio, err := core.ParsePriority(b.Priority)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.m.SetFilePriorities(r.PathValue("hash"), b.Files, prio); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stream serves a file with Range support so players can seek during the download.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	st, err := s.m.OpenStream(r.PathValue("hash"), idx)
	if err != nil {
		fail(w, err)
		return
	}
	defer st.Close()
	http.ServeContent(w, r, st.Name, time.Time{}, st)
}

// ---------------------------------------------------------------- settings & status

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.Get())
}

// putSettings updates the limits, which take effect immediately. Paths and the listen
// port need a restart and are intentionally not changeable here yet.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var b struct {
		DownLimitKBps    int                 `json:"downLimitKBps"`
		UpLimitKBps      int                 `json:"upLimitKBps"`
		AltDownLimitKBps int                 `json:"altDownLimitKBps"`
		AltUpLimitKBps   int                 `json:"altUpLimitKBps"`
		RatioLimit       float64             `json:"ratioLimit"`
		SeedTimeLimit    *int                `json:"seedTimeLimitMinutes"` // optional: omitted = unchanged
		NotifyOnComplete *bool               `json:"notifyOnComplete"`     // optional
		StartHidden      *bool               `json:"startHidden"`          // optional
		SetupDone        *bool               `json:"setupDone"`            // optional
		CloseToTray      *bool               `json:"closeToTray"`          // optional
		MinimizeToTray   *bool               `json:"minimizeToTray"`       // optional
		MaxChecks        *int                `json:"maxConcurrentChecks"`  // optional
		AddPaused        *bool               `json:"addPaused"`            // optional
		SpeedUnit        *string             `json:"speedUnit"`            // optional: bytes | bits
		MaxActive        int                 `json:"maxActiveDownloads"`
		AltSchedule      *config.AltSchedule `json:"altSchedule"` // optional: omitted = unchanged
		// Folders and port: all optional, omitted = unchanged.
		DataDir          *string                 `json:"dataDir"`
		TorrentCopyDir   *string                 `json:"torrentCopyDir"`
		LabelPaths       *map[string]string      `json:"labelPaths"`
		LabelColors      *map[string]string      `json:"labelColors"` // optional: omitted = unchanged
		MoveCompleted    *string                 `json:"moveCompletedDir"`
		WatchDir         *string                 `json:"watchDir"`
		ListenPort       *int                    `json:"listenPort"`
		Network          *config.Network         `json:"network"`
		Preallocate      *bool                   `json:"preallocate"`
		CopyRemovePolicy config.CopyRemovePolicy `json:"copyRemovePolicy"`
	}
	if err := decode(r, &b); err != nil || b.DownLimitKBps < 0 || b.UpLimitKBps < 0 ||
		b.AltDownLimitKBps < 0 || b.AltUpLimitKBps < 0 || b.RatioLimit < 0 || b.MaxActive < 0 ||
		(b.SeedTimeLimit != nil && (*b.SeedTimeLimit < 0 || *b.SeedTimeLimit > 60*24*3650)) ||
		(b.MaxChecks != nil && (*b.MaxChecks < 0 || *b.MaxChecks > 64)) ||
		(b.SpeedUnit != nil && *b.SpeedUnit != "bytes" && *b.SpeedUnit != "bits") {
		fail(w, core.ErrInvalidInput)
		return
	}
	switch b.CopyRemovePolicy {
	case config.CopyRemoveWithData, config.CopyRemoveAlways, config.CopyRemoveNever:
	default:
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetLimits(b.DownLimitKBps, b.UpLimitKBps, b.AltDownLimitKBps, b.AltUpLimitKBps); err != nil {
		fail(w, err)
		return
	}
	if b.DataDir != nil || b.TorrentCopyDir != nil || b.LabelPaths != nil || b.MoveCompleted != nil || b.WatchDir != nil {
		cur := s.cfg.Get()
		u := core.FoldersUpdate{DataDir: cur.DataDir, TorrentCopyDir: cur.TorrentCopyDir, LabelPaths: cur.LabelPaths, MoveCompletedDir: cur.MoveCompletedDir}
		u.WatchDir = cur.WatchDir
		if b.WatchDir != nil {
			u.WatchDir = *b.WatchDir
		}
		if b.MoveCompleted != nil {
			u.MoveCompletedDir = *b.MoveCompleted
		}
		if b.DataDir != nil {
			u.DataDir = *b.DataDir
		}
		if b.TorrentCopyDir != nil {
			u.TorrentCopyDir = *b.TorrentCopyDir
		}
		if b.LabelPaths != nil {
			u.LabelPaths = *b.LabelPaths
		}
		if err := s.m.SetFolders(u); err != nil {
			fail(w, err)
			return
		}
	}
	if b.LabelColors != nil {
		colors := map[string]string{}
		for l, id := range *b.LabelColors {
			l = strings.TrimSpace(l)
			if l == "" || id == "" {
				continue
			}
			if !config.ValidLabelColor(id) {
				fail(w, badInput("Неизвестный цвет метки: "+id))
				return
			}
			colors[l] = id
		}
		_ = s.cfg.Update(func(c *config.Settings) { c.LabelColors = colors })
	}
	if b.ListenPort != nil {
		if err := s.m.SetListenPort(*b.ListenPort); err != nil {
			fail(w, err)
			return
		}
	}
	if b.Network != nil {
		if err := s.m.SetNetwork(*b.Network); err != nil {
			fail(w, err)
			return
		}
	}
	if b.Preallocate != nil {
		_ = s.cfg.Update(func(c *config.Settings) { c.Preallocate = *b.Preallocate })
	}
	if b.AltSchedule != nil {
		if err := s.m.SetAltSchedule(*b.AltSchedule); err != nil {
			fail(w, err)
			return
		}
	}
	if err := s.m.SetMaxActiveDownloads(b.MaxActive); err != nil {
		fail(w, err)
		return
	}
	if err := s.cfg.Update(func(c *config.Settings) {
		c.RatioLimit, c.CopyRemovePolicy = b.RatioLimit, b.CopyRemovePolicy
		if b.SeedTimeLimit != nil {
			c.SeedTimeLimitMinutes = *b.SeedTimeLimit
		}
		if b.NotifyOnComplete != nil {
			c.NotifyOnComplete = *b.NotifyOnComplete
		}
		if b.StartHidden != nil {
			c.StartHidden = *b.StartHidden
		}
		if b.SetupDone != nil {
			c.SetupDone = *b.SetupDone
		}
		if b.CloseToTray != nil {
			c.CloseToTray = *b.CloseToTray
		}
		if b.MinimizeToTray != nil {
			c.MinimizeToTray = *b.MinimizeToTray
		}
		if b.MaxChecks != nil {
			c.MaxConcurrentChecks = *b.MaxChecks
		}
		if b.AddPaused != nil {
			c.Add.Paused = *b.AddPaused
		}
		if b.SpeedUnit != nil {
			c.SpeedUnit = *b.SpeedUnit
		}
	}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.Get())
}

// altSpeed toggles the turtle mode: POST {"enabled": true|false}.
func (s *Server) altSpeed(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetAltSpeed(b.Enabled); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": b.Enabled})
}

func (s *Server) port(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.m.PortReport())
}

// portMapping switches automatic router forwarding: POST {"enabled": true|false}.
func (s *Server) portMapping(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetPortMapping(b.Enabled); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.m.PortReport())
}

func (s *Server) portRefresh(w http.ResponseWriter, r *http.Request) {
	s.m.RefreshPort()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	down, up, ratio := s.m.GlobalRatio()
	writeJSON(w, http.StatusOK, map[string]any{
		"downloaded": down, "uploaded": up, "ratio": ratio,
		"altSpeed": s.cfg.Get().AltSpeedActive,
	})
}
