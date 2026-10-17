package webui

import (
	"os/exec"
	"strings"
	"testing"
)

// On Umbrel the page shows paths by the names of Umbrel's Files ("Downloads/x.torrent") and turns them back into the
// server's paths when it sends them (placePaths in app.js). Several .torrent files chosen at once go as a list, "paths":
// rc16 sent that list untouched, the server got "Downloads/x.torrent", which is not a path of its, and refused every file
// (found 2026-10-17). This runs that part of the page in node with two places and checks both ways.
func TestPlacePathsTurnsListsOfPathsBack(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	b, err := files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	const from, to = "// ---------- folders by the names of the system around (Umbrel) ----------", "// ---------- language ----------"
	i, j := strings.Index(src, from), strings.Index(src, to)
	if i < 0 || j < i {
		t.Fatal("the part of app.js with the places is not found")
	}
	script := `window = { __equinoxPlaces: [{ name: "Downloads", path: "/downloads" }, { name: "Media", path: "/media/" }] };
` + src[i:j] + `
const eq = (a, b, what) => { if (JSON.stringify(a) !== JSON.stringify(b)) { console.log(what + ": " + JSON.stringify(a)); process.exitCode = 1; } };
eq(placePaths({ paths: ["Downloads/a.torrent", "Media/x/b.torrent", "/elsewhere/c.torrent"] }, realPath),
  { paths: ["/downloads/a.torrent", "/media/x/b.torrent", "/elsewhere/c.torrent"] }, "sent");
eq(placePaths({ path: "Downloads/a.torrent" }, realPath), { path: "/downloads/a.torrent" }, "one sent");
eq(placePaths({ failed: [{ path: "/downloads/c.torrent", code: "stage.no_file" }] }, showPath),
  { failed: [{ path: "Downloads/c.torrent", code: "stage.no_file" }] }, "answer");
eq(placePaths({ labels: ["Downloads"] }, realPath), { labels: ["Downloads"] }, "a label named as a place");
`
	cmd := exec.Command(node, "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
