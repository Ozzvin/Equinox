package app

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
)

// TestStateDirFallsBackWhenExeDirIsNotWritable mirrors a standard user running a copy
// installed for all users in Program Files: that directory denies them write access, so the
// state dir must fall back to a per-user folder instead of failing outright.
func TestStateDirFallsBackWhenExeDirIsNotWritable(t *testing.T) {
	dir := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Skip("cannot determine the current user:", err)
	}
	deny := exec.Command("icacls", dir, "/deny", u.Username+":(OI)(CI)W")
	if out, err := deny.CombinedOutput(); err != nil {
		t.Skipf("cannot set up a read-only directory (icacls): %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("icacls", dir, "/reset", "/T").Run()
	})

	tmpLocalAppData := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmpLocalAppData)

	got := stateDirUnder(dir)
	want := filepath.Join(tmpLocalAppData, "Equinox")
	if got != want {
		t.Fatalf("stateDirUnder(%q) = %q, want the per-user fallback %q", dir, got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "data")); err == nil {
		t.Fatalf("the denied folder should not have received a data directory")
	}
}

// TestStateDirUsesExeDirWhenWritable is the ordinary case: a per-user install or a portable
// copy, both writable, keep their data next to the executable.
func TestStateDirUsesExeDirWhenWritable(t *testing.T) {
	dir := t.TempDir()
	got := stateDirUnder(dir)
	want := filepath.Join(dir, "data")
	if got != want {
		t.Fatalf("stateDirUnder(%q) = %q, want %q", dir, got, want)
	}
}
