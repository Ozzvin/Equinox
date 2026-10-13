package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartEntryOnAndOff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if AutostartEnabled() {
		t.Fatal("no entry yet, but autostart is reported on")
	}
	if err := SetAutostart(true, true); err != nil {
		t.Fatal(err)
	}
	if !AutostartEnabled() {
		t.Fatal("autostart was turned on, but is reported off")
	}
	p, _ := autostartFile()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), " -hidden\n") || !strings.HasPrefix(string(raw), "[Desktop Entry]\n") {
		t.Errorf("the entry must start the program hidden:\n%s", raw)
	}
	if err := SetAutostart(false, true); err != nil {
		t.Fatal(err)
	}
	if AutostartEnabled() {
		t.Fatal("autostart was turned off, but is reported on")
	}
	if err := SetAutostart(false, true); err != nil {
		t.Fatalf("turning off what is off already must not fail: %v", err)
	}
}

func TestAutostartFollowsXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p, err := autostartFile()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != filepath.Join(dir, "autostart") {
		t.Errorf("entry at %s, want it in %s/autostart", p, dir)
	}
}

func TestQuoteExec(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/equinox":         "/usr/bin/equinox",
		"/home/me/My Apps/equinox": `"/home/me/My Apps/equinox"`,
		`/opt/a"b/equinox`:         `"/opt/a\\"b/equinox"`,
		"/opt/$x/equinox":          `"/opt/\\$x/equinox"`,
	} {
		if got := quoteExec(in); got != want {
			t.Errorf("quoteExec(%q) = %s, want %s", in, got, want)
		}
	}
}
