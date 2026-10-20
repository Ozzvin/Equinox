package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestFileArgsTurnsFileURLsIntoPaths(t *testing.T) {
	got := fileArgs([]string{
		"file:///home/me/Downloads/My%20Film.torrent",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		"/tmp/plain.torrent",
	})
	want := []string{
		"/home/me/Downloads/My Film.torrent",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		"/tmp/plain.torrent",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileArgs = %q, want %q", got, want)
	}
}

func TestSecondCopyFindsTheLockTaken(t *testing.T) {
	dir := t.TempDir()
	first, err := lockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockInstance(dir); !errors.Is(err, errRunning) {
		t.Fatalf("a second copy must find the program running, got %v", err)
	}
	first.Close() // the first copy has gone (or crashed): the lock goes with it
	again, err := lockInstance(dir)
	if err != nil {
		t.Fatalf("after the first copy has gone the lock must be free, got %v", err)
	}
	again.Close()
}
