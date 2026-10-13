package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// packagedExe is where the .deb and .rpm put the program, next to their own /usr/share/applications/equinox.desktop.
const packagedExe = "/usr/bin/equinox"

// LauncherPath is the program file that a menu entry or the autostart should start. Inside an AppImage the running
// file is in a mount that changes on every start, so the AppImage itself ($APPIMAGE) is taken instead.
func LauncherPath() (string, error) {
	if p := os.Getenv("APPIMAGE"); p != "" {
		return p, nil
	}
	return os.Executable()
}

// dataHome is $XDG_DATA_HOME, ~/.local/share by default.
func dataHome() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// MakeDefault makes the program the one that opens .torrent files and magnet links (xdg-mime, which every desktop
// follows). The packages bring their own menu entry; an AppImage or a program unpacked by hand gets one made for the
// user, with the icon, so that the system has something to point at.
func MakeDefault() error {
	exe, err := LauncherPath()
	if err != nil {
		return err
	}
	entry := "equinox.desktop"
	if exe != packagedExe {
		entry = "equinox" + channelSuffix() + ".desktop"
		if err := writeUserEntry(exe, entry); err != nil {
			return err
		}
	}
	for _, mime := range []string{"x-scheme-handler/magnet", "application/x-bittorrent"} {
		if out, err := exec.Command("xdg-mime", "default", entry, mime).CombinedOutput(); err != nil {
			return fmt.Errorf("xdg-mime: %v %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// writeUserEntry puts a menu entry for exe, and the icon, into the user's own folders.
func writeUserEntry(exe, name string) error {
	data, err := dataHome()
	if err != nil {
		return err
	}
	icons := filepath.Join(data, "icons", "hicolor", "256x256", "apps")
	apps := filepath.Join(data, "applications")
	for _, d := range []string{icons, apps} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(icons, "equinox.png"), PNG(256), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(apps, name), []byte(menuEntry(exe)), 0o644); err != nil {
		return err
	}
	_ = exec.Command("update-desktop-database", apps).Run() // where there is one; the entry works without it too
	return nil
}

func menuEntry(exe string) string {
	return "[Desktop Entry]\nType=Application\nName=Equinox\nGenericName=Torrent Client\nGenericName[ru]=Торрент-клиент\n" +
		"Exec=" + quoteExec(exe) + " %U\nIcon=equinox\nTerminal=false\nCategories=Network;FileTransfer;P2P;\n" +
		"MimeType=application/x-bittorrent;x-scheme-handler/magnet;\nStartupNotify=false\n"
}
