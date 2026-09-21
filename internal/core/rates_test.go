package core

import (
	"testing"
	"time"
)

func TestWindowAveragesBursts(t *testing.T) {
	var w window
	for _, up := range []int64{0, 0, 1000, 0, 0} { // one burst in five seconds
		w.add(0, up)
	}
	if _, up := w.mean(); up != 200 {
		t.Fatalf("mean up = %d, want 200", up)
	}
	for i := 0; i < 5; i++ {
		w.add(0, 0)
	}
	if _, up := w.mean(); up != 0 {
		t.Fatalf("after five quiet seconds the speed must be 0, got %d", up)
	}
}

func TestPeerRateDoesNotFlicker(t *testing.T) {
	var p peerRate
	now := time.Now()
	p.step(now, 0, 0)
	var total int64
	rates := []int64{}
	for i := 1; i <= 10; i++ { // bursts: data moves every other second
		if i%2 == 0 {
			total += 200 << 10
		}
		_, up := p.step(now.Add(time.Duration(i)*time.Second), 0, total)
		rates = append(rates, up)
	}
	for _, r := range rates[3:] {
		if r == 0 {
			t.Fatalf("the smoothed speed dropped to 0 in the middle of a transfer: %v", rates)
		}
	}
}
