package threemf

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// node is a generic XML element that keeps attribute order, so elements this
// package does not understand survive a regeneration of their file.
type node struct {
	Name     string
	Attrs    KVs
	Children []*node
	Text     string
}

func (n *node) attrValue(key string) string { return n.Attrs.Value(key) }

func (n *node) child(name string) *node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// qname renders a raw token name with its prefix.
func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// parseTree parses a whole XML document into a tree and returns its root.
// Names keep their prefixes.
func parseTree(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var stack []*node
	var root *node
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{Name: qname(t.Name)}
			for _, a := range t.Attr {
				n.Attrs = append(n.Attrs, KV{qname(a.Name), a.Value})
			}
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.Children = append(top.Children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.Text += string(t)
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no root element")
	}
	trimText(root)
	return root, nil
}

// trimText drops whitespace only text of elements that have children, and
// surrounding whitespace of leaf text.
func trimText(n *node) {
	if len(n.Children) > 0 {
		n.Text = ""
	} else {
		n.Text = strings.TrimSpace(n.Text)
	}
	for _, c := range n.Children {
		trimText(c)
	}
}

// xmlEscape escapes like the slicer's xml_escape: " ' & < >.
func xmlEscape(s string) string {
	s = stripIllegalXML(s)
	if !strings.ContainsAny(s, "\"'&<>") {
		return s
	}
	return strings.NewReplacer("\"", "&quot;", "'", "&apos;", "&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// write renders the node with the given indentation, two spaces per level.
func (n *node) write(b *strings.Builder, indent string) {
	b.WriteString(indent)
	b.WriteString("<" + n.Name)
	for _, a := range n.Attrs {
		b.WriteString(" " + a.Key + "=\"" + attrEscape(a.Value) + "\"")
	}
	switch {
	case len(n.Children) == 0 && n.Text == "":
		b.WriteString("/>\n")
	case len(n.Children) == 0:
		b.WriteString(">" + xmlEscape(n.Text) + "</" + n.Name + ">\n")
	default:
		b.WriteString(">\n")
		for _, c := range n.Children {
			c.write(b, indent+"  ")
		}
		b.WriteString(indent + "</" + n.Name + ">\n")
	}
}

// attrEscape escapes an attribute value: xmlEscape plus character references
// for line breaks and tabs, which an XML parser would otherwise turn into
// spaces.
func attrEscape(s string) string {
	s = xmlEscape(s)
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	return strings.NewReplacer("\n", "&#10;", "\r", "&#13;", "\t", "&#9;").Replace(s)
}

// illegalXML reports a character XML 1.0 does not allow in a document: the C0
// controls other than tab, line feed and carriage return, the surrogate range
// and U+FFFE, U+FFFF.
func illegalXML(r rune) bool {
	switch {
	case r == '\t' || r == '\n' || r == '\r':
		return false
	case r < 0x20, r >= 0xD800 && r <= 0xDFFF, r == 0xFFFE, r == 0xFFFF:
		return true
	}
	return false
}

// stripIllegalXML removes the characters XML cannot hold. Everything written
// to a project goes through it, so an exported 3MF is always well formed XML,
// whatever a name or a value (from a tool or from an input file) contained.
func stripIllegalXML(s string) string {
	if strings.IndexFunc(s, illegalXML) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if illegalXML(r) {
			return -1
		}
		return r
	}, s)
}

// checkText rejects, at the boundary of a mutation, text that has characters
// XML cannot hold, so the caller learns instead of a silent change.
func checkText(what string, values ...string) error {
	for _, v := range values {
		if strings.IndexFunc(v, illegalXML) >= 0 {
			return fmt.Errorf("%w: the %s contains a control character, which a project file cannot hold", ErrInvalid, what)
		}
	}
	return nil
}
