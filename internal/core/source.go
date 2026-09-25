package core

import (
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// What the .torrent file itself says about the torrent: the comment, who made it and when, and the page of the
// torrent on the tracker it came from ("publisher-url", which trackers put next to the info). The engine cannot
// say: what it builds from the info alone (Torrent.Metainfo) carries a comment of its own ("dynamic metainfo from
// client") and the time of the call as the creation date. So these are read from the file when the torrent is added
// and kept in its record.

// sourceMeta is that information as it is kept.
type sourceMeta struct {
	Comment      string `json:"comment,omitempty"`
	CreatedBy    string `json:"createdBy,omitempty"`
	CreatedAt    int64  `json:"createdAt,omitempty"` // Unix time
	Publisher    string `json:"publisher,omitempty"`
	PublisherURL string `json:"publisherUrl,omitempty"` // an http(s) address, nothing else
}

func (s sourceMeta) empty() bool { return s == sourceMeta{} }

// engineMade tells the text and the creator the engine puts into the metadata it builds itself.
func engineMade(comment, createdBy string) bool {
	return comment == "dynamic metainfo from client" || strings.HasPrefix(createdBy, "github.com/anacrolix/torrent")
}

// parseSource reads the information out of a .torrent: from the raw bytes when there are any (they hold the keys
// the metainfo type does not know, such as publisher-url), otherwise from the parsed structure. What the engine
// made up itself is left out.
func parseSource(raw []byte, mi *metainfo.MetaInfo) sourceMeta {
	var s sourceMeta
	if mi != nil {
		s.Comment, s.CreatedBy = mi.Comment, mi.CreatedBy
		if mi.CreationDate > 0 {
			s.CreatedAt = mi.CreationDate
		}
	}
	if len(raw) > 0 {
		var top map[string]bencode.Bytes
		if bencode.Unmarshal(raw, &top) == nil {
			str := func(keys ...string) string {
				for _, k := range keys {
					if b, ok := top[k]; ok {
						var v string
						if bencode.Unmarshal(b, &v) == nil && strings.TrimSpace(v) != "" {
							return fixEncoding(v, top)
						}
					}
				}
				return ""
			}
			if c := str("comment.utf-8", "comment"); c != "" {
				s.Comment = c
			}
			if c := str("created by.utf-8", "created by"); c != "" {
				s.CreatedBy = c
			}
			s.Publisher = str("publisher.utf-8", "publisher")
			s.PublisherURL = webAddress(str("publisher-url.utf-8", "publisher-url"))
		}
	}
	s.Comment, s.CreatedBy = strings.TrimSpace(s.Comment), strings.TrimSpace(s.CreatedBy)
	if engineMade(s.Comment, s.CreatedBy) {
		s = sourceMeta{Publisher: s.Publisher, PublisherURL: s.PublisherURL}
	}
	return s
}

// webAddress returns the address if it is an http or https one, and "" otherwise: it becomes a link in the
// interface, and a torrent is somebody else's file.
func webAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 2048 {
		return ""
	}
	return raw
}

// fixEncoding turns text of a torrent that says it is in windows-1251 (the "encoding" key; Russian trackers have
// such files) into UTF-8. Text that is valid UTF-8 already is left alone.
func fixEncoding(v string, top map[string]bencode.Bytes) string {
	if utf8.ValidString(v) {
		return v
	}
	var enc string
	if b, ok := top["encoding"]; ok {
		_ = bencode.Unmarshal(b, &enc)
	}
	if e := strings.ToLower(enc); strings.Contains(e, "1251") || strings.Contains(e, "cp1251") {
		rs := make([]rune, 0, len(v))
		for i := 0; i < len(v); i++ {
			c := v[i]
			switch {
			case c < 0x80:
				rs = append(rs, rune(c))
			case c >= 0xC0:
				rs = append(rs, rune(c)-0xC0+0x410) // А..я
			case c == 0xA8:
				rs = append(rs, 0x401) // Ё
			case c == 0xB8:
				rs = append(rs, 0x451) // ё
			default:
				rs = append(rs, utf8.RuneError)
			}
		}
		return string(rs)
	}
	return strings.ToValidUTF8(v, "?")
}

// sourceOf returns what the torrent's file said. A torrent added before this was kept has no record of it; its
// internal copy of the metadata still holds the comment, the creator and the date (not the publisher: the copy was
// written from the parsed structure), and they are taken from there and remembered.
func (m *Manager) sourceOf(hash string, rec record) sourceMeta {
	if rec.Source != nil {
		return *rec.Source
	}
	var s sourceMeta
	if mi, err := metainfo.LoadFromFile(m.metaPath(hash)); err == nil {
		s = parseSource(nil, mi)
	}
	m.state.touch(func(st *state) {
		if r := st.Torrents[hash]; r != nil && r.Source == nil {
			r.Source = &s
		}
	})
	return s
}

// mergeSource fills in what a torrent's record does not know yet from a .torrent file given for it again (the file
// of a torrent that was added before its publisher-url was kept, for instance). Nothing already there is replaced.
func (m *Manager) mergeSource(hash string, raw []byte, mi *metainfo.MetaInfo) {
	add := parseSource(raw, mi)
	if add.empty() {
		return
	}
	_ = m.state.with(func(st *state) {
		r := st.Torrents[hash]
		if r == nil {
			return
		}
		if r.Source == nil {
			r.Source = &sourceMeta{}
		}
		cur := r.Source
		if cur.Comment == "" {
			cur.Comment = add.Comment
		}
		if cur.CreatedBy == "" {
			cur.CreatedBy = add.CreatedBy
		}
		if cur.CreatedAt == 0 {
			cur.CreatedAt = add.CreatedAt
		}
		if cur.Publisher == "" {
			cur.Publisher = add.Publisher
		}
		if cur.PublisherURL == "" {
			cur.PublisherURL = add.PublisherURL
		}
	})
}

// createdTime is the creation date as a time, zero if the torrent does not say.
func (s sourceMeta) createdTime() time.Time {
	if s.CreatedAt <= 0 {
		return time.Time{}
	}
	return time.Unix(s.CreatedAt, 0)
}
