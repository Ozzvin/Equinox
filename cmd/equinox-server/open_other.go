//go:build !windows

package main

import "flag"

// openFlags registers -open and -allow-host: the server can run behind a proxy that signs users in (Umbrel,
// Docker) with no personal key of its own, listening on any address. Windows has no such use for it (see
// open_windows.go) and does not carry the flag at all, so it cannot be turned on there by mistake.
func openFlags() (open *bool, allowHosts *string) {
	open = flag.Bool("open", false, "run behind a proxy that signs users in (Umbrel): listen on any address and ask for no key. Never expose this port to a network that is not trusted")
	allowHosts = flag.String("allow-host", "", "with -open: extra host names to accept, comma separated (IP addresses, short names and .local names always pass)")
	return open, allowHosts
}
