package mcpserver

// Test-only sample tools that exercise every shared helper (successResult,
// failure, reported, jsonBlock, textBlock, truncateOutput, imageResult,
// attachImage, paginatePage, addTool and the schema helpers) end to end over
// the in-memory MCP client, instead of unit-testing them in isolation. They
// are registered only from tests, through newServer's extra option, so none of
// this ships. Their texts and annotations are added to the real tables for the
// duration of one test only (sampleTools), so the standard text test, which
// lists the real tools, never sees them.

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"
)

type sampleOKInput struct {
	Name  string `json:"name"`
	Loud  *bool  `json:"loud,omitempty"`
	Count *int   `json:"count,omitempty"`
}

type sampleOKFront struct {
	Name  string `yaml:"name"`
	Count int    `yaml:"count"`
}

func sampleOK(_ context.Context, _ *mcp.CallToolRequest, in sampleOKInput) (*mcp.CallToolResult, any, error) {
	count := 1
	if in.Count != nil {
		count = *in.Count
	}
	name := in.Name
	if boolOr(in.Loud, false) {
		name = strings.ToUpper(name)
	}
	body := fmt.Sprintf("Greeted %s %d time(s).\n\n%s\n\n%s", name, count,
		jsonBlock(map[string]int{"count": count}), textBlock("line one\nline two"))
	return successResult(sampleOKFront{Name: name, Count: count}, body), nil, nil
}

type sampleFailInput struct {
	Mode string `json:"mode"`
}

func sampleFail(ctx context.Context, _ *mcp.CallToolRequest, in sampleFailInput) (*mcp.CallToolResult, any, error) {
	const what = "run the sample"
	switch in.Mode {
	case "plain":
		return failure(ctx, what, errors.New("boom"), ""), nil, nil
	case "hint":
		return failure(ctx, what, errors.New("boom"), "Call sample_ok instead."), nil, nil
	case "cancelled":
		return failure(ctx, what, fmt.Errorf("wrapped: %w", context.Canceled), ""), nil, nil
	case "timeout":
		return failure(ctx, what, fmt.Errorf("wrapped: %w", context.DeadlineExceeded), ""), nil, nil
	case "net":
		return failure(ctx, what, &net.OpError{Op: "dial", Err: errors.New("refused")}, ""), nil, nil
	case "typed":
		te := &toolError{e: render.Error{Code: render.CodeConflict, Message: "already final", Hint: "Resolve it."}}
		return failure(ctx, what, fmt.Errorf("wrapped: %w", te), "ignored hint"), nil, nil
	case "reported":
		return reported("slice the plate", "the plate is empty", "Add a model first."), nil, nil
	case "long":
		return failure(ctx, what, errors.New(strings.Repeat("x", 3*maxMessageBytes)), ""), nil, nil
	}
	return failure(ctx, what, errors.New("unknown mode"), ""), nil, nil
}

type sampleImageInput struct {
	Size     int   `json:"size"`
	Noisy    *bool `json:"noisy,omitempty"`
	Optional *bool `json:"optional,omitempty"`
}

type sampleImageFront struct {
	Size int `yaml:"size"`
}

// noisyPNG is a size by size PNG of pseudo-random pixels, which compress
// badly, so a large one cannot fit in a reply without scaling.
func noisyPNG(size int, noisy bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	rnd := rand.New(rand.NewSource(1))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if noisy {
				img.SetNRGBA(x, y, color.NRGBA{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{200, 30, 30, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func sampleImage(_ context.Context, _ *mcp.CallToolRequest, in sampleImageInput) (*mcp.CallToolResult, any, error) {
	data := noisyPNG(in.Size, boolOr(in.Noisy, false))
	front := sampleImageFront{Size: in.Size}
	if boolOr(in.Optional, false) {
		return attachImage(successResult(front, "Optional image."), data, "sample image"), nil, nil
	}
	return imageResult(front, "Explicit image.", data), nil, nil
}

type sampleListInput struct {
	Page *int `json:"page,omitempty"`
}

type sampleListFront struct {
	render.PageMeta `yaml:",inline"`
}

func sampleList(ctx context.Context, _ *mcp.CallToolRequest, in sampleListInput) (*mcp.CallToolResult, any, error) {
	page := 1
	if in.Page != nil {
		page = *in.Page
	}
	// High-entropy records (a repeated character tokenizes to almost nothing),
	// so 60 of them span several pages at paginatePage's token budget.
	rnd := rand.New(rand.NewSource(42))
	records := make([]string, 60)
	for i := range records {
		buf := make([]byte, 300)
		rnd.Read(buf)
		records[i] = fmt.Sprintf("record-%02d: %s", i+1, hex.EncodeToString(buf))
	}
	window, meta, next, err := paginatePage(records, page, func(w []string) (string, error) {
		return strings.Join(w, "\n"), nil
	})
	if err != nil {
		return failure(ctx, "paginate the sample records", err, ""), nil, nil
	}
	body := strings.Join(window, "\n") + next
	return successResult(sampleListFront{PageMeta: meta}, body), nil, nil
}

type sampleOutputInput struct {
	Bytes int `json:"bytes"`
}

func sampleOutput(_ context.Context, _ *mcp.CallToolRequest, in sampleOutputInput) (*mcp.CallToolResult, any, error) {
	return successResult(sampleOKFront{Name: "output"}, textBlock(strings.Repeat("0123456789\n", in.Bytes/11+1)[:in.Bytes])), nil, nil
}

// sampleTools returns the registration func to pass to sessionWith, and puts
// the sample tools' texts and annotations in the real tables until t ends.
func sampleTools(t *testing.T) func(*Server) {
	t.Helper()
	texts := map[string]toolText{
		"sample_ok": {
			Description: "Greet a name; a test-only tool.",
			Params: map[string]string{
				"name":  "who to greet",
				"loud":  "shout the name (default false)",
				"count": "how many times (default 1)",
			},
		},
		"sample_fail": {
			Description: "Fail in a chosen way; a test-only tool.",
			Params:      map[string]string{"mode": "which failure to produce"},
		},
		"sample_image": {
			Description: "Return a generated PNG; a test-only tool.",
			Params: map[string]string{
				"size":     "edge of the square image in pixels",
				"noisy":    "fill with pseudo-random pixels that compress badly (default false)",
				"optional": "attach the image as optional feedback (default false)",
			},
		},
		"sample_list": {
			Description: "List synthetic records a page at a time; a test-only tool.",
			Params:      map[string]string{"page": "1-indexed page number (default 1)"},
		},
		"sample_output": {
			Description: "Print a chosen number of bytes; a test-only tool.",
			Params:      map[string]string{"bytes": "how many bytes of output to produce"},
		},
	}
	annotations := map[string]toolAnnotation{
		"sample_ok": annAdditive, "sample_fail": annChanging, "sample_image": annIdempotent,
		"sample_list": annReadOnly, "sample_output": annReadOnly,
	}
	for name, text := range texts {
		toolTexts[name] = text
		toolAnnotations[name] = annotations[name]
	}
	t.Cleanup(func() {
		for name := range texts {
			delete(toolTexts, name)
			delete(toolAnnotations, name)
		}
	})
	return func(s *Server) {
		addTool(s.mcpServer, "sample_ok", withRange(inputSchema[sampleOKInput](map[string]string{"count": "1"}), 1, 5, "count"), sampleOK)
		addTool(s.mcpServer, "sample_fail", withEnum(inputSchema[sampleFailInput](nil), "mode",
			"plain", "hint", "cancelled", "timeout", "net", "typed", "reported", "long"), sampleFail)
		addTool(s.mcpServer, "sample_image", withRange(inputSchema[sampleImageInput](nil), 1, 4000, "size"), sampleImage)
		addTool(s.mcpServer, "sample_list", inputSchema[sampleListInput](nil), sampleList)
		addTool(s.mcpServer, "sample_output", inputSchema[sampleOutputInput](nil), sampleOutput)
	}
}
