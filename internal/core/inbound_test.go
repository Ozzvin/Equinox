package core

import (
	"testing"

	"github.com/anacrolix/torrent"
)

// The connection direction is read from an unexported engine field. If a library update
// renames it, this fails immediately instead of silently disabling reachability detection.
func TestEngineExposesConnectionDirection(t *testing.T) {
	out, ok := isOutgoing(&torrent.PeerConn{})
	if !ok || out {
		t.Fatalf("isOutgoing on a fresh PeerConn: outgoing=%v ok=%v", out, ok)
	}
}
