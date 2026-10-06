package appui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Width is the display width of s in terminal columns. Escape codes count for
// nothing and wide characters for two.
func Width(s string) int { return ansi.StringWidth(s) }

// Spaces is n blanks (none for n < 1).
func Spaces(n int) string {
	if n < 1 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// PadRight pads s with blanks to w columns; a longer s is returned as it is.
func PadRight(s string, w int) string { return s + Spaces(w-Width(s)) }

// PadLeft puts blanks in front of s up to w columns.
func PadLeft(s string, w int) string { return Spaces(w-Width(s)) + s }

// Wrap word-wraps plain text to width columns: existing newlines are kept, and
// every line after the first starts with hang blanks (the width counts them).
// A word wider than the line breaks after a backslash, slash or hyphen that
// fits, else at the width. Nothing is ever cut.
func Wrap(text string, width, hang int) []string {
	return WrapPrefixed(text, "", Spaces(hang), width)
}

// WrapPrefixed wraps plain text to width columns with first in front of the
// first line and rest in front of every other line. The prefixes may carry
// styling; their display width is counted.
func WrapPrefixed(text, first, rest string, width int) []string {
	return WrapRole(text, first, rest, width, RoleText)
}

// WrapRole is WrapPrefixed with the wrapped text painted in the colour of a role
// (the prefixes are left as they are).
func WrapRole(text, first, rest string, width int, role Role) []string {
	if text == "" {
		return nil
	}
	var out []string
	firstLine := true
	prefix := func() string {
		if firstLine {
			return first
		}
		return rest
	}
	for _, par := range strings.Split(text, "\n") {
		words := strings.Fields(par)
		if len(words) == 0 {
			out = append(out, "")
			firstLine = false
			continue
		}
		cur := ""
		flush := func() {
			out = append(out, prefix()+Paint(role, cur))
			firstLine = false
			cur = ""
		}
		for _, w := range words {
			for {
				avail := max(width-Width(prefix()), 1)
				if cur == "" {
					if Width(w) <= avail {
						cur = w
						break
					}
					var head string
					head, w = cutToken(w, avail)
					cur = head
					flush()
					continue
				}
				if Width(cur)+1+Width(w) <= avail {
					cur += " " + w
					break
				}
				flush()
			}
		}
		if cur != "" {
			flush()
		}
	}
	return out
}

// cutToken splits a word wider than avail: the head fits, and ends after a
// path separator when one is far enough in.
func cutToken(tok string, avail int) (head, tail string) {
	runes := []rune(tok)
	w, n := 0, 0
	for n < len(runes) {
		rw := Width(string(runes[n]))
		if w+rw > avail && n > 0 {
			break
		}
		w += rw
		n++
	}
	cut := n
	if n < len(runes) {
		for j := n; j >= 1; j-- {
			if isBreakRune(runes[j-1]) {
				if j*3 >= n {
					cut = j
				}
				break
			}
		}
	}
	return string(runes[:cut]), string(runes[cut:])
}

func isBreakRune(r rune) bool { return r == '\\' || r == '/' || r == '-' }

// wrapRanges splits runes into lines of at most width columns without losing a
// rune (spaces stay), preferring to end a line after a space, backslash, slash
// or hyphen. It returns the [start, end) index of each line. An empty input is
// one empty line.
func wrapRanges(runes []rune, width int) [][2]int {
	width = max(width, 1)
	var out [][2]int
	start := 0
	for start < len(runes) {
		w, n := 0, start
		for n < len(runes) {
			rw := Width(string(runes[n]))
			if w+rw > width && n > start {
				break
			}
			w += rw
			n++
		}
		end := n
		if n < len(runes) {
			for j := n; j > start; j-- {
				if isBreakRune(runes[j-1]) || runes[j-1] == ' ' {
					if (j-start)*3 >= n-start {
						end = j
					}
					break
				}
			}
		}
		out = append(out, [2]int{start, end})
		start = end
	}
	if len(out) == 0 {
		out = append(out, [2]int{0, 0})
	}
	return out
}
