package store

import (
	"testing"
	"time"
)

// A read has nothing to wait for when no write is pending.
func TestDiskPriorityReadIsInstantWithoutWrites(t *testing.T) {
	d := newDiskPriority()
	start := time.Now()
	d.waitForWrites()
	if since := time.Since(start); since > 5*time.Millisecond {
		t.Fatalf("a read with no pending write must not wait, took %v", since)
	}
}

// A read waits for a pending write to end, but is released as soon as it does, well before the cap.
func TestDiskPriorityReadWaitsForAWriteThenProceeds(t *testing.T) {
	d := newDiskPriority()
	d.beginWrite()
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		d.waitForWrites()
		done <- time.Since(start)
	}()
	time.Sleep(10 * time.Millisecond) // let the read start waiting
	d.endWrite()
	waited := <-done
	if waited >= maxReadWait {
		t.Fatalf("the read waited the full cap (%v) instead of being released by endWrite, got %v", maxReadWait, waited)
	}
}

// A read must never wait longer than the cap, even if the write never ends (so seeding is only
// slowed, never stopped).
func TestDiskPriorityReadNeverWaitsPastTheCap(t *testing.T) {
	d := newDiskPriority()
	d.beginWrite()
	defer d.endWrite()
	start := time.Now()
	d.waitForWrites()
	elapsed := time.Since(start)
	if elapsed < maxReadWait {
		t.Fatalf("returned before the cap without the write ending: %v < %v", elapsed, maxReadWait)
	}
	if elapsed > maxReadWait+40*time.Millisecond {
		t.Fatalf("overshot the cap by too much: %v", elapsed)
	}
}

// Several concurrent writes: the gate only opens once every one of them has ended.
func TestDiskPriorityWaitsForAllConcurrentWrites(t *testing.T) {
	d := newDiskPriority()
	d.beginWrite()
	d.beginWrite()
	done := make(chan struct{})
	go func() { d.waitForWrites(); close(done) }()
	time.Sleep(10 * time.Millisecond)
	d.endWrite() // one of two: the read must still be waiting
	select {
	case <-done:
		t.Fatal("the read proceeded while a second write was still pending")
	case <-time.After(5 * time.Millisecond):
	}
	d.endWrite() // the second: now it may proceed
	select {
	case <-done:
	case <-time.After(maxReadWait):
		t.Fatal("the read did not proceed once every write had ended")
	}
}

// Writes themselves are never gated by other writes or by pending reads.
func TestDiskPriorityWritesAreNeverGated(t *testing.T) {
	d := newDiskPriority()
	start := time.Now()
	d.beginWrite()
	d.beginWrite() // a second, concurrent write must not block on the first
	if since := time.Since(start); since > 5*time.Millisecond {
		t.Fatalf("beginWrite must never block, took %v", since)
	}
	d.endWrite()
	d.endWrite()
}
