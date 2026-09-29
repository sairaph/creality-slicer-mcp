package mcpserver

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"
)

func TestImageBudgetShrinksWithTheText(t *testing.T) {
	empty := &mcp.CallToolResult{}
	withText := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(make([]byte, 100<<10))}}}
	if got, want := imageBudget(empty), (render.MaxBytes-replyMargin)/4*3; got != want {
		t.Errorf("empty budget = %d, want %d", got, want)
	}
	if imageBudget(withText) >= imageBudget(empty)-(100<<10)/2 {
		t.Errorf("the budget did not shrink with 100 KiB of text: %d vs %d", imageBudget(withText), imageBudget(empty))
	}
	huge := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(make([]byte, render.MaxBytes))}}}
	if imageBudget(huge) != 0 {
		t.Errorf("budget with a full reply = %d, want 0", imageBudget(huge))
	}
}

func TestImageContent(t *testing.T) {
	if _, ok := imageContent(nil); ok {
		t.Error("an empty image was accepted")
	}
	c, ok := imageContent([]byte("x"))
	ic, isImage := c.(*mcp.ImageContent)
	if !ok || !isImage || ic.MIMEType != "image/png" || string(ic.Data) != "x" {
		t.Errorf("imageContent = %+v, %v", c, ok)
	}
}

func TestFitPNG(t *testing.T) {
	data := noisyPNG(400, true)

	same, err := fitPNG(data, len(data))
	if err != nil || !bytes.Equal(same, data) {
		t.Fatalf("an image within budget was changed: %v", err)
	}

	budget := len(data) / 4
	fitted, err := fitPNG(data, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(fitted) > budget {
		t.Errorf("fitted image is %d bytes, over the budget %d", len(fitted), budget)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(fitted))
	if err != nil || cfg.Width >= 400 || cfg.Width != cfg.Height || cfg.Width < minImageEdge {
		t.Errorf("fitted size = %+v, %v", cfg, err)
	}

	// Nothing fits in 10 bytes: the smallest size is tried and then it fails.
	if _, err := fitPNG(data, 10); !errors.Is(err, errImageTooLarge) {
		t.Errorf("err = %v, want errImageTooLarge", err)
	}
	// Not a PNG.
	if _, err := fitPNG([]byte("not a png"), 3); err == nil || errors.Is(err, errImageTooLarge) {
		t.Errorf("err = %v, want a decode error", err)
	}
	// Already at the smallest size and still over budget.
	tiny := noisyPNG(minImageEdge, true)
	if _, err := fitPNG(tiny, 10); !errors.Is(err, errImageTooLarge) {
		t.Errorf("tiny image: err = %v, want errImageTooLarge", err)
	}
}

func TestDownscaleAveragesBoxesAndKeepsAlpha(t *testing.T) {
	// 4x2: left half black, right half white, all opaque.
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			v := uint8(0)
			if x >= 2 {
				v = 255
			}
			src.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	dst := downscale(src, 2, 1)
	if dst.Bounds().Dx() != 2 || dst.Bounds().Dy() != 1 {
		t.Fatalf("size = %v", dst.Bounds())
	}
	if l, r := dst.RGBAAt(0, 0), dst.RGBAAt(1, 0); l != (color.RGBA{0, 0, 0, 255}) || r != (color.RGBA{254, 254, 254, 255}) && r != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("pixels = %v %v", l, r)
	}

	// Mixed 1 black and 1 white pixel average to mid grey.
	mix := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	mix.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 255})
	mix.SetNRGBA(1, 0, color.NRGBA{255, 255, 255, 255})
	if p := downscale(mix, 1, 1).RGBAAt(0, 0); p.R < 126 || p.R > 128 || p.A != 255 {
		t.Errorf("mixed pixel = %v, want mid grey", p)
	}

	// A fully transparent image stays transparent.
	clear := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if p := downscale(clear, 2, 2).RGBAAt(1, 1); p.A != 0 {
		t.Errorf("transparent pixel became %v", p)
	}
}

func TestFitPNGRefusesANonPNGEvenWithinBudget(t *testing.T) {
	if _, err := fitPNG([]byte("not a png"), 1<<20); err == nil || errors.Is(err, errImageTooLarge) {
		t.Errorf("err = %v, want a decode error", err)
	}
}
