package gcodeinfo

import (
	"math"
	"strconv"
)

// MotionState is what the printer knows between two G-code lines: enough to
// resume reading the file in the middle (at a layer) and still interpret the
// next move correctly.
type MotionState struct {
	X, Y, Z     float64
	E           float64 // current extruder position (meaningful with AbsoluteE)
	Feed        float64 // last F word, mm/min
	Tool        int     // active T<n>
	Feature     string  // last ;TYPE: value
	AbsoluteE   bool    // M82 (default false: the K2 profiles use M83)
	AbsoluteXYZ bool    // G90 (default true)
}

func newMotionState() MotionState {
	return MotionState{AbsoluteXYZ: true}
}

// Move is one planar motion of the tool head: a G0/G1 line (or a G2/G3 arc,
// approximated by its chord) that changes X or Y.
type Move struct {
	X0, Y0, X1, Y1 float64
	Z              float64 // Z after the move
	Extruding      bool    // filament is pushed (E delta > 0)
	E              float64 // E delta of the move in mm of filament (negative = retract)
	Feature        string  // ;TYPE: in effect
	Tool           int
	Speed          float64 // F in effect, mm/min
	Arc            bool
	Clockwise      bool    // arcs only: G2
	I, J           float64 // arcs only: centre offset from the start
	P              float64 // arcs only: the P word, the number of full turns (0 when absent)
}

// apply interprets one G-code line, updates the state and, when the line moves
// the tool in the XY plane, returns the move.
func (s *MotionState) apply(line []byte) (Move, bool) {
	// Feature tags are comments and must be read before they are stripped.
	if len(line) > 6 && line[0] == ';' {
		if hasPrefix(line, ";TYPE:") {
			s.Feature = string(trimSpace(line[6:]))
		}
		return Move{}, false
	}
	line = trimSpace(stripComment(line))
	if len(line) < 2 {
		return Move{}, false
	}
	switch line[0] {
	case 'T':
		if n, ok := parseUint(line[1:]); ok {
			s.Tool = n
		}
		return Move{}, false
	case 'M':
		switch string(firstWord(line)) {
		case "M82":
			s.AbsoluteE = true
		case "M83":
			s.AbsoluteE = false
		}
		return Move{}, false
	case 'G':
	default:
		return Move{}, false
	}
	switch string(firstWord(line)) {
	case "G0", "G00", "G1", "G01":
		return s.move(line, false, false)
	case "G2", "G02":
		return s.move(line, true, true)
	case "G3", "G03":
		return s.move(line, true, false)
	case "G90":
		s.AbsoluteXYZ = true
	case "G91":
		s.AbsoluteXYZ = false
	case "G92":
		s.set(line)
	}
	return Move{}, false
}

// set handles G92 (set position): only the words present change.
func (s *MotionState) set(line []byte) {
	eachWord(line, func(letter byte, v float64) {
		switch letter {
		case 'X':
			s.X = v
		case 'Y':
			s.Y = v
		case 'Z':
			s.Z = v
		case 'E':
			s.E = v
		}
	})
}

func (s *MotionState) move(line []byte, arc, cw bool) (Move, bool) {
	var x, y, z, e, i, j, pTurns float64
	var hasX, hasY, hasZ, hasE bool
	eachWord(line, func(letter byte, v float64) {
		switch letter {
		case 'X':
			x, hasX = v, true
		case 'Y':
			y, hasY = v, true
		case 'Z':
			z, hasZ = v, true
		case 'E':
			e, hasE = v, true
		case 'I':
			i = v
		case 'J':
			j = v
		case 'P':
			pTurns = v
		case 'F':
			s.Feed = v
		}
	})
	x0, y0 := s.X, s.Y
	if hasX {
		if s.AbsoluteXYZ {
			s.X = x
		} else {
			s.X += x
		}
	}
	if hasY {
		if s.AbsoluteXYZ {
			s.Y = y
		} else {
			s.Y += y
		}
	}
	if hasZ {
		if s.AbsoluteXYZ {
			s.Z = z
		} else {
			s.Z += z
		}
	}
	var de float64
	if hasE {
		if s.AbsoluteE {
			de = e - s.E
			s.E = e
		} else {
			de = e
			s.E += e
		}
	}
	if s.X == x0 && s.Y == y0 && !arc {
		return Move{}, false
	}
	if !hasX && !hasY && !arc {
		return Move{}, false
	}
	return Move{
		X0: x0, Y0: y0, X1: s.X, Y1: s.Y, Z: s.Z,
		Extruding: de > 0, E: de,
		Feature: s.Feature, Tool: s.Tool, Speed: s.Feed,
		Arc: arc, Clockwise: cw && arc, I: i, J: j, P: pTurns,
	}, true
}

// eachWord calls fn for every letter+number word after the command word.
func eachWord(line []byte, fn func(letter byte, v float64)) {
	n := len(line)
	p := 0
	for p < n && line[p] != ' ' && line[p] != '\t' {
		p++
	}
	for p < n {
		for p < n && (line[p] == ' ' || line[p] == '\t') {
			p++
		}
		if p >= n {
			return
		}
		letter := line[p]
		p++
		start := p
		for p < n && line[p] != ' ' && line[p] != '\t' {
			p++
		}
		if letter >= 'a' && letter <= 'z' {
			letter -= 'a' - 'A'
		}
		if v, ok := parseFloat(line[start:p]); ok {
			fn(letter, v)
		}
	}
}

func stripComment(b []byte) []byte {
	for i, c := range b {
		if c == ';' {
			return b[:i]
		}
	}
	return b
}

func firstWord(b []byte) []byte {
	for i, c := range b {
		if c == ' ' || c == '\t' {
			return b[:i]
		}
	}
	return b
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func hasPrefix(b []byte, p string) bool {
	return len(b) >= len(p) && string(b[:len(p)]) == p
}

// parseUint reads leading digits (T12, T1 ; comment is already stripped).
func parseUint(b []byte) (int, bool) {
	n, digits := 0, 0
	for _, c := range b {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		digits++
		if digits > 9 {
			return 0, false
		}
	}
	if digits == 0 {
		return 0, false
	}
	// Anything after the digits other than space means it is not a tool word
	// (for example "TIMELAPSE_TAKE_FRAME").
	if digits < len(b) && b[digits] != ' ' && b[digits] != '\t' {
		return 0, false
	}
	return n, true
}

var pow10 = [...]float64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15}

// parseFloat parses a G-code number ("-1", ".3742", "12.5") without
// allocating; anything unusual falls back to strconv.
func parseFloat(b []byte) (float64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	i, neg := 0, false
	if b[0] == '-' || b[0] == '+' {
		neg = b[0] == '-'
		i = 1
	}
	var mant uint64
	digits, frac := 0, -1
	for ; i < len(b); i++ {
		c := b[i]
		switch {
		case c >= '0' && c <= '9':
			mant = mant*10 + uint64(c-'0')
			digits++
			if frac >= 0 {
				frac++
			}
		case c == '.' && frac < 0:
			frac = 0
		default:
			return slowFloat(b)
		}
	}
	if digits == 0 || digits > 15 {
		return slowFloat(b)
	}
	v := float64(mant)
	if frac > 0 {
		v /= pow10[frac]
	}
	if neg {
		v = -v
	}
	return v, true
}

func slowFloat(b []byte) (float64, bool) {
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}
