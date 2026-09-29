package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// canvas is an RGBA pixel buffer with a depth buffer. Pixels are straight
// (not premultiplied) RGBA. On a transparent canvas only opaque geometry is
// drawn, so alpha is 0 or 255 until the canvas is downsampled.
type canvas struct {
	w, h  int
	pix   []uint8 // 4 bytes per pixel
	depth []float32
}

func newCanvas(w, h int, bg color.NRGBA) *canvas {
	c := &canvas{w: w, h: h, pix: make([]uint8, w*h*4), depth: make([]float32, w*h)}
	if bg != (color.NRGBA{}) {
		for i := 0; i < len(c.pix); i += 4 {
			c.pix[i], c.pix[i+1], c.pix[i+2], c.pix[i+3] = bg.R, bg.G, bg.B, bg.A
		}
	}
	for i := range c.depth {
		c.depth[i] = float32(math.Inf(-1))
	}
	return c
}

// tri fills a triangle given in pixel coordinates. z is the depth of each
// vertex (larger is nearer the camera); with useDepth the depth buffer decides
// visibility and is updated, without it the triangle is painted over whatever
// is there. A colour with alpha below 255 is blended over the pixel.
func (c *canvas) tri(x0, y0, z0, x1, y1, z1, x2, y2, z2 float32, col [4]uint8, useDepth bool) {
	area := (x1-x0)*(y2-y0) - (x2-x0)*(y1-y0)
	if area == 0 || area != area {
		return
	}
	minX := int(math.Floor(float64(min(x0, x1, x2) - 0.5)))
	maxX := int(math.Ceil(float64(max(x0, x1, x2) - 0.5)))
	minY := int(math.Floor(float64(min(y0, y1, y2) - 0.5)))
	maxY := int(math.Ceil(float64(max(y0, y1, y2) - 0.5)))
	if maxX < 0 || maxY < 0 || minX >= c.w || minY >= c.h {
		return
	}
	minX, minY = max(minX, 0), max(minY, 0)
	maxX, maxY = min(maxX, c.w-1), min(maxY, c.h-1)
	inv := 1 / area
	// Edge function steps: w0 belongs to vertex 0, and so on; each is
	// normalised so all three are positive inside for either winding.
	dw0x, dw0y := (y1-y2)*inv, (x2-x1)*inv
	dw1x, dw1y := (y2-y0)*inv, (x0-x2)*inv
	px, py := float32(minX)+0.5, float32(minY)+0.5
	rowW0 := ((x1-px)*(y2-py) - (x2-px)*(y1-py)) * inv
	rowW1 := ((x2-px)*(y0-py) - (x0-px)*(y2-py)) * inv
	// Without depth (flat quads and lines) triangles overlap a hair so the
	// seam between the two halves of a quad does not show.
	eps := float32(-1e-6)
	if !useDepth {
		eps = -1e-4
	}
	for y := minY; y <= maxY; y++ {
		w0, w1 := rowW0, rowW1
		idx := y*c.w + minX
		for x := minX; x <= maxX; x++ {
			w2 := 1 - w0 - w1
			if w0 >= eps && w1 >= eps && w2 >= eps {
				if useDepth {
					z := w0*z0 + w1*z1 + w2*z2
					if z > c.depth[idx] {
						c.depth[idx] = z
						o := idx * 4
						c.pix[o], c.pix[o+1], c.pix[o+2], c.pix[o+3] = col[0], col[1], col[2], col[3]
					}
				} else {
					c.blend(idx*4, col)
				}
			}
			w0 += dw0x
			w1 += dw1x
			idx++
		}
		rowW0 += dw0y
		rowW1 += dw1y
	}
}

func (c *canvas) blend(o int, col [4]uint8) {
	a := int(col[3])
	if a == 255 {
		c.pix[o], c.pix[o+1], c.pix[o+2], c.pix[o+3] = col[0], col[1], col[2], 255
		return
	}
	for i := 0; i < 3; i++ {
		c.pix[o+i] = uint8((int(col[i])*a + int(c.pix[o+i])*(255-a)) / 255)
	}
	c.pix[o+3] = uint8(a + int(c.pix[o+3])*(255-a)/255)
}

// quad paints a filled quadrilateral (two triangles) without depth.
func (c *canvas) quad(p [4][2]float32, col [4]uint8) {
	c.tri(p[0][0], p[0][1], 0, p[1][0], p[1][1], 0, p[2][0], p[2][1], 0, col, false)
	c.tri(p[0][0], p[0][1], 0, p[2][0], p[2][1], 0, p[3][0], p[3][1], 0, col, false)
}

// line paints a straight segment of the given width in pixels, without depth.
func (c *canvas) line(x0, y0, x1, y1, width float32, col [4]uint8) {
	dx, dy := x1-x0, y1-y0
	l := float32(math.Hypot(float64(dx), float64(dy)))
	if l == 0 {
		// A dot: a small square.
		h := width / 2
		c.quad([4][2]float32{{x0 - h, y0 - h}, {x0 + h, y0 - h}, {x0 + h, y0 + h}, {x0 - h, y0 + h}}, col)
		return
	}
	nx, ny := -dy/l*width/2, dx/l*width/2
	c.quad([4][2]float32{{x0 + nx, y0 + ny}, {x1 + nx, y1 + ny}, {x1 - nx, y1 - ny}, {x0 - nx, y0 - ny}}, col)
}

// downsample averages factor by factor blocks into one NRGBA pixel, weighting
// colours by alpha so a transparent background does not bleed into edges.
func (c *canvas) downsample(factor int) *image.NRGBA {
	if factor <= 1 {
		out := image.NewNRGBA(image.Rect(0, 0, c.w, c.h))
		copy(out.Pix, c.pix)
		return out
	}
	w, h := c.w/factor, c.h/factor
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	n := factor * factor
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, a int
			for sy := 0; sy < factor; sy++ {
				o := ((y*factor+sy)*c.w + x*factor) * 4
				for sx := 0; sx < factor; sx++ {
					pa := int(c.pix[o+3])
					r += int(c.pix[o]) * pa
					g += int(c.pix[o+1]) * pa
					b += int(c.pix[o+2]) * pa
					a += pa
					o += 4
				}
			}
			d := out.PixOffset(x, y)
			if a == 0 {
				continue
			}
			out.Pix[d] = uint8((r + a/2) / a)
			out.Pix[d+1] = uint8((g + a/2) / a)
			out.Pix[d+2] = uint8((b + a/2) / a)
			out.Pix[d+3] = uint8((a + n/2) / n)
		}
	}
	return out
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func rgba(c color.NRGBA) [4]uint8 { return [4]uint8{c.R, c.G, c.B, c.A} }

// ssFactor is the supersampling factor for an output of size pixels: small
// images get more samples so edges stay smooth.
func ssFactor(size int) int {
	if size < 160 {
		return 3
	}
	return 2
}
