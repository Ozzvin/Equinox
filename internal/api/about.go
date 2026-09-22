package api

import (
	"net/http"
	"runtime"
	"runtime/debug"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

// About is the static "About" info of the running build, for the settings page.
type About struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	GoVer   string `json:"goVersion"`
	OS      string `json:"os"`     // "windows/amd64"
	Engine  string `json:"engine"` // e.g. "github.com/anacrolix/torrent v1.61.0", "" if unknown
}

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	a := About{
		Name: buildinfo.Name, Version: buildinfo.Version,
		GoVer: runtime.Version(), OS: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/anacrolix/torrent" {
				a.Engine = d.Path + " " + d.Version
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, a)
}
