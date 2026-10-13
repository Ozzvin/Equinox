package core

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
)

// waitForTransfer is waitFor for a transfer between two clients of one test. On a timeout it says where things stood
// on both ends, which a bare "condition not met" does not: these tests now and then ran out of time on the Windows
// runner, and the message alone could not tell a connection that never came up from a transfer that was just slow.
func waitForTransfer(t *testing.T, a *Manager, hashA string, b *Manager, hashB string, cond func() bool) {
	t.Helper()
	start := time.Now()
	for i := 0; i < 600; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met in %v\n  seed:  %s\n  leech: %s\n--- seed engine:\n%s\n--- leech engine:\n%s\n--- goroutines serving or reading:\n%s",
		time.Since(start).Round(time.Millisecond), swarmState(a, hashA), swarmState(b, hashB), engineStatus(a), engineStatus(b), servingStacks())
}

// servingStacks are the stacks of the goroutines that serve a peer's request or read and write the storage: where a
// request that was taken but never answered is stuck.
func servingStacks() string {
	buf := make([]byte, 8<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		for _, mark := range []string{"peerRequestServer", "servePeerRequest", "internal/store.", "(*PeerConn).upload", "messageWriter"} {
			if strings.Contains(g, mark) {
				out = append(out, g)
				break
			}
		}
	}
	return strings.Join(out, "\n\n")
}

// swarmState is one torrent of a test client, as this program and as the engine see it.
func swarmState(m *Manager, hash string) string {
	tt, err := m.get(hash)
	if err != nil {
		return err.Error()
	}
	st := tt.Stats()
	s, _ := statusOf(m, hash)
	m.mu.Lock()
	var watch string
	if w := m.stalls[tt.InfoHash()]; w != nil {
		watch = fmt.Sprintf("stall watch: read %d, quiet for %v, nudged %v ago; may download %v", w.read, time.Since(w.since).Round(time.Millisecond), time.Since(w.nudged).Round(time.Millisecond), m.mayDownload(tt.InfoHash()))
	} else {
		watch = fmt.Sprintf("no stall watch; may download %v", m.mayDownload(tt.InfoHash()))
	}
	m.mu.Unlock()
	watch += fmt.Sprintf("; engine requests outstanding (all connections, incl. closed ones): %d", engineRequests(tt))
	return watch + "\n         " + fmt.Sprintf("%+v\n         engine: total %d, pending %d, half-open %d, active %d, seeders %d, pieces %d/%d, port %d",
		s, st.TotalPeers, st.PendingPeers, st.HalfOpenPeers, st.ActivePeers, st.ConnectedSeeders, st.PiecesComplete, tt.NumPieces(), m.Port())
}

// engineStatus is the engine's own report on a client: its torrents and every connection with its flags (who chokes
// whom, who is interested), less the DHT part.
func engineStatus(m *Manager) string {
	var buf bytes.Buffer
	m.cl.WriteStatus(&buf)
	var out []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "DHT") || strings.HasPrefix(strings.TrimSpace(l), "Bucket") {
			continue
		}
		out = append(out, "    "+l)
	}
	return strings.Join(out, "\n")
}

// describeDiff says where and how two contents differ: the first and last differing offsets, how many bytes, and
// whether what was read there is zeros (never written) or something else (written wrong).
func describeDiff(got, want []byte) string {
	if len(got) != len(want) {
		return fmt.Sprintf("length %d, want %d", len(got), len(want))
	}
	first, last, n, zeros := -1, -1, 0, 0
	for i := range got {
		if got[i] != want[i] {
			if first < 0 {
				first = i
			}
			last = i
			n++
			if got[i] == 0 {
				zeros++
			}
		}
	}
	if n == 0 {
		return "equal"
	}
	return fmt.Sprintf("%d bytes differ, offsets %d..%d, %d of them zeros", n, first, last, zeros)
}

// engineRequests is how many chunk requests the engine counts as in flight for the torrent, whichever connection
// they were made on: its own map (Torrent.requestState), read by reflection, for there is no other way to see it.
// More here than on the live connections means requests held by a connection that is gone.
func engineRequests(t *torrent.Torrent) int {
	f := reflect.ValueOf(t).Elem().FieldByName("requestState")
	if !f.IsValid() {
		return -1
	}
	return f.Len()
}
