package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogRotatesInsteadOfGrowing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "equinox.log")
	l, err := openRotating(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes
	for i := 0; i < 7; i++ {
		if _, err := l.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	cur, _ := os.ReadFile(p)
	old, err := os.ReadFile(p + ".old")
	if err != nil {
		t.Fatalf("the full log must be kept as .old: %v", err)
	}
	if len(cur) > 100 || len(old) > 100 {
		t.Fatalf("no file may exceed the cap: current %d, old %d", len(cur), len(old))
	}
	if len(cur) == 0 || len(old) == 0 {
		t.Fatalf("both files must hold data: %d %d", len(cur), len(old))
	}
	l.f.Close()

	// A log that is already big when the program starts is rotated on the first write.
	if err := os.WriteFile(p, []byte(strings.Repeat("y", 95)), 0o644); err != nil {
		t.Fatal(err)
	}
	l2, err := openRotating(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { l2.f.Close() }() // the file changes when it rotates, so look it up at the end
	_, _ = l2.Write([]byte(line))
	if b, _ := os.ReadFile(p); string(b) != line {
		t.Fatalf("the new file must start with the new message, got %d bytes", len(b))
	}
}
