package buildinfo

import "testing"

func TestPeerIDPrefix(t *testing.T) {
	for v, want := range map[string]string{"1.0.0": "-EQ1000-", "1.2.3": "-EQ1230-", "dev": "-EQ0000-", "": "-EQ0000-", "12.0.1": "-EQ2010-"} {
		got := peerIDPrefix(v)
		if got != want {
			t.Errorf("%q: got %s, want %s", v, got, want)
		}
		if len(got) != 8 {
			t.Errorf("%q: a peer id prefix is 8 bytes, got %d", v, len(got))
		}
	}
}
