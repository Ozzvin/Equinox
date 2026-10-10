package webui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Ad blockers (uBlock Origin, AdGuard, Adblock: the EasyList rules) hide elements on every site by their class or
// id alone: ".ad-row", ".ad-side", ".ad-buttons" are among those rules, and the "Add torrents" dialog once used
// exactly these names, so in a browser with a blocker it showed no options at all (found on Umbrel, 2026-10-10;
// the desktop app's WebView2 has no blocker). This keeps such words out of the page's class and id names.
var adBait = regexp.MustCompile(`(^|[-_])(ad|ads|adv|advert|adverts|advertisement|banner|banners|sponsor|sponsored|promo|promoted)([-_]|$)`)

var nameSources = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:class|id)="([^"$]*)"`),                           // markup, also inside the script's templates
	regexp.MustCompile(`classList\.(?:add|toggle|remove)\("([A-Za-z0-9_-]+)`), // classes set by the script
	regexp.MustCompile(`[.#]([A-Za-z][A-Za-z0-9_-]*)`),                        // selectors in the style sheets
}

func TestNoNamesAdBlockersHide(t *testing.T) {
	err := fs.WalkDir(files, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		css := strings.HasSuffix(path, ".css")
		if !css && !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".js") {
			return nil
		}
		b, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		for i, re := range nameSources {
			if (i == 2) != css { // the selector pattern only makes sense in a style sheet
				continue
			}
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				for _, name := range strings.Fields(m[1]) {
					if adBait.MatchString(strings.ToLower(name)) {
						t.Errorf("%s: %q looks like an ad to ad blockers, which hide it in the browser", path, name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
