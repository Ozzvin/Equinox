package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Notifications on Linux go to the desktop's notification server over D-Bus (org.freedesktop.Notifications, the
// same one notify-send talks to). Without a session bus, or with no server on it, Notify returns the error and
// nothing is shown.

const (
	notifyDest  = "org.freedesktop.Notifications"
	notifyPath  = "/org/freedesktop/Notifications"
	notifyIface = "org.freedesktop.Notifications"
)

var notifier struct {
	mu      sync.Mutex
	conn    *dbus.Conn
	ours    map[uint32]bool // ids of the notifications this program has shown, for their clicks
	onClick func()
}

// HaveSessionBus reports whether the user's session bus is there to connect to. Without one, godbus would try to start
// one with dbus-launch, and the tray library would fail on a connection it never got: a desktop without D-Bus, or a
// program started over SSH, then runs with no tray and no notifications.
func HaveSessionBus() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
		return true
	}
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(rt, "bus"))
	return err == nil
}

// bus returns the session bus, connecting once.
func bus() (*dbus.Conn, error) {
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if notifier.conn != nil {
		return notifier.conn, nil
	}
	if !HaveSessionBus() {
		return nil, errors.New("no D-Bus session bus")
	}
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	notifier.conn, notifier.ours = c, map[uint32]bool{}
	return c, nil
}

// Notify shows a notification with the program's name and icon.
func Notify(title, text string) error {
	c, err := bus()
	if err != nil {
		return err
	}
	// "default" is the action of a click on the notification itself; the servers that know it show no button for it
	actions := []string{"default", "Equinox"}
	hints := map[string]dbus.Variant{"desktop-entry": dbus.MakeVariant("equinox")}
	var id uint32
	err = c.Object(notifyDest, notifyPath).Call(notifyIface+".Notify", 0,
		"Equinox", uint32(0), "equinox", title, text, actions, hints, int32(-1)).Store(&id)
	if err != nil {
		return err
	}
	notifier.mu.Lock()
	notifier.ours[id] = true
	notifier.mu.Unlock()
	return nil
}

// WatchNotificationClicks calls onClick when the user clicks one of this program's notifications.
func WatchNotificationClicks(onClick func()) {
	c, err := bus()
	if err != nil {
		return
	}
	if err := c.AddMatchSignal(dbus.WithMatchInterface(notifyIface), dbus.WithMatchObjectPath(notifyPath)); err != nil {
		return
	}
	notifier.mu.Lock()
	notifier.onClick = onClick
	notifier.mu.Unlock()
	ch := make(chan *dbus.Signal, 16)
	c.Signal(ch)
	go func() {
		for s := range ch {
			if len(s.Body) < 1 {
				continue
			}
			id, _ := s.Body[0].(uint32)
			notifier.mu.Lock()
			ours, click := notifier.ours[id], notifier.onClick
			switch s.Name {
			case notifyIface + ".NotificationClosed":
				delete(notifier.ours, id)
			}
			notifier.mu.Unlock()
			if ours && click != nil && s.Name == notifyIface+".ActionInvoked" && len(s.Body) > 1 && s.Body[1] == "default" {
				click()
			}
		}
	}()
}
