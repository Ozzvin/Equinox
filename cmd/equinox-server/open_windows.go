//go:build windows

package main

// openFlags: on Windows there is no supported way to run this server behind a signing-in proxy, and -open turns
// off the personal key and lets it listen on any address — together with /api/fs, /api/create and /stream (any
// file the program can read, chosen by whoever can reach the port) that is not something to have within a flag
// of accidentally turning on here. The flag does not exist on this build at all, not merely default off: passing
// -open to it fails to parse, the same as any other unknown flag.
func openFlags() (open *bool, allowHosts *string) {
	off, none := false, ""
	return &off, &none
}
