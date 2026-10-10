package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ozzvin/equinox/internal/api"
)

// settings.yml as umbrelOS 2.0 writes it for the app, with a folder added under "Add your own folder".
const sampleUmbrelSettings = `dependencies: {}
autoStart: true
customMounts:
  - serviceName: server
    targetPath: /storage
    sourcePath: /External/RAID/Torrents
    readOnly: false
  - serviceName: server
    targetPath: "/my films"
    sourcePath: "/Network/nas/Films HD"
    readOnly: true
`

func TestUmbrelNames(t *testing.T) {
	dl, mounts := umbrelNames(strings.NewReader(sampleUmbrelSettings))
	if dl != "" {
		t.Errorf("Downloads moved to %q, but the file does not say so", dl)
	}
	if mounts["/storage"] != "/External/RAID/Torrents" || mounts["/my films"] != "/Network/nas/Films HD" || len(mounts) != 2 {
		t.Fatalf("mounts %v", mounts)
	}

	dl, _ = umbrelNames(strings.NewReader("folderAccess:\n  umbrel-downloads: /External/RAID/Downloads\n"))
	if dl != "/External/RAID/Downloads" {
		t.Errorf("Downloads pointed elsewhere: got %q", dl)
	}
}

// With the copy of Umbrel's settings in the state folder, the places are named by the paths of its Files.
func TestPlacesNamedByUmbrelFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, umbrelSettingsFile), []byte(sampleUmbrelSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	got := placesFor(dir, []api.Place{{Name: "Home/Downloads", Path: "/downloads"}, {Name: "x", Path: "/storage"}})
	if got[0].Name != "Home/Downloads" || got[1].Name != "External/RAID/Torrents" {
		t.Fatalf("places %+v", got)
	}
	if got := placesFor(t.TempDir(), []api.Place{{Name: "Home/Downloads", Path: "/downloads"}}); got[0].Name != "Home/Downloads" {
		t.Fatalf("without the copy the names stay: %+v", got)
	}
}
