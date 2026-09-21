package desktop

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestIconIsValidAndDrawn(t *testing.T) {
	ico := Icon()
	if binary.LittleEndian.Uint16(ico[2:]) != 1 || binary.LittleEndian.Uint16(ico[4:]) != 1 {
		t.Fatal("not a single-image icon")
	}
	size := binary.LittleEndian.Uint32(ico[14:])
	off := binary.LittleEndian.Uint32(ico[18:])
	if int(off+size) != len(ico) {
		t.Fatalf("directory entry does not match the data: off=%d size=%d len=%d", off, size, len(ico))
	}
	if len(DIBFromIcon(ico)) != int(size) {
		t.Fatal("DIBFromIcon must return exactly the image data")
	}
}

func TestPNGSizesHaveContent(t *testing.T) {
	for _, n := range []int{16, 32, 48, 256} {
		img, err := png.Decode(bytes.NewReader(PNG(n)))
		if err != nil {
			t.Fatalf("size %d: %v", n, err)
		}
		if b := img.Bounds(); b.Dx() != n || b.Dy() != n {
			t.Fatalf("size %d: got %v", n, b)
		}
		// Corner is transparent (rounded), centre is opaque.
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Errorf("size %d: corner should be transparent", n)
		}
		if _, _, _, a := img.At(n/2, n/2).RGBA(); a == 0 {
			t.Errorf("size %d: centre should be drawn", n)
		}
	}
}
