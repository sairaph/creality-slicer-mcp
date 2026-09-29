package main

// cpp.go: a small, purpose-built lexer and statement-tree builder for the C++
// files the catalog is extracted from. It is not a C++ parser. It understands
// comments, string and char literals, a few preprocessor conditionals, and
// splits code into statements and brace blocks.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// The only preprocessor macro whose guarded code we keep but tag. Its CMake
// option defaults to OFF (CMakeLists.txt:119).
const macroTimeAnalytics = "SLIC3R_ENABLE_TIME_ANALYTICS_EXPORT"

// CText is a comment-free, preprocessor-resolved view of a source file. Dead
// code (#if 0) and directives are blanked, but line numbers are preserved.
type CText struct {
	Rel    string
	Buf    []byte
	LineOf []int32  // byte offset -> 1-based line number
	Cond   []string // 1-based line -> macro condition tag ("" when unconditional)
	Raw    []string // original lines (CR stripped), index = line-1
}

func (c *CText) lineAt(off int) int {
	if off < 0 {
		off = 0
	}
	if off >= len(c.LineOf) {
		off = len(c.LineOf) - 1
	}
	return int(c.LineOf[off])
}

func (c *CText) condAt(line int) string {
	if line >= 0 && line < len(c.Cond) {
		return c.Cond[line]
	}
	return ""
}

// stripComments removes // and /* */ comments while leaving string and char
// literals intact. Newlines are kept so that line numbers stay valid.
func stripComments(src []byte) []byte {
	out := make([]byte, 0, len(src))
	n := len(src)
	i := 0
	for i < n {
		c := src[i]
		switch {
		case c == '"':
			// raw string literal R"delim(...)delim" (only if preceded by R)
			if i > 0 && src[i-1] == 'R' {
				j := i + 1
				for j < n && src[j] != '(' {
					j++
				}
				delim := string(src[i+1 : j])
				endTok := ")" + delim + "\""
				k := strings.Index(string(src[j:]), endTok)
				if k >= 0 {
					end := j + k + len(endTok)
					out = append(out, src[i:end]...)
					i = end
					continue
				}
			}
			out = append(out, c)
			i++
			for i < n {
				out = append(out, src[i])
				if src[i] == '\\' && i+1 < n {
					out = append(out, src[i+1])
					i += 2
					continue
				}
				if src[i] == '"' {
					i++
					break
				}
				i++
			}
		case c == '\'':
			// char literal: 'x' or '\x' or '\xx'
			if i+2 < n && src[i+2] == '\'' && src[i+1] != '\\' {
				out = append(out, src[i:i+3]...)
				i += 3
			} else if i+3 < n && src[i+1] == '\\' && src[i+3] == '\'' {
				out = append(out, src[i:i+4]...)
				i += 4
			} else {
				out = append(out, c)
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					out = append(out, '\n')
				}
				i++
			}
			i += 2
			out = append(out, ' ')
		default:
			out = append(out, c)
			i++
		}
	}
	return out
}

type ppState struct {
	live bool
	cond string
	// kind of the opening directive, for #else handling
	wasZero bool // "#if 0"
	wasOne  bool // "#if 1"
	other   bool // any other macro: both branches treated as live
}

var reDirective = regexp.MustCompile(`^\s*#\s*(\w+)\s*(.*)$`)

// loadClean turns the bytes of one source file into its CText.
func loadClean(rel string, data []byte) (*CText, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	raw := strings.Split(text, "\n")
	stripped := stripComments([]byte(text))
	lines := strings.Split(string(stripped), "\n")
	for len(lines) < len(raw) {
		lines = append(lines, "")
	}

	c := &CText{Rel: rel, Raw: raw}
	c.Cond = make([]string, len(lines)+2)
	var stack []ppState
	active := func() bool {
		for _, s := range stack {
			if !s.live {
				return false
			}
		}
		return true
	}
	condTag := func() string {
		var parts []string
		for _, s := range stack {
			if s.cond != "" {
				parts = append(parts, s.cond)
			}
		}
		return strings.Join(parts, " && ")
	}
	continuation := false
	var b strings.Builder
	for i, ln := range lines {
		lineNo := i + 1
		trim := strings.TrimSpace(ln)
		isDir := strings.HasPrefix(trim, "#")
		if continuation {
			continuation = strings.HasSuffix(strings.TrimRight(ln, " \t"), "\\")
			ln = ""
		} else if isDir {
			m := reDirective.FindStringSubmatch(ln)
			dir, arg := "", ""
			if m != nil {
				dir, arg = m[1], strings.TrimSpace(m[2])
			}
			switch dir {
			case "if":
				switch {
				case arg == "0":
					stack = append(stack, ppState{live: false, wasZero: true})
				case arg == "1":
					stack = append(stack, ppState{live: true, wasOne: true})
				default:
					st := ppState{live: true, other: true}
					if strings.Contains(arg, macroTimeAnalytics) {
						st.cond = macroTimeAnalytics
					}
					stack = append(stack, st)
				}
			case "ifdef":
				st := ppState{live: true, other: true}
				if arg == macroTimeAnalytics {
					st.cond = macroTimeAnalytics
				}
				stack = append(stack, st)
			case "ifndef":
				stack = append(stack, ppState{live: true, other: true})
			case "else", "elif":
				if len(stack) > 0 {
					top := &stack[len(stack)-1]
					switch {
					case top.wasZero:
						top.live = true
					case top.wasOne:
						top.live = false
					}
					if top.cond == macroTimeAnalytics {
						top.cond = "" // else branch of the macro is the default-on path
					}
				}
			case "endif":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
			continuation = strings.HasSuffix(strings.TrimRight(ln, " \t"), "\\")
			ln = ""
		}
		if !active() {
			ln = ""
		}
		c.Cond[lineNo] = condTag()
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	c.Buf = []byte(b.String())
	c.LineOf = make([]int32, len(c.Buf))
	cur := int32(1)
	for i, ch := range c.Buf {
		c.LineOf[i] = cur
		if ch == '\n' {
			cur++
		}
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Statement tree

const (
	nStmt  = iota // a statement; Text has no trailing ';'
	nBlock        // a {...} block; Header is the text before '{'
)

type Node struct {
	Kind     int
	Text     string // statement text
	Header   string // block header
	Line     int
	Cond     string
	Children []*Node
	Off      int // byte offset of first char in CText.Buf
}

var reLambdaInit = regexp.MustCompile(`(?s)=\s*\[[^\]]*\]\s*\(.*\)\s*(mutable\s*)?(->\s*[\w:<>]+\s*)?$`)

func isBlockHeader(p string) bool {
	if p == "" {
		return true
	}
	for _, kw := range []string{"for", "if", "while", "switch", "catch"} {
		if strings.HasPrefix(p, kw) && len(p) > len(kw) && !isIdentByte(p[len(kw)]) {
			return true
		}
	}
	if strings.HasPrefix(p, "else") || p == "do" || p == "try" {
		return true
	}
	for _, kw := range []string{"struct ", "class ", "namespace ", "enum ", "union ", "extern \""} {
		if strings.HasPrefix(p, kw) {
			return true
		}
	}
	if strings.HasPrefix(p, "static struct ") {
		return true
	}
	if reLambdaInit.MatchString(p) {
		return false
	}
	if strings.HasSuffix(p, ")") || strings.HasSuffix(p, "const") || strings.HasSuffix(p, "override") || strings.HasSuffix(p, "noexcept") {
		// function header, unless it is an assignment of a call result followed by braces
		return true
	}
	return false
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// buildTree converts the cleaned buffer to a statement tree.
func buildTree(c *CText) *Node {
	root := &Node{Kind: nBlock, Header: "<file>"}
	stack := []*Node{root}
	buf := c.Buf
	n := len(buf)
	var cur []byte
	curOff := -1
	depth := 0 // () and [] depth
	push := func(nd *Node) {
		top := stack[len(stack)-1]
		top.Children = append(top.Children, nd)
	}
	flushStmt := func() {
		t := strings.TrimSpace(string(cur))
		if t != "" {
			ln := c.lineAt(curOff)
			push(&Node{Kind: nStmt, Text: t, Line: ln, Cond: c.condAt(ln), Off: curOff})
		}
		cur = cur[:0]
		curOff = -1
	}
	appendByte := func(i int) {
		if curOff < 0 && buf[i] != ' ' && buf[i] != '\n' && buf[i] != '\t' {
			curOff = i
		}
		cur = append(cur, buf[i])
	}
	i := 0
	for i < n {
		ch := buf[i]
		switch ch {
		case '"':
			// string literal (raw strings only appear in comments/tests here)
			appendByte(i)
			i++
			for i < n {
				appendByte(i)
				if buf[i] == '\\' && i+1 < n {
					appendByte(i + 1)
					i += 2
					continue
				}
				if buf[i] == '"' {
					i++
					break
				}
				i++
			}
		case '\'':
			if i+2 < n && buf[i+2] == '\'' && buf[i+1] != '\\' {
				appendByte(i)
				appendByte(i + 1)
				appendByte(i + 2)
				i += 3
			} else if i+3 < n && buf[i+1] == '\\' && buf[i+3] == '\'' {
				for k := 0; k < 4; k++ {
					appendByte(i + k)
				}
				i += 4
			} else {
				appendByte(i)
				i++
			}
		case '(', '[':
			depth++
			appendByte(i)
			i++
		case ')', ']':
			depth--
			appendByte(i)
			i++
		case ';':
			if depth <= 0 {
				depth = 0
				flushStmt()
			} else {
				appendByte(i)
			}
			i++
		case '{':
			if depth > 0 {
				appendByte(i)
				i++
				break
			}
			hdr := strings.TrimSpace(string(cur))
			if isBlockHeader(hdr) {
				ln := c.lineAt(i)
				if curOff >= 0 {
					ln = c.lineAt(curOff)
				}
				nd := &Node{Kind: nBlock, Header: hdr, Line: ln, Cond: c.condAt(ln), Off: i}
				push(nd)
				stack = append(stack, nd)
				cur = cur[:0]
				curOff = -1
				i++
			} else {
				// brace initializer: consume to the matching '}' as part of the statement
				bd := 0
				for i < n {
					appendByte(i)
					switch buf[i] {
					case '"':
						i++
						for i < n {
							appendByte(i)
							if buf[i] == '\\' && i+1 < n {
								appendByte(i + 1)
								i += 2
								continue
							}
							if buf[i] == '"' {
								break
							}
							i++
						}
					case '{':
						bd++
					case '}':
						bd--
					}
					i++
					if bd == 0 {
						break
					}
				}
			}
		case '}':
			if depth > 0 {
				appendByte(i)
				i++
				break
			}
			flushStmt()
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			i++
		default:
			appendByte(i)
			i++
		}
	}
	flushStmt()
	return root
}

// ---------------------------------------------------------------------------
// Small expression helpers shared by the def and GUI parsers.

// splitTop splits s on sep at nesting depth 0, honoring strings, () [] {} and
// template angle brackets only when angleAware is set.
func splitTop(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '"':
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == '"' {
					break
				}
				i++
			}
		case '\'':
			if i+2 < len(s) && s[i+2] == '\'' {
				i += 2
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if ch == sep && depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	last := strings.TrimSpace(s[start:])
	if last != "" || len(parts) > 0 {
		parts = append(parts, last)
	}
	return parts
}

// matchParen returns the index of the ')' matching the '(' at s[open], or -1.
func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '"':
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == '"' {
					break
				}
				i++
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// stringLiterals returns every string literal in s (decoded), in order, with
// the byte offset of its opening quote.
type strLit struct {
	Val string
	Off int
}

func stringLiterals(s string) []strLit {
	var out []strLit
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			start := i
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j > len(s) {
				j = len(s)
			}
			out = append(out, strLit{Val: unescapeC(s[start+1 : min(j, len(s))]), Off: start})
			i = j
		}
	}
	return out
}

func unescapeC(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\'':
			b.WriteByte('\'')
		case '\\':
			b.WriteByte('\\')
		case '0':
			b.WriteByte(0)
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func warnf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", a...)
}
