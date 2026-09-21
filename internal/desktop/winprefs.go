package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Sizes and position of the application window, remembered between starts. The window has a size for
// each density of the interface (the page reports which one is on), so a compact window stays small
// and a large one stays large.

const (
	DensityLarge    = "large"
	DensityStandard = "standard"
	DensityCompact  = "compact"
)

// Size is in pixels.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Client sizes the window gets the first time, per density.
var defaultSizes = map[string]Size{
	DensityLarge:    {1280, 800},
	DensityStandard: {1000, 640},
	DensityCompact:  {640, 400},
}

// The smallest outer size of the window, per density.
var minSizes = map[string]Size{
	DensityLarge:    {720, 480},
	DensityStandard: {720, 480},
	DensityCompact:  {640, 400},
}

// ValidDensity reports whether d is one of the known densities.
func ValidDensity(d string) bool { _, ok := defaultSizes[d]; return ok }

// DefaultSize is the client size of a window that has no remembered size.
func DefaultSize(d string) Size {
	if s, ok := defaultSizes[d]; ok {
		return s
	}
	return defaultSizes[DensityStandard]
}

// MinSize is the smallest outer size the window may be given.
func MinSize(d string) Size {
	if s, ok := minSizes[d]; ok {
		return s
	}
	return minSizes[DensityStandard]
}

// WindowPrefs is what is remembered about the window.
type WindowPrefs struct {
	Density string          `json:"density"`
	HasPos  bool            `json:"hasPos"`
	X       int             `json:"x"` // outer position
	Y       int             `json:"y"`
	Max     bool            `json:"maximized"`
	Sizes   map[string]Size `json:"sizes"` // outer sizes the person has left the window at
}

// LoadWindowPrefs reads the file; a missing or broken file gives the defaults.
func LoadWindowPrefs(path string) WindowPrefs {
	p := WindowPrefs{Density: DensityStandard, Sizes: map[string]Size{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	var got WindowPrefs
	if json.Unmarshal(b, &got) != nil {
		return p
	}
	if ValidDensity(got.Density) {
		p.Density = got.Density
	}
	p.HasPos, p.X, p.Y, p.Max = got.HasPos, got.X, got.Y, got.Max
	for d, s := range got.Sizes {
		if ValidDensity(d) && s.W >= MinSize(d).W && s.H >= MinSize(d).H && s.W <= 20000 && s.H <= 20000 {
			p.Sizes[d] = s
		}
	}
	return p
}

// Save writes the file (through a temporary one, so a crash cannot leave half of it).
func (p WindowPrefs) Save(path string) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Saved returns the remembered outer size for a density, if there is one.
func (p WindowPrefs) Saved(d string) (Size, bool) { s, ok := p.Sizes[d]; return s, ok }

// SetSize remembers the outer size for a density.
func (p *WindowPrefs) SetSize(d string, s Size) {
	if !ValidDensity(d) {
		return
	}
	if p.Sizes == nil {
		p.Sizes = map[string]Size{}
	}
	p.Sizes[d] = s
}

// ForgetSize drops the remembered size of a density, so it goes back to the default.
func (p *WindowPrefs) ForgetSize(d string) { delete(p.Sizes, d) }

// PosVisible reports whether a window at (x, y) would still be reachable on a screen area
// (the whole desktop): a monitor that was unplugged must not leave the window out of sight.
func PosVisible(x, y, vx, vy, vw, vh int) bool {
	if vw <= 0 || vh <= 0 {
		return false
	}
	return x+120 > vx && x < vx+vw-120 && y >= vy-20 && y < vy+vh-60
}
