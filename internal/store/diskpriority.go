package store

import (
	"sync"
	"time"
)

// diskPriority gives writes (data landing from a download) precedence over reads (data being
// served to peers while seeding) on the same disk. On a spinning disk the two compete for the same
// head, and a read that keeps winning that race stalls the download it belongs to; a write that
// keeps winning only makes an upload a little slower, which peers tolerate far better. A read waits
// for pending writes to clear, but never longer than maxReadWait, so seeding is slowed, not stopped.
//
// One diskPriority is shared by every torrent under a Storage, because the contention it manages is
// physical (one disk), not per-torrent.
type diskPriority struct {
	mu      sync.Mutex
	writing int
	clear   chan struct{} // closed while writing == 0; replaced by a fresh, open one when it goes above 0
}

func newDiskPriority() *diskPriority {
	d := &diskPriority{clear: make(chan struct{})}
	close(d.clear) // starts with nothing to wait for
	return d
}

// maxReadWait bounds how long a read gives pending writes a head start. Long enough to matter on a
// slow disk, short enough that seeding stays responsive.
const maxReadWait = 40 * time.Millisecond

// beginWrite marks a write as under way; the matching endWrite must run once it is done, so callers
// use "d.beginWrite(); defer d.endWrite()".
func (d *diskPriority) beginWrite() {
	if d == nil {
		return // a zero-value Storage (only used directly in tests) has no scheduling
	}
	d.mu.Lock()
	if d.writing == 0 {
		d.clear = make(chan struct{})
	}
	d.writing++
	d.mu.Unlock()
}

func (d *diskPriority) endWrite() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.writing--
	if d.writing == 0 {
		close(d.clear)
	}
	d.mu.Unlock()
}

// waitForWrites lets a read proceed once no write is pending, or after maxReadWait, whichever
// comes first.
func (d *diskPriority) waitForWrites() {
	if d == nil {
		return
	}
	d.mu.Lock()
	ch := d.clear
	d.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(maxReadWait):
	}
}
