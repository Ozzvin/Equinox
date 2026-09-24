//go:build !windows

package app

import "os"

// Where the process has a real standard error, redirecting it is the job of whoever started it.
func setRuntimeStderr(*os.File) {}
