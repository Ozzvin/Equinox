// Package desktop holds the pieces of the Windows application that do not need Win32.
package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math"
)

// Icon returns a 32x32 .ico image: equinox, drawn as a circle split into a sunlit half and a
// night half, tilted by Earth's axial tilt (23.4°), with a sun and a moon set into the two halves.
// It is drawn in code so the executable needs no resource files.
func Icon() []byte {
	const n = 32
	px := render(n)
	return icoFromBGRA(px, n)
}

// PNG returns the same picture as a PNG of the given size (for the executable's resources).
func PNG(n int) []byte {
	px := render(n)
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for i := 0; i < n*n; i++ { // BGRA (straight alpha) -> NRGBA
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = px[i*4+2], px[i*4+1], px[i*4], px[i*4+3]
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// render draws the icon at n x n as top-down BGRA pixels. All coordinates are designed
// on a 32-unit grid and scaled.
func render(n int) []byte {
	k := float64(n) / 32
	// BGRA pixels, top-down.
	px := make([]byte, n*n*4)
	set := func(x, y int, r, g, b byte, a float64) {
		i := (y*n + x) * 4
		// Alpha-blend over what is already there.
		oa := float64(px[i+3]) / 255
		na := a + oa*(1-a)
		if na == 0 {
			return
		}
		blend := func(nc byte, oc byte) byte {
			return byte((float64(nc)*a + float64(oc)*oa*(1-a)) / na)
		}
		px[i], px[i+1], px[i+2], px[i+3] = blend(b, px[i]), blend(g, px[i+1]), blend(r, px[i+2]), byte(na*255)
	}

	// Rounded square background, anti-aliased with a 1px soft edge.
	radius := 6.5 * k
	bg := [3]byte{0x1b, 0x21, 0x30}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			cx, cy := float64(x)+0.5, float64(y)+0.5
			half := float64(n) / 2
			dx := math.Max(math.Abs(cx-half)-(half-radius), 0)
			dy := math.Max(math.Abs(cy-half)-(half-radius), 0)
			d := math.Hypot(dx, dy) - radius
			if cov := clamp(0.5 - d); cov > 0 {
				set(x, y, bg[0], bg[1], bg[2], cov)
			}
		}
	}

	// The equinox: a circle split into a sunlit half (orange) and a night half (white), a hairline
	// gap between them, and a sun and a moon set into their own half — tilted 23.4°, Earth's own
	// axial tilt, the reason an equinox happens at all. Coordinates are in the disc's own, unrotated
	// frame: a pixel is tested by rotating it back into that frame.
	const (
		diskR   = 10.75
		gapLo   = 0.0 // the sunlit half ends here...
		gapHi   = 1.0 // ...and the night half starts here: a 1-unit gap shows the background between them
		bodyR   = 1.625
		sunY    = -6.5
		moonY   = 7.5
		tiltDeg = 23.4
	)
	sun := [3]byte{0xf0, 0xa6, 0x3c}
	moon := [3]byte{0xf4, 0xf5, 0xf8}
	sinT, cosT := math.Sincos(tiltDeg * math.Pi / 180)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := (float64(x)+0.5)/k-16, (float64(y)+0.5)/k-16
			// Rotate the pixel back into the disk's own frame (the inverse of the +23.4° tilt
			// applied when the artwork was drawn, so the disk itself appears tilted).
			lx, ly := dx*cosT-dy*sinT, dx*sinT+dy*cosT
			distEdge := math.Hypot(lx, ly) - diskR
			circleCov := clamp(0.5 - distEdge)
			if circleCov <= 0 {
				continue
			}
			if cov := math.Min(circleCov, clamp(0.5-ly)); cov > 0 { // the sunlit half, ly <= gapLo
				set(x, y, sun[0], sun[1], sun[2], cov)
			}
			if cov := math.Min(circleCov, clamp(ly-gapHi+0.5)); cov > 0 { // the night half, ly >= gapHi
				set(x, y, moon[0], moon[1], moon[2], cov)
			}
			if cov := clamp(0.5 - (math.Hypot(lx, ly-sunY) - bodyR)); cov > 0 { // the sun
				set(x, y, bg[0], bg[1], bg[2], cov)
			}
			if cov := clamp(0.5 - (math.Hypot(lx, ly-moonY) - bodyR)); cov > 0 { // the moon
				set(x, y, bg[0], bg[1], bg[2], cov)
			}
		}
	}

	return px
}

// icoFromBGRA wraps top-down BGRA pixels into an .ico with one BMP (DIB) image.
func icoFromBGRA(px []byte, n int) []byte {
	// header, XOR bitmap (bottom-up BGRA), AND mask.
	var dib bytes.Buffer
	le := func(v any) { _ = binary.Write(&dib, binary.LittleEndian, v) }
	le(uint32(40)) // BITMAPINFOHEADER size
	le(int32(n))
	le(int32(n * 2)) // height counts the AND mask too
	le(uint16(1))    // planes
	le(uint16(32))   // bits per pixel
	le(uint32(0))    // BI_RGB
	le(uint32(n * n * 4))
	le(int32(0))
	le(int32(0))
	le(uint32(0))
	le(uint32(0))
	for y := n - 1; y >= 0; y-- {
		dib.Write(px[y*n*4 : (y+1)*n*4])
	}
	dib.Write(make([]byte, ((n+31)/32*4)*n)) // AND mask: all zero, alpha channel decides

	var ico bytes.Buffer
	w := func(v any) { _ = binary.Write(&ico, binary.LittleEndian, v) }
	w(uint16(0)) // reserved
	w(uint16(1)) // type: icon
	w(uint16(1)) // one image
	ico.WriteByte(byte(n))
	ico.WriteByte(byte(n))
	ico.WriteByte(0) // palette
	ico.WriteByte(0)
	w(uint16(1))  // planes
	w(uint16(32)) // bpp
	w(uint32(dib.Len()))
	w(uint32(22)) // data offset
	ico.Write(dib.Bytes())
	return ico.Bytes()
}

// DIBFromIcon returns the image data of a single-image .ico (without the ICO header),
// which is what CreateIconFromResourceEx expects.
func DIBFromIcon(ico []byte) []byte {
	if len(ico) <= 22 {
		return nil
	}
	return ico[22:]
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }
