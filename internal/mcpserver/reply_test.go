package mcpserver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortMessageCutsOnACharacterBoundary(t *testing.T) {
	if got := shortMessage("short"); got != "short" {
		t.Errorf("short message changed: %q", got)
	}
	// A multi-byte character straddling the limit must not be split.
	long := strings.Repeat("a", maxMessageBytes-1) + "é" + strings.Repeat("b", 100)
	got := shortMessage(long)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "(message truncated)") || len(got) > maxMessageBytes+40 {
		t.Errorf("shortMessage: valid=%v, %d bytes", utf8.ValidString(got), len(got))
	}
}

func TestTruncateOutputKeepsTheEnd(t *testing.T) {
	small := "one\ntwo"
	if truncateOutput(small) != small {
		t.Error("a small output was changed")
	}
	var b strings.Builder
	for i := 0; b.Len() < maxOutputBytes+5000; i++ {
		b.WriteString("line " + strings.Repeat("x", i%40) + "\n")
	}
	b.WriteString("the end")
	got := truncateOutput(b.String())
	if !strings.HasPrefix(got, "[output truncated: the first ") || !strings.HasSuffix(got, "the end") {
		t.Errorf("truncated output: prefix %.60q, suffix %q", got, got[len(got)-10:])
	}
	if len(got) > maxOutputBytes+200 {
		t.Errorf("truncated output is %d bytes", len(got))
	}
	// The kept part starts on a whole line.
	body := got[strings.Index(got, "\n")+1:]
	if !strings.HasPrefix(body, "line ") {
		t.Errorf("kept part starts mid-line: %.30q", body)
	}
}

func TestCapList(t *testing.T) {
	got, left := capList([]int{1, 2, 3, 4, 5}, 3)
	if len(got) != 3 || left != 2 {
		t.Errorf("capList = %v, %d", got, left)
	}
	got, left = capList([]int{1, 2}, 3)
	if len(got) != 2 || left != 0 {
		t.Errorf("capList = %v, %d", got, left)
	}
}

func TestBlocksAreFenced(t *testing.T) {
	if got := textBlock("a\nb"); got != "~~~text\na\nb\n~~~\n" {
		t.Errorf("textBlock = %q", got)
	}
	if got := jsonBlock(map[string]string{"k": "v"}); got != "~~~json\n{\n  \"k\": \"v\"\n}\n~~~\n" {
		t.Errorf("jsonBlock = %q", got)
	}
	// Text containing a tilde fence gets a longer one.
	if got := textBlock("~~~\ninner\n~~~"); !strings.HasPrefix(got, "~~~~text\n") {
		t.Errorf("textBlock with a tilde fence = %q", got)
	}
}
