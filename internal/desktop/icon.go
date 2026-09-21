// Package desktop holds the pieces of the Windows application that do not need Win32.
package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math"
)

// Icon returns a 32x32 .ico image: a rounded blue square with a white download arrow.
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
	radius := 7.0 * k
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			cx, cy := float64(x)+0.5, float64(y)+0.5
			half := float64(n) / 2
			dx := math.Max(math.Abs(cx-half)-(half-radius), 0)
			dy := math.Max(math.Abs(cy-half)-(half-radius), 0)
			d := math.Hypot(dx, dy) - radius
			cov := clamp(0.5 - d)
			if cov > 0 {
				set(x, y, 0x2f, 0x6f, 0xed, cov)
			}
		}
	}

	// Arrow: a vertical stem, a chevron and a base line, as thick anti-aliased segments.
	segs := [][4]float64{
		{16, 8, 16, 20}, {10.5, 15, 16, 20.5}, {21.5, 15, 16, 20.5}, {9, 25, 23, 25},
	}
	stroke := 1.6 * k
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			cx, cy := float64(x)+0.5, float64(y)+0.5
			best := math.MaxFloat64
			for _, s := range segs {
				best = math.Min(best, distToSegment(cx, cy, s[0]*k, s[1]*k, s[2]*k, s[3]*k))
			}
			if cov := clamp(stroke + 0.5 - best); cov > 0 {
				set(x, y, 255, 255, 255, cov)
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

func distToSegment(px, py, ax, ay, bx, by float64) float64 {
	vx, vy := bx-ax, by-ay
	l2 := vx*vx + vy*vy
	t := 0.0
	if l2 > 0 {
		t = clamp(((px-ax)*vx + (py-ay)*vy) / l2)
	}
	return math.Hypot(px-(ax+t*vx), py-(ay+t*vy))
}
