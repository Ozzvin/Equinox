// Package changelog reads CHANGELOG.md: a heading "## 1.0.24" for each version and, under it, what it changed.
package changelog

import "strings"

// Entry is one version of the changelog.
type Entry struct {
	Version string `json:"version"`
	Body    string `json:"body"` // Markdown, as written
}

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
