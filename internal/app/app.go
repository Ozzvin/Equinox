// Package app starts the whole daemon (engine + HTTP API + web UI). It is shared by the
// console daemon and the Windows desktop application.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Ozzvin/equinox/internal/api"
	"github.com/Ozzvin/equinox/internal/config"
	"github.com/Ozzvin/equinox/internal/core"
	"github.com/Ozzvin/equinox/internal/webui"
)

// App is a running daemon.
type App struct {
	Manager  *core.Manager
	Settings *config.Store
	// URL opens the web UI with the access key already included.
	URL string
	// Token is the API access key.
	Token string
	// StateDir is where settings, state, torrent copies and downloads live.
	StateDir string

	srv *http.Server
}

// Start brings everything up. listen is the preferred HTTP address; if it is taken, a
// free loopback port is used instead. Only loopback addresses are accepted.
func Start(stateDir, listen string) (*App, error) {
	if err := checkLoopback(listen); err != nil {
		return nil, err
	}
	cfg, err := config.Load(filepath.Join(stateDir, "settings.json"), stateDir)
	if err != nil {
		return nil, err
	}
	m, err := core.New(cfg, stateDir)
	if err != nil {
		return nil, err
	}
	token, err := api.LoadToken(filepath.Join(stateDir, "api-token"))
	if err != nil {
		m.Close()
		return nil, err
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil { // preferred port is busy: take any free one
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		m.Close()
		return nil, err
	}
	a := &App{
		Manager: m, Settings: cfg, Token: token, StateDir: stateDir,
		URL: fmt.Sprintf("http://%s/#token=%s", ln.Addr(), token),
		srv: &http.Server{Handler: api.New(m, cfg, token, webui.Handler()), ReadHeaderTimeout: 10 * time.Second},
	}
	go func() { _ = a.srv.Serve(ln) }()
	return a, nil
}

// Close stops the HTTP server and the engine.
func (a *App) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.srv.Shutdown(ctx)
	a.Manager.Close()
}

func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("the API must listen on a loopback address, got " + addr)
	}
	return nil
}

// ExeDir returns the directory of the running executable.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}
