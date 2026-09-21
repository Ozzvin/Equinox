package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowPrefsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	if p := LoadWindowPrefs(path); p.Density != DensityStandard || p.HasPos || len(p.Sizes) != 0 {
		t.Fatalf("a missing file must give the defaults: %+v", p)
	}
	p := LoadWindowPrefs(path)
	p.Density, p.HasPos, p.X, p.Y = DensityCompact, true, 100, 50
	p.SetSize(DensityCompact, Size{700, 420})
	p.SetSize(DensityStandard, Size{1234, 777})
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	q := LoadWindowPrefs(path)
	if q.Density != DensityCompact || !q.HasPos || q.X != 100 || q.Y != 50 {
		t.Fatalf("not stored: %+v", q)
	}
	if s, ok := q.Saved(DensityStandard); !ok || s != (Size{1234, 777}) {
		t.Fatalf("size lost: %+v", q.Sizes)
	}
	q.ForgetSize(DensityStandard)
	if _, ok := q.Saved(DensityStandard); ok {
		t.Fatal("a forgotten size must be gone")
	}
}

func TestWindowPrefsIgnoreNonsense(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	bad := `{"density":"huge","sizes":{"standard":{"w":10,"h":10},"large":{"w":99999,"h":800},"nope":{"w":900,"h":900},"compact":{"w":650,"h":410}}}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	p := LoadWindowPrefs(path)
	if p.Density != DensityStandard {
		t.Fatalf("an unknown density falls back to standard, got %q", p.Density)
	}
	if len(p.Sizes) != 1 {
		t.Fatalf("only the sane size may stay: %+v", p.Sizes)
	}
	if _, ok := p.Saved(DensityCompact); !ok {
		t.Fatal("the sane size was dropped")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if q := LoadWindowPrefs(path); q.Density != DensityStandard || len(q.Sizes) != 0 {
		t.Fatalf("a broken file gives the defaults: %+v", q)
	}
}

func TestSizesPerDensity(t *testing.T) {
	if DefaultSize(DensityLarge) != (Size{1280, 800}) || DefaultSize(DensityStandard) != (Size{1000, 640}) || DefaultSize(DensityCompact) != (Size{640, 400}) {
		t.Fatal("default sizes changed")
	}
	if MinSize(DensityCompact) != (Size{640, 400}) || MinSize(DensityStandard) != (Size{720, 480}) {
		t.Fatal("minimum sizes changed")
	}
	if !ValidDensity("large") || ValidDensity("mini") || ValidDensity("") {
		t.Fatal("ValidDensity")
	}
}

func TestPosVisible(t *testing.T) {
	// one 1920x1080 screen
	if !PosVisible(100, 100, 0, 0, 1920, 1080) {
		t.Fatal("a normal position is visible")
	}
	if PosVisible(3000, 100, 0, 0, 1920, 1080) || PosVisible(100, 2000, 0, 0, 1920, 1080) {
		t.Fatal("a window on an unplugged monitor is not visible")
	}
	if !PosVisible(-1200, 40, -1920, 0, 3840, 1080) {
		t.Fatal("a monitor on the left has negative coordinates")
	}
	if PosVisible(0, 0, 0, 0, 0, 0) {
		t.Fatal("no screen, nothing is visible")
	}
}
