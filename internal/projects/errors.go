package projects

import (
	"errors"
	"fmt"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// Codes of Error, the tool layer's error codes.
const (
	CodeInvalidInput = "invalid_input"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeUnavailable  = "unavailable"
	CodeSlicerError  = "slicer_error"
	CodeInternal     = "internal_error"
)

// Error is a failure the tool layer shows to the agent as it is: a code, a
// message that says what is wrong with the concrete names and values, and a
// hint that says what to do next.
type Error struct {
	Code    string
	Message string
	Hint    string
	// Fields carries structured detail: for slicer_error the exit code name,
	// the exit code, the meaning and the cleaned output tail.
	Fields map[string]any
}

func (e *Error) Error() string {
	if e.Hint != "" {
		return e.Message + " (" + e.Hint + ")"
	}
	return e.Message
}

func errf(code, hint, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...), Hint: hint}
}

func invalidf(hint, format string, a ...any) *Error {
	return errf(CodeInvalidInput, hint, format, a...)
}
func notFoundf(hint, format string, a ...any) *Error { return errf(CodeNotFound, hint, format, a...) }
func conflictf(hint, format string, a ...any) *Error { return errf(CodeConflict, hint, format, a...) }

// AsError returns err as an *Error (wrapping an unknown error as internal_error).
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// threemfError maps the error of a project mutation to a tool error: bad text
// is invalid input, a missing object or plate is not found, the rest is internal.
func threemfError(err error) *Error {
	switch {
	case errors.Is(err, threemf.ErrInvalid):
		return invalidf("names and values cannot hold control characters", "%v", err)
	case errors.Is(err, threemf.ErrNotFound):
		return notFoundf("call get_project to see the objects and plates", "%v", err)
	}
	return errf(CodeInternal, "", "%v", err)
}

// busyError is the conflict of a slice that cannot start because another run
// uses the project's output folder.
func busyError() *Error {
	return conflictf("poll get_slice_status for the running slice, or cancel it", "this project is already being sliced")
}
