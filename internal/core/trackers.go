package core

import (
	"net/netip"
	"net/url"
	"strings"

	"github.com/anacrolix/torrent"
)

// The list of torrents can be filtered by tracker: each torrent belongs to its main one. A tracker is named the way people
// call it, from the address of its announce: "http://bt.t-ru.org/ann?magnet" is "rutracker", "udp://tracker.opentrackr.org:1337/announce"
// is "opentrackr". Only the host is used: an announce address of a private tracker holds the person's key.

// trackerAliases are the trackers that people call by another name than the site of their announce address.
var trackerAliases = map[string]string{
	"t-ru.org":      "rutracker",
	"rutracker.cc":  "rutracker",
	"rutracker.nl":  "rutracker",
	"rutracker.net": "rutracker",
}

// trackerWebsites are the trackers whose announce address is on another site than the one people visit.
var trackerWebsites = map[string]string{
	"t-ru.org":      "rutracker.org",
	"rutracker.cc":  "rutracker.org",
	"rutracker.nl":  "rutracker.org",
	"rutracker.net": "rutracker.org",
}

// twoPartSuffixes are the endings that take two labels, so that "tracker.torrent.eu.org" is "torrent", not "eu".
var twoPartSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "com.au": true, "co.jp": true, "com.br": true, "com.cn": true, "co.nz": true,
	"eu.org": true, "com.ua": true, "org.ua": true, "co.il": true,
}

// splitTracker gives the name of the tracker whose announce address is given and the site it belongs to (the domain, or the
// site people visit when that is another one): both "" when there is no host in it, the site "" for an address.
func splitTracker(announce string) (name, site string) {
	u, err := url.Parse(strings.TrimSpace(announce))
	if err != nil {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", ""
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return host, "" // an address, not a name: it is what it is
	}
	labels := strings.Split(strings.TrimPrefix(host, "www."), ".")
	n := len(labels)
	if n < 2 {
		return host, ""
	}
	site = labels[n-2] + "." + labels[n-1]
	if alias, ok := trackerAliases[site]; ok {
		name = alias
	} else if twoPartSuffixes[site] && n >= 3 {
		name = labels[n-3]
		site = labels[n-3] + "." + site
	} else {
		name = labels[n-2]
	}
	if web, ok := trackerWebsites[site]; ok {
		site = web
	}
	return name, site
}

// TrackerName is the name of the tracker whose announce address is given: "" when there is no host in it.
func TrackerName(announce string) string {
	name, _ := splitTracker(announce)
	return name
}

// mainTracker is the tracker a torrent belongs to: the one its file names as the main one (the "announce" key, kept when the
// file was added); with no such key, or for a magnet link, the first tracker of its list. It gives the name and the site.
func mainTracker(announce string, t *torrent.Torrent) (name, site string) {
	if name, site = splitTracker(announce); name != "" {
		return name, site
	}
	mi := t.Metainfo()
	for _, tier := range mi.UpvertedAnnounceList() {
		for _, u := range tier {
			if name, site = splitTracker(u); name != "" {
				return name, site
			}
		}
	}
	return "", ""
}
