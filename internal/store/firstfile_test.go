package store

import "testing"

// The first file a block can touch is found by its offset, empty files and the gaps of a v2 torrent included.
func TestFirstFile(t *testing.T) {
	ts := &torrentStore{files: []fileSpan{
		{offset: 0, length: 10},  // 0..10
		{offset: 10, length: 0},  // empty
		{offset: 16, length: 20}, // 16..36, after a gap (v2: each file on a piece boundary)
		{offset: 48, length: 5},  // 48..53
	}}
	for off, want := range map[int64]int{0: 0, 9: 0, 10: 2, 12: 2, 35: 2, 36: 3, 50: 3, 53: 4} {
		if got := ts.firstFile(off); got != want {
			t.Errorf("byte %d: file %d, want %d", off, got, want)
		}
	}
}
