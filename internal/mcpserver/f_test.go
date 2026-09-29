package mcpserver

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// F1: a failed slice is the cause, the log tail and the log path, not the
// app's whole output as well.
func TestFailedSliceReplyIsSmall(t *testing.T) {
	var out, tail []string
	for i := 0; i < 400; i++ {
		out = append(out, fmt.Sprintf("[2026-09-29 12:20:59.986645] [0x0000201c] [info] line %d of the application output", i))
	}
	for i := 0; i < 15; i++ {
		tail = append(tail, fmt.Sprintf("log tail line %d", i))
	}
	err := &projects.Error{Code: projects.CodeSlicerError, Message: "OBJECT_COLLISION_IN_SEQ_PRINT (exit code -63): objects collide", Hint: "Call slice_project with arrange true.",
		Fields: map[string]any{"exit_code": -63, "output_tail": strings.Join(out, "\n"), "log_tail": tail, "log_file": "C:\\Models\\out\\slice.log"}}
	res := projFailure(err)
	total := 0
	var all strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			total += len(tc.Text)
			all.WriteString(tc.Text)
		}
	}
	got := all.String()
	if strings.Contains(got, "Output:") || strings.Contains(got, "line 399 of the application output") {
		t.Errorf("the app's output is attached:\n%s", got)
	}
	contains(t, "failure", got, "slicer_error", "objects collide", "Last lines of the slicer log", "log tail line 14", "Full log: C:\\Models\\out\\slice.log", "arrange true")
	if total > 3000 {
		t.Errorf("a failed slice reply is %d bytes", total)
	}
	// Without a log, the output is cut to its last 15 lines.
	err.Fields = map[string]any{"output_tail": strings.Join(out, "\n")}
	res = projFailure(err)
	got = ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			got += tc.Text
		}
	}
	if !strings.Contains(got, "line 399 of the application output") || strings.Contains(got, "line 300 of the application output") {
		t.Errorf("the output without a log was not cut to its last lines:\n%s", got)
	}
}

// F2: numbers in the front matter carry a sensible precision.
func TestFrontNumbersAreRounded(t *testing.T) {
	o := objectFrontOf(projects.ObjectInfo{ID: 2, Name: "x", Size: [3]float64{28.284271240234375, 20.004999, 10}, Position: [3]float64{130.0000001, 99.999999, 0.005001}, Rotation: [3]float64{0, 44.9999, 90.04999}})
	if o.Size != [3]float64{28.28, 20, 10} || o.Position != [3]float64{130, 100, 0.01} || o.Rotation != [3]float64{0, 45, 90} {
		t.Errorf("front = %+v", o)
	}
	if round2(1.4999) != 1.5 || round2(3.216) != 3.22 || round1(0.04) != 0 || round1(12.26) != 12.3 {
		t.Errorf("rounding: %v %v %v %v", round2(1.4999), round2(3.216), round1(0.04), round1(12.26))
	}
}

// The slicer's time is shown once.
func TestElapsedIsShownOnce(t *testing.T) {
	for sec, want := range map[float64]string{0: "under 0.1 s", 0.049: "under 0.1 s", 0.05: "0.1 s", 0.94: "0.9 s", 12.34: "12.3 s", 59.96: "1m 00s", 1290.4: "21m 30s"} {
		if got := elapsedText(sec); got != want {
			t.Errorf("elapsedText(%v) = %q, want %q", sec, got, want)
		}
	}
	pf := newProjFixture(t)
	id := pf.withModel(t, "Timed")
	body := bodyOf(pf.ok(t, "slice_project", map[string]any{"project": id, "wait": 30}))
	re := regexp.MustCompile(`Sliced 1 plate\(s\) from revision \d+ in (under 0\.1 s|\d+\.\d s|\d+m \d\ds)\.`)
	if !re.MatchString(body) {
		t.Errorf("elapsed wording:\n%s", body)
	}
	if strings.Contains(body, " s).") {
		t.Errorf("the time is given twice:\n%s", body)
	}
}
