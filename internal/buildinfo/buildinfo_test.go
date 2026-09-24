package buildinfo

import "testing"

func TestIsRelease(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	for v, want := range map[string]bool{
		"1.0.16": true, "1.0": true, "12.0.1": true,
		"dev": false, "": false, "beta-1.0.17": false, "1.0.17-beta": false, "1.0.x": false, "1": false,
	} {
		Version = v
		if got := IsRelease(); got != want {
			t.Errorf("IsRelease() for %q = %v, want %v", v, got, want)
		}
	}
}

func TestPeerIDPrefix(t *testing.T) {
	for v, want := range map[string]string{"1.0.0": "-EQ1000-", "1.2.3": "-EQ1230-", "dev": "-EQ0000-", "": "-EQ0000-", "12.0.1": "-EQ2010-", "beta-1.0.17": "-EQ1070-"} {
		got := peerIDPrefix(v)
		if got != want {
			t.Errorf("%q: got %s, want %s", v, got, want)
		}
		if len(got) != 8 {
			t.Errorf("%q: a peer id prefix is 8 bytes, got %d", v, len(got))
		}
	}
}
