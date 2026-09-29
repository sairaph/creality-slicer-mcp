package mcpserver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
)

// showPresetValue renders a flattened preset value: a scalar as it is, a
// vector as its entries joined by commas (in brackets when there are several).
func showPresetValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		switch len(x) {
		case 0:
			return "[]"
		case 1:
			return x[0]
		}
		return "[" + strings.Join(x, ", ") + "]"
	}
	return fmt.Sprint(v)
}

// showDefault renders a catalog default value.
func showDefault(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = showDefault(e)
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		// float_or_percent: {"percent": true, "value": 10}
		if val, ok := x["value"]; ok {
			s := showDefault(val)
			if p, _ := x["percent"].(bool); p {
				return s + "%"
			}
			return s
		}
	}
	return fmt.Sprint(v)
}

// areaOf is the GUI path of a setting, "" when it has no GUI line.
func areaOf(o *catalog.Option) string { return o.GUIPath() }

// oneLine flattens text to a single line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// notFound is a not_found error result.
func notFound(message, hint string) *toolResult {
	return render.ErrorResult(render.Error{Code: render.CodeNotFound, Message: shortMessage(message), Hint: hint})
}

// invalidInput is an invalid_input error result.
func invalidInput(message, hint string) *toolResult {
	return render.ErrorResult(render.Error{Code: render.CodeInvalidInput, Message: shortMessage(message), Hint: hint})
}

// unavailable is an unavailable error result for something the install or its
// data lacks; the hint points at get_slicer_status.
func unavailable(what string, err error) *toolResult {
	return render.ErrorResult(render.Error{
		Code:    render.CodeUnavailable,
		Message: shortMessage(fmt.Sprintf("Cannot %s: %v", what, err)),
		Hint:    "Call get_slicer_status to see what is missing; settings, presets and guides need Creality Print to be found.",
	})
}

// pipeSafe keeps a cell of a pipe separated row on its line.
func pipeSafe(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", "/")
}

// capText cuts text after a whole line once it passes limit bytes, and says so.
func capText(s string, limit int, advice string) string {
	if len(s) <= limit {
		return s
	}
	cut := strings.LastIndexByte(s[:limit], '\n')
	if cut < 0 {
		cut = limit
	}
	return s[:cut] + "\n\n[output cut at " + strconv.Itoa(cut) + " of " + strconv.Itoa(len(s)) + " bytes: " + advice + "]"
}

// toolResult is the result type of every handler.
type toolResult = mcp.CallToolResult
