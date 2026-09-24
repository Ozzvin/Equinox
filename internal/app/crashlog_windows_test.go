//go:build windows

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The trace of a panic goes to the process's standard error handle, which a GUI program does not have;
// SetupLog must make it end up in the log file all the same.
func TestPanicTraceEndsUpInTheLog(t *testing.T) {
	if p := os.Getenv("EQUINOX_CRASH_LOG"); p != "" { // this is the child process: log, then crash
		if err := SetupLog(p, 1<<20); err != nil {
			os.Exit(3)
		}
		panic("boom for the log")
	}

	path := filepath.Join(t.TempDir(), "equinox.log")
	cmd := exec.Command(os.Args[0], "-test.run=TestPanicTraceEndsUpInTheLog")
	cmd.Env = append(os.Environ(), "EQUINOX_CRASH_LOG="+path)
	// Like a GUI program, give it no standard error of its own (exec attaches the null device).
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("the child should die of the panic (exit code 2), got %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "panic: boom for the log") || !strings.Contains(string(b), "goroutine") {
		t.Fatalf("the panic did not reach the log, which holds:\n%s", b)
	}
}
