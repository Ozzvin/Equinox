package api

import (
	"net/http"

	"github.com/Ozzvin/equinox/internal/core"
)

// Desktop is what the page asks of the desktop program it is the window of, when that window is the user's own
// browser (Linux). A program with a window of its own (Windows) binds these to the page itself instead.
type Desktop interface {
	AutostartEnabled() bool
	SetAutostart(on, hidden bool) error
	// MakeDefault makes the program the one that opens .torrent files and magnet links.
	MakeDefault() error
}

// SetBrowserApp says the page is the window of the desktop program, shown in the user's own browser (Linux): it then
// takes .torrent files and magnet links opened from outside, as the program's own window does on Windows, and has
// the system section of the settings, answered by d.
func (s *Server) SetBrowserApp(d Desktop) { s.desktop = d }

func (s *Server) desktopRoutes() {
	s.mux.HandleFunc("GET /api/desktop/autostart", s.getAutostart)
	s.mux.HandleFunc("PUT /api/desktop/autostart", s.putAutostart)
	s.mux.HandleFunc("POST /api/desktop/default-app", s.makeDefault)
}

func (s *Server) getAutostart(w http.ResponseWriter, r *http.Request) {
	if s.desktop == nil {
		fail(w, core.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": s.desktop.AutostartEnabled()})
}

// putAutostart turns starting at login on or off; it starts hidden in the tray as the settings say.
func (s *Server) putAutostart(w http.ResponseWriter, r *http.Request) {
	if s.desktop == nil {
		fail(w, core.ErrNotFound)
		return
	}
	var b struct {
		On bool `json:"on"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.desktop.SetAutostart(b.On, s.cfg.Get().StartHidden); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": b.On})
}

func (s *Server) makeDefault(w http.ResponseWriter, r *http.Request) {
	if s.desktop == nil {
		fail(w, core.ErrNotFound)
		return
	}
	if err := s.desktop.MakeDefault(); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
