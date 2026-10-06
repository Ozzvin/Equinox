package api

import (
	"sort"

	"github.com/Ozzvin/equinox/internal/config"
	"github.com/Ozzvin/equinox/internal/core"
)

// colorLabels gives every label that has no colour yet (one a torrent carries or one with a folder in the settings)
// a colour of its own: the one of the palette fewest labels have, the earlier in the palette on a tie, so labels
// made one after another come out as different as the palette allows. Labels are not given a colour from their
// name any more, which made two labels of the same colour much too often.
func (s *Server) colorLabels(list []core.Status) {
	cur := s.cfg.Get()
	var missing []string
	seen := map[string]bool{}
	want := func(l string) {
		if l == "" || seen[l] {
			return
		}
		seen[l] = true
		if !config.ValidLabelColor(cur.LabelColors[l]) {
			missing = append(missing, l)
		}
	}
	for _, t := range list {
		want(t.Label)
	}
	for l := range cur.LabelPaths {
		want(l)
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	_ = s.cfg.Update(func(c *config.Settings) {
		var still []string // another request may have coloured some meanwhile
		for _, l := range missing {
			if !config.ValidLabelColor(c.LabelColors[l]) {
				still = append(still, l)
			}
		}
		c.LabelColors = config.AssignLabelColors(c.LabelColors, still)
	})
}
