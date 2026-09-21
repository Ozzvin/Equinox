// Package buildinfo holds the version of the program. The build scripts set Version from the VERSION
// file with -ldflags "-X github.com/Ozzvin/equinox/internal/buildinfo.Version=1.0.0".
package buildinfo

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is the release number, "dev" for a build made without the scripts.
var Version = "dev"

// Name is how other clients see the program.
const Name = "Equinox"

// ClientVersion is the text sent to peers in the extended handshake ("Equinox 1.0.0").
func ClientVersion() string { return Name + " " + Version }

// PeerIDPrefix is the start of the peer id in the usual "-XXNNNN-" form ("-EQ1000-" for 1.0.0).
func PeerIDPrefix() string { return peerIDPrefix(Version) }

func peerIDPrefix(v string) string {
	var digits [4]int
	for i, part := range strings.SplitN(v, ".", 4) {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			break
		}
		digits[i] = n % 10
	}
	return fmt.Sprintf("-EQ%d%d%d%d-", digits[0], digits[1], digits[2], digits[3])
}
