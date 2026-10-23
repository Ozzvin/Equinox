package api

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeDisks(t *testing.T, f func(dir string) (uint64, uint64, error)) {
	t.Helper()
	old := diskUsage
	diskUsage = f
	t.Cleanup(func() { diskUsage = old })
}

// The places of the folder picker and the folder shown carry the size of their disk and what is free on it, for the
// bars of the window (Umbrel: each external drive its own numbers).
func TestFolderPickerSaysHowFullEachDiskIs(t *testing.T) {
	var dl, raid string
	e := setup(t, func(s *Server) {
		dl, raid = filepath.Join(t.TempDir(), "downloads"), filepath.Join(t.TempDir(), "raid")
		s.SetPlaces([]Place{{Name: "Downloads", Path: dl}, {Name: "RAID/Torrents", Path: raid}})
	})
	for _, d := range []string{dl, raid} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeDisks(t, func(dir string) (uint64, uint64, error) {
		if strings.HasPrefix(dir, raid) {
			return 4000 << 30, 8 << 30, nil
		}
		return 468 << 30, 360 << 30, nil
	})

	l := fsGet(t, e, raid)
	if len(l.Places) != 2 {
		t.Fatalf("places %+v", l.Places)
	}
	for i, want := range [][2]uint64{{468 << 30, 360 << 30}, {4000 << 30, 8 << 30}} {
		p := l.Places[i]
		if p.TotalBytes == nil || p.FreeBytes == nil || *p.TotalBytes != want[0] || *p.FreeBytes != want[1] {
			t.Errorf("%s: %v %v, want %v", p.Name, p.TotalBytes, p.FreeBytes, want)
		}
	}
	if l.FreeBytes == nil || *l.FreeBytes != 8<<30 {
		t.Fatalf("the folder shown is on the RAID: %v", l.FreeBytes)
	}
}

// A disk that cannot say (an error, a network file system with no size) or does not answer in time gets no numbers:
// the page draws no bar, and the window does not wait for it.
func TestFolderPickerWithoutDiskSizes(t *testing.T) {
	var dl, slow, nas string
	e := setup(t, func(s *Server) {
		dl, slow, nas = filepath.Join(t.TempDir(), "downloads"), filepath.Join(t.TempDir(), "asleep"), filepath.Join(t.TempDir(), "nas")
		s.SetPlaces([]Place{{Name: "Downloads", Path: dl}, {Name: "Asleep", Path: slow}, {Name: "NAS", Path: nas}})
	})
	for _, d := range []string{dl, slow, nas} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := spaceWait
	spaceWait = 200 * time.Millisecond
	t.Cleanup(func() { spaceWait = old })
	fakeDisks(t, func(dir string) (uint64, uint64, error) {
		switch {
		case strings.HasPrefix(dir, slow):
			time.Sleep(3 * time.Second)
			return 1 << 40, 1 << 30, nil
		case strings.HasPrefix(dir, nas):
			return 0, 0, nil
		}
		return 0, 0, errors.New("statfs: function not implemented")
	})

	start := time.Now()
	r := e.do(t, "GET", "/api/fs?path="+url.QueryEscape(dl), nil, nil)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the listing waited %v for a disk that did not answer", d)
	}
	if strings.Contains(string(body), "totalBytes") || strings.Contains(string(body), "freeBytes") {
		t.Fatalf("no numbers when no disk gave them: %s", body)
	}
}

// Without given places the usual folders (all on the system drive) get no bar; the download folder and the drives do.
func TestFolderPickerMeasuresDrivesNotEveryFolder(t *testing.T) {
	e := setup(t)
	fakeDisks(t, func(string) (uint64, uint64, error) { return 100 << 30, 50 << 30, nil })
	for _, p := range fsGet(t, e, "").Places {
		measured := p.TotalBytes != nil
		if want := p.Kind == "drive" || p.Kind == "data"; measured != want {
			t.Errorf("%s (%s): measured %v, want %v", p.Name, p.Kind, measured, want)
		}
	}
}
