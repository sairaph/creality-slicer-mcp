// Package xmlscan is a small, fast, forgiving XML tokenizer for the huge
// regular files of a 3MF package (a mesh file can hold a million triangle
// elements). It hands out start and end tags with their attributes and the
// non blank text between tags, without building anything: attributes are
// slices of the input, converted only when asked.
//
// It is not a validating parser: it skips the XML declaration, comments,
// processing instructions and DOCTYPE, treats CDATA as text, and does not
// check that tags match.
package xmlscan

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Kind is the kind of a token.
type Kind int

// Token kinds.
const (
	Start Kind = iota + 1 // <name attr="v"> (a self closing tag yields Start then End)
	End                   // </name>
	Text                  // non blank text between tags
)

type rawAttr struct{ name, value []byte }

// Token is one lexical item. It is only valid until the next call to Next.
type Token struct {
	Kind Kind
	// Name is the element name without its namespace prefix (Start and End).
	Name []byte
	// Raw is the whole start tag as written ("<model unit=...>"), for Start.
	Raw []byte
	// Data is the raw text of a Text token (entities not decoded).
	Data  []byte
	attrs []rawAttr
}

// Attr returns the attribute whose name (prefix ignored) is local, with
// entities decoded.
func (t *Token) Attr(local string) (string, bool) {
	b, ok := t.AttrBytes(local)
	if !ok {
		return "", false
	}
	return Unescape(b), true
}

// AttrString is Attr without the ok.
func (t *Token) AttrString(local string) string {
	s, _ := t.Attr(local)
	return s
}

// AttrBytes returns the raw bytes of an attribute value (entities not decoded).
func (t *Token) AttrBytes(local string) ([]byte, bool) {
	for i := range t.attrs {
		if localName(t.attrs[i].name, local) {
			return t.attrs[i].value, true
		}
	}
	return nil, false
}

// AttrFold is like Attr but matches the name ignoring case.
func (t *Token) AttrFold(local string) (string, bool) {
	for i := range t.attrs {
		n := t.attrs[i].name
		if j := bytes.IndexByte(n, ':'); j >= 0 {
			n = n[j+1:]
		}
		if strings.EqualFold(string(n), local) {
			return Unescape(t.attrs[i].value), true
		}
	}
	return "", false
}

// EachAttr calls fn for every attribute with its local name and decoded value.
func (t *Token) EachAttr(fn func(local, value string)) {
	for i := range t.attrs {
		n := t.attrs[i].name
		if j := bytes.IndexByte(n, ':'); j >= 0 {
			n = n[j+1:]
		}
		fn(string(n), Unescape(t.attrs[i].value))
	}
}

func localName(name []byte, local string) bool {
	if j := bytes.IndexByte(name, ':'); j >= 0 {
		name = name[j+1:]
	}
	return string(name) == local
}

// Is reports whether the element name equals s.
func (t *Token) Is(s string) bool { return string(t.Name) == s }

// Scanner walks over a document held in memory.
type Scanner struct {
	data    []byte
	pos     int
	pending bool // a self closing tag still owes its End token
	name    []byte
	buf     []rawAttr
	tok     Token
}

// New returns a Scanner for data.
func New(data []byte) *Scanner {
	return &Scanner{data: bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))}
}

// Offset is the position after the last token.
func (s *Scanner) Offset() int { return s.pos }

// Next returns the next token; ok is false at the end of the document. The
// returned token is reused by the next call.
func (s *Scanner) Next() (tok *Token, ok bool, err error) {
	if s.pending {
		s.pending = false
		s.tok = Token{Kind: End, Name: s.name}
		return &s.tok, true, nil
	}
	for s.pos < len(s.data) {
		if s.data[s.pos] != '<' {
			end := bytes.IndexByte(s.data[s.pos:], '<')
			var text []byte
			if end < 0 {
				text, s.pos = s.data[s.pos:], len(s.data)
			} else {
				text, s.pos = s.data[s.pos:s.pos+end], s.pos+end
			}
			if len(bytes.TrimSpace(text)) > 0 {
				s.tok = Token{Kind: Text, Data: text}
				return &s.tok, true, nil
			}
			continue
		}
		rest := s.data[s.pos:]
		switch {
		case bytes.HasPrefix(rest, []byte("<?")):
			end := bytes.Index(rest, []byte("?>"))
			if end < 0 {
				return nil, false, fmt.Errorf("xml: unterminated processing instruction at offset %d", s.pos)
			}
			s.pos += end + 2
		case bytes.HasPrefix(rest, []byte("<!--")):
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				return nil, false, fmt.Errorf("xml: unterminated comment at offset %d", s.pos)
			}
			s.pos += 4 + end + 3
		case bytes.HasPrefix(rest, []byte("<![CDATA[")):
			end := bytes.Index(rest, []byte("]]>"))
			if end < 0 {
				return nil, false, fmt.Errorf("xml: unterminated CDATA at offset %d", s.pos)
			}
			text := rest[9:end]
			s.pos += end + 3
			if len(bytes.TrimSpace(text)) > 0 {
				s.tok = Token{Kind: Text, Data: text}
				return &s.tok, true, nil
			}
		case bytes.HasPrefix(rest, []byte("<!")):
			end := bytes.IndexByte(rest, '>')
			if end < 0 {
				return nil, false, fmt.Errorf("xml: unterminated declaration at offset %d", s.pos)
			}
			s.pos += end + 1
		case bytes.HasPrefix(rest, []byte("</")):
			end := bytes.IndexByte(rest, '>')
			if end < 0 {
				return nil, false, fmt.Errorf("xml: unterminated end tag at offset %d", s.pos)
			}
			name := bytes.TrimSpace(rest[2:end])
			s.pos += end + 1
			s.tok = Token{Kind: End, Name: stripPrefix(name)}
			return &s.tok, true, nil
		default:
			return s.startTag()
		}
	}
	return nil, false, nil
}

func stripPrefix(name []byte) []byte {
	if j := bytes.IndexByte(name, ':'); j >= 0 {
		return name[j+1:]
	}
	return name
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// startTag parses "<name attr="v" ...>" or its self closing form.
func (s *Scanner) startTag() (*Token, bool, error) {
	data := s.data
	begin := s.pos
	i := begin + 1
	for i < len(data) && !isSpace(data[i]) && data[i] != '>' && data[i] != '/' {
		i++
	}
	if i >= len(data) {
		return nil, false, fmt.Errorf("xml: unterminated start tag at offset %d", begin)
	}
	name := data[begin+1 : i]
	attrs := s.buf[:0]
	selfClosing := false
	for {
		for i < len(data) && isSpace(data[i]) {
			i++
		}
		if i >= len(data) {
			return nil, false, fmt.Errorf("xml: unterminated start tag at offset %d", begin)
		}
		c := data[i]
		if c == '>' {
			i++
			break
		}
		if c == '/' {
			if i+1 < len(data) && data[i+1] == '>' {
				selfClosing = true
				i += 2
				break
			}
			return nil, false, fmt.Errorf("xml: stray / in start tag at offset %d", i)
		}
		ns := i
		for i < len(data) && data[i] != '=' && !isSpace(data[i]) && data[i] != '>' && data[i] != '/' {
			i++
		}
		an := data[ns:i]
		for i < len(data) && isSpace(data[i]) {
			i++
		}
		if i >= len(data) || data[i] != '=' {
			// An attribute without a value (lenient): keep it empty.
			attrs = append(attrs, rawAttr{name: an})
			continue
		}
		i++
		for i < len(data) && isSpace(data[i]) {
			i++
		}
		if i >= len(data) || (data[i] != '"' && data[i] != '\'') {
			return nil, false, fmt.Errorf("xml: attribute %q needs a quoted value at offset %d", an, i)
		}
		q := data[i]
		i++
		vs := i
		end := bytes.IndexByte(data[i:], q)
		if end < 0 {
			return nil, false, fmt.Errorf("xml: unterminated attribute value at offset %d", vs)
		}
		attrs = append(attrs, rawAttr{name: an, value: data[vs : vs+end]})
		i = vs + end + 1
	}
	s.pos = i
	s.buf = attrs
	s.name = stripPrefix(name)
	s.tok = Token{Kind: Start, Name: s.name, Raw: data[begin:i], attrs: attrs}
	s.pending = selfClosing
	return &s.tok, true, nil
}

// Unescape decodes the five predefined entities and numeric character
// references.
func Unescape(b []byte) string {
	if bytes.IndexByte(b, '&') < 0 {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		if b[i] != '&' {
			sb.WriteByte(b[i])
			i++
			continue
		}
		semi := bytes.IndexByte(b[i:], ';')
		if semi < 0 || semi > 10 {
			sb.WriteByte('&')
			i++
			continue
		}
		ent := string(b[i+1 : i+semi])
		switch ent {
		case "amp":
			sb.WriteByte('&')
		case "lt":
			sb.WriteByte('<')
		case "gt":
			sb.WriteByte('>')
		case "quot":
			sb.WriteByte('"')
		case "apos":
			sb.WriteByte('\'')
		default:
			var code int64
			var err error
			switch {
			case strings.HasPrefix(ent, "#x"), strings.HasPrefix(ent, "#X"):
				code, err = strconv.ParseInt(ent[2:], 16, 32)
			case strings.HasPrefix(ent, "#"):
				code, err = strconv.ParseInt(ent[1:], 10, 32)
			default:
				err = fmt.Errorf("unknown entity")
			}
			if err != nil {
				sb.WriteString("&" + ent + ";")
			} else {
				sb.WriteRune(rune(code))
			}
		}
		i += semi + 1
	}
	return sb.String()
}
