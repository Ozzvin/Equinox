package core

import (
	"fmt"
	"os"
	"runtime/debug"
)

// survive, deferred first thing in a goroutine of ours that the program can live without (an icon, a check, the
// metadata of a new torrent), writes a panic in it down with its stack instead of letting it take the whole program
// down. The engine's own goroutines cannot be covered this way.
func survive(what string) {
	if r := recover(); r != nil {
		survivePanic(what, r)
	}
}

// survivePanic writes down a panic recovered by a goroutine that does more than survive about it.
func survivePanic(what string, r any) {
	fmt.Fprintf(os.Stderr, "panic in %s: %v\n%s", what, r, debug.Stack())
}
