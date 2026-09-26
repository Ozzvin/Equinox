package core

import (
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"github.com/anacrolix/torrent"
)

// The list of torrents can be filtered by tracker. A tracker is named the way people call it, from the address of its
// announce: "http://bt.t-ru.org/ann?magnet" is "rutracker", "udp://tracker.opentrackr.org:1337/announce" is
// "opentrackr". Only the host is used: an announce address of a private tracker holds the person's key.

// trackerAliases are the trackers that people call by another name than the site of their announce address.
var trackerAliases = map[string]string{
	"t-ru.org":      "rutracker",
	"rutracker.cc":  "rutracker",
	"rutracker.nl":  "rutracker",
	"rutracker.net": "rutracker",
}

// twoPartSuffixes are the endings that take two labels, so that "tracker.torrent.eu.org" is "torrent", not "eu".
var twoPartSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "com.au": true, "co.jp": true, "com.br": true, "com.cn": true, "co.nz": true,
	"eu.org": true, "com.ua": true, "org.ua": true, "co.il": true,
}

// TrackerName is the name of the tracker whose announce address is given: "" when there is no host in it.
func TrackerName(announce string) string {
	u, err := url.Parse(strings.TrimSpace(announce))
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return host // an address, not a name: it is what it is
	}
	labels := strings.Split(strings.TrimPrefix(host, "www."), ".")
	n := len(labels)
	if n < 2 {
		return host
	}
	site := labels[n-2] + "." + labels[n-1]
	if alias, ok := trackerAliases[site]; ok {
		return alias
	}
	if twoPartSuffixes[site] && n >= 3 {
		return labels[n-3]
	}
	return labels[n-2]
}

// trackerNames are the trackers of a torrent, each once, in the order of their names: the ones in the torrent itself and
// the ones the person added.
func trackerNames(t *torrent.Torrent, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(announce string) {
		if name := TrackerName(announce); name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	mi := t.Metainfo()
	for _, tier := range mi.UpvertedAnnounceList() {
		for _, u := range tier {
			add(u)
		}
	}
	for _, u := range extra {
		add(u)
	}
	sort.Strings(out)
	return out
}
