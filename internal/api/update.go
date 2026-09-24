package api

import (
	"net/http"

	"github.com/Ozzvin/equinox/internal/update"
)

// UpdateStatus is what the settings page shows about a newer release.
type UpdateStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Notes     string `json:"notes,omitempty"`
	URL       string `json:"url,omitempty"` // the release's page, for a manual download
}

// updateCheck asks GitHub for the latest release. An answer is reused for as long as the period of the
// automatic check, so that reloading the page does not ask again; ?force=1 bypasses that, for the "check
// now" button and for the timer of the periodic check.
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	info, err := update.CheckWithin(r.Context(), r.URL.Query().Get("force") == "1", s.cfg.Get().UpdateCheckEvery())
	if err != nil {
		fail(w, err)
		return
	}
	if info == nil {
		writeJSON(w, http.StatusOK, UpdateStatus{})
		return
	}
	writeJSON(w, http.StatusOK, UpdateStatus{Available: true, Version: info.Version, Notes: info.Notes, URL: info.URL})
}

// updateProgress reports how an install started by the desktop app's installUpdate is going,
// for the progress dialog to poll while it is open.
func (s *Server) updateProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, update.CurrentProgress())
}
