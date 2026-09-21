//go:build windows

package desktop

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
	softwareRoot = `Software`
	runSuffix    = `Microsoft\Windows\CurrentVersion\Run`
	runName      = "Equinox"
)

func exePath() (string, error) { return os.Executable() }

// AutostartEnabled reports whether the app starts with Windows.
func AutostartEnabled() bool { return autostartEnabled(softwareRoot) }

func autostartEnabled(root string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, root+`\`+runSuffix, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runName)
	return err == nil
}

// SetAutostart adds or removes the app from the current user's startup list. With hidden the app
// then starts in the tray without opening its window.
func SetAutostart(on, hidden bool) error { return setAutostart(softwareRoot, on, hidden) }

func setAutostart(root string, on, hidden bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, root+`\`+runSuffix, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runName); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := exePath()
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(`"%s"`, exe)
	if hidden {
		cmd += " -hidden"
	}
	return k.SetStringValue(runName, cmd)
}

// RegisterHandlers makes the app selectable as the program for magnet links and .torrent
// files (under Settings → Default apps). Windows does not let a program silently take
// over these associations, so the user still confirms the choice there.
func RegisterHandlers() error { return registerHandlers(softwareRoot) }

// registerHandlers writes everything below root (normally "Software"; tests use a scratch
// branch so the real registry is left alone).
func registerHandlers(root string) error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(`"%s" "%%1"`, exe)

	set := func(path string, values map[string]string) error {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		for name, v := range values {
			if err := k.SetStringValue(name, v); err != nil {
				return err
			}
		}
		return nil
	}

	type step struct {
		path   string
		values map[string]string
	}
	steps := []step{
		{root + `\Classes\Equinox.Magnet`, map[string]string{"": "URL:Magnet link", "URL Protocol": ""}},
		{root + `\Classes\Equinox.Magnet\shell\open\command`, map[string]string{"": cmd}},
		{root + `\Classes\Equinox.Torrent`, map[string]string{"": "Torrent file"}},
		{root + `\Classes\Equinox.Torrent\shell\open\command`, map[string]string{"": cmd}},
		// "Open with" list.
		{root + `\Classes\Applications\Equinox.exe\shell\open\command`, map[string]string{"": cmd}},
		{root + `\Classes\Applications\Equinox.exe\SupportedTypes`, map[string]string{".torrent": ""}},
		// Makes the app appear in Settings → Default apps.
		{root + `\Equinox\Capabilities`, map[string]string{
			"ApplicationName":        "Equinox",
			"ApplicationDescription": "Torrent client",
		}},
		{root + `\Equinox\Capabilities\URLAssociations`, map[string]string{"magnet": "Equinox.Magnet"}},
		{root + `\Equinox\Capabilities\FileAssociations`, map[string]string{".torrent": "Equinox.Torrent"}},
		{root + `\RegisteredApplications`, map[string]string{"Equinox": root + `\Equinox\Capabilities`}},
	}
	for _, s := range steps {
		if err := set(s.path, s.values); err != nil {
			return fmt.Errorf("%s: %w", s.path, err)
		}
	}
	return nil
}
