package render

import (
	"image"
	"image/color"
	"strings"
)

// A 5 by 7 pixel bitmap font, enough for legends and scale bars: capitals,
// digits and a little punctuation. Lower case letters draw as capitals. No font
// file and no dependency.
//
// Each glyph is seven rows of five bits, most significant bit on the left.
var glyphs = map[rune][7]uint8{
	' ': {},
	'0': {0b01110, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b01110},
	'1': {0b00100, 0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'2': {0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b01000, 0b11111},
	'3': {0b11110, 0b00001, 0b00001, 0b01110, 0b00001, 0b00001, 0b11110},
	'4': {0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010},
	'5': {0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110},
	'6': {0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110},
	'7': {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000},
	'8': {0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110},
	'9': {0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100},
	'A': {0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'B': {0b11110, 0b10001, 0b10001, 0b11110, 0b10001, 0b10001, 0b11110},
	'C': {0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110},
	'D': {0b11110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b11110},
	'E': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111},
	'F': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b10000},
	'G': {0b01110, 0b10001, 0b10000, 0b10111, 0b10001, 0b10001, 0b01111},
	'H': {0b10001, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'I': {0b01110, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'J': {0b00111, 0b00010, 0b00010, 0b00010, 0b00010, 0b10010, 0b01100},
	'K': {0b10001, 0b10010, 0b10100, 0b11000, 0b10100, 0b10010, 0b10001},
	'L': {0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111},
	'M': {0b10001, 0b11011, 0b10101, 0b10101, 0b10001, 0b10001, 0b10001},
	'N': {0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001, 0b10001},
	'O': {0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'P': {0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000},
	'Q': {0b01110, 0b10001, 0b10001, 0b10001, 0b10101, 0b10010, 0b01101},
	'R': {0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001},
	'S': {0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110},
	'T': {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100},
	'U': {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'V': {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100},
	'W': {0b10001, 0b10001, 0b10001, 0b10101, 0b10101, 0b10101, 0b01010},
	'X': {0b10001, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001, 0b10001},
	'Y': {0b10001, 0b10001, 0b01010, 0b00100, 0b00100, 0b00100, 0b00100},
	'Z': {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b10000, 0b11111},
	'.': {0, 0, 0, 0, 0, 0b00100, 0b00100},
	',': {0, 0, 0, 0, 0b00100, 0b00100, 0b01000},
	':': {0, 0b00100, 0b00100, 0, 0b00100, 0b00100, 0},
	'-': {0, 0, 0, 0b11111, 0, 0, 0},
	'+': {0, 0b00100, 0b00100, 0b11111, 0b00100, 0b00100, 0},
	'=': {0, 0, 0b11111, 0, 0b11111, 0, 0},
	'_': {0, 0, 0, 0, 0, 0, 0b11111},
	'/': {0b00001, 0b00001, 0b00010, 0b00100, 0b01000, 0b10000, 0b10000},
	'(': {0b00010, 0b00100, 0b01000, 0b01000, 0b01000, 0b00100, 0b00010},
	')': {0b01000, 0b00100, 0b00010, 0b00010, 0b00010, 0b00100, 0b01000},
	'%': {0b11001, 0b11010, 0b00010, 0b00100, 0b01000, 0b01011, 0b10011},
	'#': {0b01010, 0b01010, 0b11111, 0b01010, 0b11111, 0b01010, 0b01010},
	'?': {0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0, 0b00100},
}

// glyphWidth and glyphHeight are the font's cell size at scale 1, glyphAdvance
// the distance between characters.
const (
	glyphWidth   = 5
	glyphHeight  = 7
	glyphAdvance = 6
)

// textWidth is the width in pixels of s at the scale.
func textWidth(s string, scale int) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	return (n*glyphAdvance - 1) * scale
}

// drawText paints s with its top left at x, y, each font pixel scale by scale
// device pixels. The background is left as it is.
func drawText(img *image.NRGBA, x, y, scale int, s string, col color.NRGBA) {
	for _, r := range strings.ToUpper(s) {
		g, ok := glyphs[r]
		if !ok {
			g = glyphs['?']
		}
		for row := 0; row < glyphHeight; row++ {
			for colIdx := 0; colIdx < glyphWidth; colIdx++ {
				if g[row]&(1<<(glyphWidth-1-colIdx)) == 0 {
					continue
				}
				fillRect(img, x+colIdx*scale, y+row*scale, scale, scale, col)
			}
		}
		x += glyphAdvance * scale
	}
}

// fillRect paints an opaque rectangle, clipped to the image.
func fillRect(img *image.NRGBA, x, y, w, h int, col color.NRGBA) {
	b := img.Bounds()
	for yy := max(y, b.Min.Y); yy < min(y+h, b.Max.Y); yy++ {
		for xx := max(x, b.Min.X); xx < min(x+w, b.Max.X); xx++ {
			img.SetNRGBA(xx, yy, col)
		}
	}
}
