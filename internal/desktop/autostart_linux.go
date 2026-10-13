package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

// Autostart on Linux is a .desktop file in ~/.config/autostart (the XDG autostart specification, which GNOME, KDE,
// Xfce and the others follow).

// autostartFile is where the entry lives: $XDG_CONFIG_HOME/autostart, ~/.config/autostart by default.
func autostartFile() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "equinox"+channelSuffix()+".desktop"), nil
}

// channelSuffix keeps a test build's entry apart from the installed program's.
func channelSuffix() string {
	if ch := buildinfo.Channel(); ch != "" {
		return "-" + ch
	}
	return ""
}

// AutostartEnabled reports whether the program starts when the user logs in.
func AutostartEnabled() bool {
	p, err := autostartFile()
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(p)
	return err == nil && !strings.Contains(string(raw), "\nHidden=true")
}

// SetAutostart turns starting at login on or off; hidden starts it in the tray without opening the browser.
func SetAutostart(on, hidden bool) error {
	p, err := autostartFile()
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := LauncherPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(autostartEntry(exe, hidden)), 0o644)
}

func autostartEntry(exe string, hidden bool) string {
	cmd := quoteExec(exe)
	if hidden {
		cmd += " -hidden"
	}
	return "[Desktop Entry]\nType=Application\nName=Equinox\nExec=" + cmd + "\nIcon=equinox\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"
}

// quoteExec quotes a path for the Exec key of a .desktop file when it needs it.
func quoteExec(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\$`") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", `$`, `\\$`)
	return `"` + r.Replace(s) + `"`
}
