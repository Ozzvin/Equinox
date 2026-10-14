package main

import (
	"errors"
	"testing"
)

// A second server on the same state directory finds the lock taken; once the first is gone, the lock is free again.
func TestOneServerPerStateDirectory(t *testing.T) {
	dir := t.TempDir()
	first, err := lockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockInstance(dir); !errors.Is(err, errRunning) {
		t.Fatalf("a second server must find the first one: %v", err)
	}
	first.Close()
	again, err := lockInstance(dir)
	if err != nil {
		t.Fatalf("the lock must be free once the first server is gone: %v", err)
	}
	again.Close()
}
