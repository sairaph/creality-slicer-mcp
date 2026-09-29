package mcpserver

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"
)

// minImageEdge is the smallest edge fitPNG scales an image down to.
const minImageEdge = 16

// imageScaleStep is the factor each fitPNG attempt shrinks the source by,
// measured from the original so the steps never compound their blur.
const imageScaleStep = 0.75

// errImageTooLarge means an image would not fit even at its smallest size.
var errImageTooLarge = errors.New("the image does not fit in a reply even at its smallest size")

// imageBudget returns the largest base64-decoded image res can still carry
// without taking the reply past render.MaxBytes, given the text content
// already in res. Every caller that appends an image to a result that may
// already have text uses it, so the budget and the size actually checked
// against never drift apart. Images already in res count too, at their base64
// size, so several pictures together never pass the limit.
func imageBudget(res *mcp.CallToolResult) int {
	text := 0
	for _, c := range res.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			text += len(c.Text)
		case *mcp.ImageContent:
			text += (len(c.Data) + 2) / 3 * 4
		}
	}
	return max((render.MaxBytes-replyMargin-text)/4*3, 0)
}

// imageContent wraps PNG bytes as an image content item; ok is false for an
// empty image. Data is the raw bytes: the SDK base64-encodes them.
func imageContent(data []byte) (mcp.Content, bool) {
	if len(data) == 0 {
		return nil, false
	}
	return &mcp.ImageContent{Data: data, MIMEType: "image/png"}, true
}

// fitPNG returns data unchanged when it is at most budget bytes, otherwise
// the image re-encoded at smaller and smaller sizes (75 percent per step, from
// the original, down to minImageEdge pixels on the short edge) until it fits.
// It never fails an image that fits at some size; errImageTooLarge means none
// did, and any other error means data is not a PNG.
func fitPNG(data []byte, budget int) ([]byte, error) {
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	if len(data) <= budget {
		return data, nil
	}
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	short := min(w, h)
	for scale := imageScaleStep; ; scale *= imageScaleStep {
		nw, nh := max(int(math.Round(float64(w)*scale)), 1), max(int(math.Round(float64(h)*scale)), 1)
		last := false
		if min(nw, nh) < minImageEdge {
			// The smallest size is the last attempt.
			if short <= minImageEdge {
				return nil, errImageTooLarge
			}
			f := float64(minImageEdge) / float64(short)
			nw, nh = max(int(math.Round(float64(w)*f)), 1), max(int(math.Round(float64(h)*f)), 1)
			last = true
		}
		out, err := encodePNG(downscale(src, nw, nh))
		if err != nil {
			return nil, err
		}
		if len(out) <= budget {
			return out, nil
		}
		if last {
			return nil, errImageTooLarge
		}
	}
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// downscale shrinks src to w by h pixels with a box filter: each output pixel
// is the average of the source pixels it covers, alpha-weighted. No cgo, no
// dependency; w and h must not exceed the source size.
func downscale(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA() // alpha-premultiplied, 16 bit
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o+0] = uint8((r / n) >> 8)
			dst.Pix[o+1] = uint8((g / n) >> 8)
			dst.Pix[o+2] = uint8((bl / n) >> 8)
			dst.Pix[o+3] = uint8((a / n) >> 8)
		}
	}
	return dst
}

// imageResult is the reply of a tool whose point is an image: front and body
// as the first content item and the PNG as the second, scaled down when it
// would not fit in a reply (the body then says so). An image that cannot be
// made to fit is an error, not a silent omission.
func imageResult(front any, body string, data []byte) *mcp.CallToolResult {
	out := successResult(front, body)
	fitted, err := fitPNG(data, imageBudget(out))
	if err != nil {
		return render.ErrorResult(render.Error{
			Code:    render.CodeInternal,
			Message: shortMessage(fmt.Sprintf("Failed to attach the image: %v", err)),
			Hint:    "Call the tool again; if it fails again, report this as a bug.",
		})
	}
	if len(fitted) != len(data) {
		if cfg, err := png.DecodeConfig(bytes.NewReader(fitted)); err == nil {
			out = successResult(front, fmt.Sprintf("%s\n\nThe image was reduced to %dx%d pixels to fit in the reply.", body, cfg.Width, cfg.Height))
		}
	}
	img, ok := imageContent(fitted)
	if !ok {
		return render.ErrorResult(render.Error{Code: render.CodeInternal, Message: "Failed to attach the image: it is empty.",
			Hint: "Call the tool again; if it fails again, report this as a bug."})
	}
	out.Content = append(out.Content, img)
	return out
}

// attachImage adds a PNG to res as its last content item, for a tool where the
// image is optional feedback: one that cannot be scaled to fit, or is not a
// PNG, is logged to stderr and left out, so it never turns a successful call
// into an error.
func attachImage(res *mcp.CallToolResult, data []byte, what string) *mcp.CallToolResult {
	fitted, err := fitPNG(data, imageBudget(res))
	if err != nil {
		fmt.Fprintf(os.Stderr, "creality-slicer-mcp: %s left out: %v\n", what, err)
		return res
	}
	if img, ok := imageContent(fitted); ok {
		res.Content = append(res.Content, img)
	}
	return res
}
