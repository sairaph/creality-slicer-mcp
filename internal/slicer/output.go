package slicer

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"sync"
)

// tailLimit is the most output kept per stream (an anti-overflow guard, not a
// product limit): the newest bytes win.
const tailLimit = 1 << 20

// logLine matches the slicer's own trace, debug and info log lines that reach
// stdout; warnings and errors are kept.
var logLine = regexp.MustCompile(`^\[\d{4}-\d\d-\d\d [0-9:.]+\] \[0x[0-9a-f]+\] \[(trace|debug|info)\]`)

// isNoise reports launcher chatter and low-level log lines.
func isNoise(line string) bool {
	return strings.HasPrefix(line, "OpenGL probe") || logLine.MatchString(line)
}

// cleanOutput makes captured console output presentable: valid UTF-8, LF line
// ends, no blank lines and no launcher noise. Error lines are kept.
func cleanOutput(data []byte) string {
	text := strings.ToValidUTF8(string(data), "�")
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, "\r \t")
		if strings.TrimSpace(line) == "" || isNoise(line) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	limit     int
	buf       []byte
	truncated bool
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	// Compact lazily so a stream of small writes costs amortised O(1).
	if len(t.buf) > 2*t.limit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.limit:]...)
		t.truncated = true
	}
	return len(p), nil
}

// Bytes returns the retained tail, starting at a line boundary when the head
// was dropped.
func (t *tailBuffer) Bytes() []byte {
	b := t.buf
	if len(b) > t.limit {
		b = b[len(b)-t.limit:]
		t.truncated = true
	}
	if t.truncated {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b
}

// syncWriter serialises writes from the stdout and stderr copiers.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
