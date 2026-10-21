// Package changelog reads CHANGELOG.md: a heading "## 1.0.24" for each version and, under it, what it changed.
//
// A line of the list that concerns one system only starts with its tag: "- [windows] …", "- [umbrel] …",
// "- [linux] …", or several at once ("- [umbrel,linux] …"). A line without a tag is common to all of them.
package changelog

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

// Entry is one version of the changelog.
type Entry struct {
	Version string `json:"version"`
	Body    string `json:"body"` // Markdown, as written
}

// Platforms are the tags a line of the list may carry.
var Platforms = []string{"windows", "umbrel", "linux"}

// sinceOnServers is the first version shown on Umbrel and Linux: the 1.0.x entries were written for Windows alone
// and carry no tags.
const sinceOnServers = "1.1.0"

// Parse splits the changelog into its versions, the newest first as they stand in the file. What comes before the first
// "## " heading (the title) is dropped.
func Parse(md string) []Entry {
	var out []Entry
	var body []string
	flush := func() {
		if len(out) > 0 {
			out[len(out)-1].Body = strings.TrimSpace(strings.Join(body, "\n"))
		}
		body = body[:0]
	}
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if v, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			out = append(out, Entry{Version: strings.TrimSpace(v)})
			continue
		}
		if len(out) > 0 {
			body = append(body, line)
		}
	}
	flush()
	return out
}

// tagRe is the tag at the start of an item: "- [umbrel,linux] text".
var tagRe = regexp.MustCompile(`^([-*] )\[([a-z]+(?:\s*,\s*[a-z]+)*)\]\s+`)

// Tags returns the platforms an item of the list is tagged with, nil for a line without a tag.
func Tags(line string) []string {
	m := tagRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	var tags []string
	for _, t := range strings.Split(m[2], ",") {
		tags = append(tags, strings.TrimSpace(t))
	}
	return tags
}

// For keeps what the given platform should see: the common lines and its own, without their tags. A heading
// ("### Исправлено") left with no lines goes, and so does a version left with none; on Umbrel and Linux the
// versions before 1.1.0 are not shown at all.
func For(entries []Entry, platform string) []Entry {
	var out []Entry
	for _, e := range entries {
		if platform != "windows" && Less(e.Version, sinceOnServers) {
			continue
		}
		if body := filter(e.Body, platform); body != "" {
			out = append(out, Entry{Version: e.Version, Body: body})
		}
	}
	return out
}

// filter drops the items tagged for other platforms (with the lines that continue them) and the headings left empty.
func filter(body, platform string) string {
	var kept []string
	heading := -1 // the index in kept of the heading with nothing under it yet
	show := true  // the item being continued is shown
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "#"):
			if heading >= 0 {
				kept = kept[:heading]
			}
			heading, show = len(kept), true
			kept = append(kept, line)
			continue
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			show = true
			if tags := Tags(line); tags != nil {
				show = false
				for _, t := range tags {
					show = show || t == platform
				}
				line = tagRe.ReplaceAllString(line, "$1")
			}
			if show {
				heading = -1
			}
		case strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			show = true // plain text under a heading
			heading = -1
		}
		if show || strings.TrimSpace(line) == "" { // a blank line also parts what comes after a dropped item
			kept = append(kept, line)
		}
	}
	if heading >= 0 {
		kept = kept[:heading]
	}
	if !hasText(kept) {
		return ""
	}
	// blank lines that dropped items left next to each other
	var out []string
	for _, l := range kept {
		if strings.TrimSpace(l) == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Sections are the headings of a version's lists ("### Новое"), the part of a release's description the update window shows.
var Sections = []string{"Новое", "Исправлено"}

// proseTags are the words a release's description writes in place of a tag, since the copies before 1.1.1 show the
// description as it is and would show the brackets: "- Только для Docker и Umbrel: …" is "- [umbrel] …".
var proseTags = []struct{ prefix, tag string }{
	{"Только для Docker и Umbrel: ", "umbrel"},
	{"Только для Umbrel: ", "umbrel"},
	{"Только для Windows: ", "windows"},
	{"Только для Linux: ", "linux"},
}

// ReleaseNotes is what the update window shows of a release's description from GitHub: the items of its Sections
// that concern the platform, without their tags. The rest of the page (the introduction, what runs where, the
// downloads, a note after the lists) is for the release's page. A description with none of the Sections (an
// unexpected shape) is only filtered by tags, so that the window still says something.
func ReleaseNotes(body, platform string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		for _, p := range proseTags {
			if rest, ok := strings.CutPrefix(line, "- "+p.prefix); ok {
				r, size := utf8.DecodeRuneInString(rest)
				lines[i] = "- [" + p.tag + "] " + string(unicode.ToUpper(r)) + rest[size:]
				break
			}
		}
	}
	var kept []string
	in, found, item := false, false, false
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			in = strings.HasPrefix(line, "### ") && slices.Contains(Sections, strings.TrimSpace(line[4:]))
			found, item = found || in, false
			if in {
				kept = append(kept, line)
			}
			continue
		}
		switch {
		case !in:
			continue
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			item = true
		case strings.TrimSpace(line) == "", strings.HasPrefix(line, " "), strings.HasPrefix(line, "\t"):
			// a blank line, or the item going on
		default:
			item = false // a paragraph after the list
		}
		if item || strings.TrimSpace(line) == "" {
			kept = append(kept, line)
		}
	}
	if !found {
		kept = lines
	}
	return filter(strings.Join(kept, "\n"), platform)
}

func hasText(lines []string) bool {
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return true
		}
	}
	return false
}

// Base is the version a build belongs to in the changelog: "1.1.0" for "1.1.0-rc17", "1.0.48" for "beta-1.0.48".
func Base(version string) string {
	v := strings.TrimPrefix(version, buildinfo.BetaPrefix)
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	return v
}

// Less compares two versions such as "1.0.47" and "1.1.0" number by number.
func Less(a, b string) bool {
	pa, pb := strings.Split(Base(a), "."), strings.Split(Base(b), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}
