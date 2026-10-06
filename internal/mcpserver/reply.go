package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// codeSlicer marks a failure Creality Print reported for a well-formed request.
const codeSlicer = "slicer_error"

// maxOutputBytes bounds the output one reply carries (printed output, logs,
// listings), so the reply stays under render.MaxBytes with room for its front
// matter, hints and an image.
const maxOutputBytes = 512 << 10

// replyMargin is room kept in a reply for its JSON framing and notices.
const replyMargin = 32 << 10

// maxMessageBytes bounds an error message. The message says what failed;
// output that explains it goes in the reply body instead.
const maxMessageBytes = 2 << 10

// Hints for the error classes failure reports.
const (
	cancelledHint = "The call was cancelled before it finished; send it again to retry."
	timeoutHint   = "The operation did not finish in time. Call the tool again; for slow work pass a larger timeout where the tool takes one."
	unavailHint   = "Something this call needs did not answer. Run `" + domain.BinaryName + " doctor` to check the installation, then call the tool again."
	internalHint  = "This is not caused by the arguments. Run `" + domain.BinaryName + " doctor` to check the installation, then call the tool again."
)

// toolError is an error that already carries its structured form.
type toolError struct{ e render.Error }

func (t *toolError) Error() string { return t.e.Message }

// failure builds an error result for err, which occurred while doing what.
// hint, when not empty, replaces the hint of err's class. Errors are returned
// as results, never as Go errors, so the model always sees the hint.
func failure(_ context.Context, what string, err error, hint string) *mcp.CallToolResult {
	return render.ErrorResult(failureError(what, err, hint))
}

// failureError is the error failure reports.
func failureError(what string, err error, hint string) render.Error {
	var te *toolError
	if errors.As(err, &te) {
		return te.e
	}
	e := render.Error{Code: render.CodeInternal, Message: shortMessage(fmt.Sprintf("Failed to %s: %v", what, err)), Hint: internalHint}
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		e.Code = render.CodeUnavailable
		e.Message = fmt.Sprintf("Failed to %s: the request was cancelled", what)
		e.Hint = cancelledHint
	case isTimeout(err):
		e.Code = render.CodeUnavailable
		e.Hint = timeoutHint
	case errors.As(err, &netErr):
		e.Code = render.CodeUnavailable
		e.Hint = unavailHint
	}
	if hint != "" {
		e.Hint = hint
	}
	return e
}

// isTimeout reports whether err is an operation that did not finish in time.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()
}

// reported builds an error result for a failure Creality Print reported for a
// well-formed request; msg is what it said. Its own output goes in the body
// through the caller, not in the message.
func reported(what, msg, hint string) *mcp.CallToolResult {
	if msg == "" {
		msg = "unknown error"
	}
	return render.ErrorResult(render.Error{
		Code:    codeSlicer,
		Message: shortMessage(fmt.Sprintf("Failed to %s: %s", what, msg)),
		Hint:    hint,
	})
}

// successResult renders front, a typed struct (never a map: key order must be
// deterministic), and body into a tool result.
func successResult(front any, body string) *mcp.CallToolResult {
	return render.SuccessResult(front, spaceBeforeNext(body))
}

var nextRE = regexp.MustCompile(`([^\n])\nNext:`)

// spaceBeforeNext puts a blank line before a "Next:" line that follows text
// directly (after a list of warnings, rows or notes), so every reply reads the
// same whichever builder wrote it.
func spaceBeforeNext(body string) string {
	return nextRE.ReplaceAllString(body, "$1\n\nNext:")
}

// shortMessage keeps the start of an error message, which says what failed,
// within maxMessageBytes.
func shortMessage(s string) string {
	s = plainPaths(s)
	if len(s) <= maxMessageBytes {
		return s
	}
	cut := maxMessageBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " ... (message truncated)"
}

// truncateOutput keeps the last maxOutputBytes of s, the part that holds the
// result or the error, and says how much was left out.
func truncateOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	tail := s[len(s)-maxOutputBytes:]
	// Start on a whole line when one begins nearby, else on a whole character.
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i < 4<<10 {
		tail = tail[i+1:]
	} else {
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
		}
	}
	return fmt.Sprintf("[output truncated: the first %d of %d bytes are left out, the last %d follow]\n%s",
		len(s)-len(tail), len(s), len(tail), tail)
}

// jsonBlock renders v as indented JSON in a fence, truncated to its last
// maxOutputBytes.
func jsonBlock(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(v))
	}
	return render.Fence(truncateOutput(string(data)), "json")
}

// textBlock renders free text, such as a log, in a fence, truncated to its
// last maxOutputBytes.
func textBlock(s string) string {
	return render.Fence(truncateOutput(s), "text")
}

// capList returns at most limit items of list, and how many were left out.
func capList[T any](list []T, limit int) ([]T, int) {
	if len(list) <= limit {
		return list, 0
	}
	return list[:limit], len(list) - limit
}

// invalidArguments turns the error the SDK returns for arguments that fail a
// tool's input schema, or cannot be decoded into its input, into the
// invalid_input error the tools themselves return. The tools never set such
// an error on a result, so one that carries it came from the SDK.
func invalidArguments(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if call, ok := req.(*mcp.CallToolRequest); ok && method == "tools/call" && call.Params != nil {
			// A call without arguments means no arguments. The SDK cannot apply
			// schema defaults to a missing object, so give it an empty one.
			if a := strings.TrimSpace(string(call.Params.Arguments)); a == "" || a == "null" {
				call.Params.Arguments = json.RawMessage("{}")
			}
		}
		result, err := next(ctx, method, req)
		if err != nil || method != "tools/call" {
			return result, err
		}
		res, ok := result.(*mcp.CallToolResult)
		if !ok || res == nil || !res.IsError || res.GetError() == nil {
			return result, err
		}
		tool := "the tool"
		if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil && call.Params.Name != "" {
			tool = call.Params.Name
		}
		fresh := render.ErrorResult(render.Error{
			Code:    render.CodeInvalidInput,
			Message: shortMessage("Invalid arguments: " + argumentProblem(res.GetError())),
			Hint: strings.TrimSpace(fmt.Sprintf("Call %s again with arguments that match its input schema: every required "+
				"argument, each of the listed type, and only listed values and argument names. %s", tool, argumentsOf(tool))),
		})
		// Change the result in place instead of returning a new one: the SDK
		// has already marked this one complete (resultType), which a client on
		// the current protocol requires, and a fresh result would lack it.
		res.Content, res.StructuredContent, res.IsError = fresh.Content, fresh.StructuredContent, true
		return res, nil
	}
}

// argumentProblem states an SDK argument error without the SDK's framing.
func argumentProblem(err error) string {
	msg := err.Error()
	for _, prefix := range []string{`validating "arguments": `, "validating root: "} {
		msg = strings.TrimPrefix(msg, prefix)
	}
	msg = strings.TrimPrefix(msg, "json: ")
	return msg
}

// recovering wraps a tool handler so that a panic in it is an internal_error
// result and not the end of the server, and with it of every running slice.
// The stack goes to stderr. It is done here, in the handler, and not in a
// middleware: only the SDK's own result carries the resultType a client on the
// current protocol requires.
func recovering[In any](name string, h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error)) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (res *mcp.CallToolResult, out any, err error) {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "creality-slicer-mcp: panic in %s: %v\n%s\n", name, r, debug.Stack())
				res, out, err = render.ErrorResult(render.Error{
					Code:    render.CodeInternal,
					Message: "The tool failed unexpectedly.",
					Hint:    "This is a bug: report it with the arguments you used.",
				}), nil, nil
			}
		}()
		return h(ctx, req, in)
	}
}

// quotedPathRE finds an absolute path (Windows, UNC or Unix) written in double
// quotes, as %q writes it, with every backslash doubled.
var quotedPathRE = regexp.MustCompile(`"((?:[A-Za-z]:|\\\\\\\\|/)[^"\n]*)"`)

// plainPaths writes such a path as it is, in backticks: the message of a Go
// error quotes a path with %q, which doubles every backslash.
func plainPaths(s string) string {
	if !strings.Contains(s, "\"") {
		return s
	}
	return quotedPathRE.ReplaceAllStringFunc(s, func(m string) string {
		if plain, err := strconv.Unquote(m); err == nil {
			return "`" + plain + "`"
		}
		return m
	})
}
