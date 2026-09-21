package api

import (
	"net/http"

	"github.com/Ozzvin/equinox/internal/core"
)

// The Status tab extras and the per-torrent options that have no place in the list.

func (s *Server) statusExtra(w http.ResponseWriter, r *http.Request) {
	x, err := s.m.StatusExtra(r.PathValue("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, x)
}

// edgePieces switches "first and last pieces first": POST {"enabled": true|false}.
func (s *Server) edgePieces(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetEdgePieces(r.PathValue("hash"), b.Enabled); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// moveDone sets the folder a torrent moves to when it finishes: POST {"path": "..."} ("" = the global setting).
func (s *Server) moveDone(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Path string `json:"path"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	if err := s.m.SetMoveDone(r.PathValue("hash"), b.Path); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
