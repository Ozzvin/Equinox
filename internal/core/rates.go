package core

import (
	"math"
	"time"
)

// Transfer speeds as other clients show them: not what moved during the last second (a peer asks for
// data in bursts, so that number jumps between zero and a spike) but an average over a few seconds.

const (
	rateWindow   = 5                // seconds averaged for a torrent's speed
	activeHold   = 10 * time.Second // a torrent counts as active this long after its last transfer
	peerTau      = 3 * time.Second  // smoothing time of a peer's speed
	peerForget   = 30 * time.Second // drop the speed of a peer not seen for this long
	rateHistoryN = rateWindow
)

// window keeps the last few one-second samples of a pair of counters (down, up).
type window struct {
	s [rateHistoryN][2]int64
	n int // samples written so far (capped)
	i int // next slot
}

func (w *window) add(down, up int64) {
	w.s[w.i] = [2]int64{down, up}
	w.i = (w.i + 1) % rateHistoryN
	if w.n < rateHistoryN {
		w.n++
	}
}

// mean is the average bytes per second over the samples seen (at most the window).
func (w *window) mean() (down, up int64) {
	if w.n == 0 {
		return 0, 0
	}
	var d, u int64
	for k := 0; k < w.n; k++ {
		d += w.s[k][0]
		u += w.s[k][1]
	}
	return d / int64(w.n), u / int64(w.n)
}

// peerRate smooths one connection's speed between the calls that ask for it.
type peerRate struct {
	down, up     int64 // counters at the last call
	at           time.Time
	dRate, uRate float64
}

// step folds new counter values in and returns the smoothed speeds (bytes/s).
func (p *peerRate) step(now time.Time, down, up int64) (int64, int64) {
	if p.at.IsZero() {
		p.down, p.up, p.at = down, up, now
		return 0, 0
	}
	dt := now.Sub(p.at)
	if dt < 200*time.Millisecond { // asked again at once: keep what we have
		return int64(p.dRate), int64(p.uRate)
	}
	a := 1 - math.Exp(-float64(dt)/float64(peerTau))
	p.dRate += (float64(down-p.down)/dt.Seconds() - p.dRate) * a
	p.uRate += (float64(up-p.up)/dt.Seconds() - p.uRate) * a
	p.down, p.up, p.at = down, up, now
	if p.dRate < 1 {
		p.dRate = 0
	}
	if p.uRate < 1 {
		p.uRate = 0
	}
	return int64(p.dRate), int64(p.uRate)
}

// peerSpeed returns the smoothed speeds of one connection and forgets connections that are gone.
func (m *Manager) peerSpeed(hash, addr string, down, up int64) (int64, int64) {
	now := time.Now()
	key := hash + "|" + addr
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.peerRates[key]
	if p == nil {
		p = &peerRate{}
		m.peerRates[key] = p
	}
	m.peerSeen[key] = now
	for k, at := range m.peerSeen {
		if now.Sub(at) > peerForget {
			delete(m.peerSeen, k)
			delete(m.peerRates, k)
		}
	}
	return p.step(now, down, up)
}
