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

// updateCheck asks GitHub for the latest release. ?force=1 bypasses the hourly cache, for the
// "check now" button; the periodic background check leaves it off.
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	info, err := update.Check(r.Context(), r.URL.Query().Get("force") == "1")
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
