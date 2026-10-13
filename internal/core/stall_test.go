package core

import "time"

// The tests wait up to 30 seconds for a transfer between two clients; a stalled one is nudged well within that.
func init() { stallNudgeAfter = 8 * time.Second } // above the gaps of a transfer capped at 8 KiB/s (status_test.go)
