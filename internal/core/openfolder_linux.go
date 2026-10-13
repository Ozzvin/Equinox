package core

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"
)

// openPath shows path in the file manager with it selected, through the D-Bus interface org.freedesktop.FileManager1
// that Nautilus, Dolphin, Nemo, Caja and Thunar answer. Without it the folder is opened with xdg-open: the folder
// itself, never a file (xdg-open on a file would open the film in a player, not show where it is).
func openPath(path string) error {
	if showItem(path) == nil {
		return nil
	}
	dir := path
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	return exec.Command("xdg-open", dir).Start()
}

func showItem(path string) error {
	if !haveSessionBus() {
		return os.ErrNotExist
	}
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer c.Close()
	u := url.URL{Scheme: "file", Path: path}
	obj := c.Object("org.freedesktop.FileManager1", "/org/freedesktop/FileManager1")
	call := obj.Go("org.freedesktop.FileManager1.ShowItems", 0, nil, []string{u.String()}, "") // a hung file manager must not hold the request
	select {
	case <-call.Done:
		return call.Err
	case <-time.After(5 * time.Second):
		return os.ErrDeadlineExceeded
	}
}

// haveSessionBus reports whether a session bus is there to connect to. Without one, godbus would try to start one with
// dbus-launch, which a server (or a container) has no use for.
func haveSessionBus() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
		return true
	}
	_, err := os.Stat(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "bus"))
	return os.Getenv("XDG_RUNTIME_DIR") != "" && err == nil
}
