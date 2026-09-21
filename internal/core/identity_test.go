package core

import (
	"strings"
	"testing"

	"github.com/Ozzvin/equinox/internal/buildinfo"
)

// Other clients must see the program's own name, not the engine's module path.
func TestPeerIdentityIsOurOwn(t *testing.T) {
	m := newManager(t, t.TempDir(), nil)
	id := m.cl.PeerID()
	if !strings.HasPrefix(string(id[:]), buildinfo.PeerIDPrefix()) {
		t.Fatalf("peer id %q does not start with %q", id[:8], buildinfo.PeerIDPrefix())
	}
	if !strings.HasPrefix(string(id[:]), "-EQ") {
		t.Fatalf("peer id %q is not ours", id[:8])
	}
}
