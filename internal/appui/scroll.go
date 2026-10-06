package appui

import tea "github.com/charmbracelet/bubbletea"

// Scroller is the offset of a scrolling screen. Only the offset is stored; the
// screen height changes with the window, so every use clamps it again.
type Scroller struct{ Off int }

// Clamp keeps the offset inside the content: total lines shown cap at a time.
func (s *Scroller) Clamp(total, cap int) {
	s.Off = min(max(s.Off, 0), max(total-max(cap, 1), 0))
}

// Keys applies a scrolling key (up/down, k/j, pgup/pgdn, home/end, g/G) and
// reports whether the key was one.
func (s *Scroller) Keys(msg tea.KeyMsg, total, cap int) bool {
	page := max(cap-1, 1)
	switch msg.String() {
	case "up", "k":
		s.Off--
	case "down", "j":
		s.Off++
	case "pgup":
		s.Off -= page
	case "pgdown":
		s.Off += page
	case "home", "g":
		s.Off = 0
	case "end", "G":
		s.Off = total
	default:
		return false
	}
	s.Clamp(total, cap)
	return true
}

// Window clamps the offset and returns the visible part of lines, with the
// 1-based first and last line shown.
func (s *Scroller) Window(lines []string, cap int) (visible []string, from, to int) {
	s.Clamp(len(lines), cap)
	end := min(s.Off+max(cap, 1), len(lines))
	return lines[s.Off:end], min(s.Off+1, end), end
}

// windowAround returns at most rows of lines for a screen that is not
// scrolled by the user: its end (a text field, the options of a confirm) when
// tail is set, else its start, moved so that line keep is in view.
func windowAround(lines []string, rows, keep int, tail bool) []string {
	rows = max(rows, 1)
	if len(lines) <= rows {
		return lines
	}
	off := 0
	if tail {
		off = len(lines) - rows
	}
	if keep < off {
		off = keep
	}
	if keep >= off+rows {
		off = keep - rows + 1
	}
	off = min(max(off, 0), len(lines)-rows)
	return lines[off : off+rows]
}
