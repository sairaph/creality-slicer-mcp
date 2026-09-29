package xmlscan

import (
	"strings"
	"testing"
)

func collect(t *testing.T, src string) []string {
	t.Helper()
	sc := New([]byte(src))
	var out []string
	for {
		tok, ok, err := sc.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return out
		}
		switch tok.Kind {
		case Start:
			s := "<" + string(tok.Name)
			tok.EachAttr(func(k, v string) { s += " " + k + "=" + v })
			out = append(out, s+">")
		case End:
			out = append(out, "</"+string(tok.Name)+">")
		case Text:
			out = append(out, "T:"+Unescape(tok.Data))
		}
	}
}

func TestTokens(t *testing.T) {
	src := "\xef\xbb\xbf<?xml version=\"1.0\"?>\n<!DOCTYPE x>\n<!-- a <comment> -->\n" +
		`<model xmlns:p="urn:p" p:UUID='u-1' unit="mm"><metadata name="A &amp; B">x &lt; y</metadata>` +
		`<vertex x="1.5" y='-2' z="3"/>  <p:item a = "1" /><![CDATA[raw <text>]]></model>`
	got := strings.Join(collect(t, src), " ")
	want := `<model p=urn:p UUID=u-1 unit=mm> <metadata name=A & B> T:x < y </metadata> <vertex x=1.5 y=-2 z=3> </vertex> <item a=1> </item> T:raw <text> </model>`
	if got != want {
		t.Errorf("\n%s\n%s", got, want)
	}
}

func TestAttrLookup(t *testing.T) {
	sc := New([]byte(`<a p:path="/x&quot;y" UUID="U" n="5" empty=""/>`))
	tok, ok, err := sc.Next()
	if err != nil || !ok {
		t.Fatal(err)
	}
	if v, ok := tok.Attr("path"); !ok || v != `/x"y` {
		t.Errorf("%q", v)
	}
	if v, ok := tok.AttrFold("uuid"); !ok || v != "U" {
		t.Errorf("%q", v)
	}
	if b, ok := tok.AttrBytes("n"); !ok || string(b) != "5" {
		t.Errorf("%q", b)
	}
	if _, ok := tok.Attr("nope"); ok || tok.AttrString("nope") != "" {
		t.Error("missing attribute")
	}
	if b, ok := tok.AttrBytes("empty"); !ok || len(b) != 0 {
		t.Error("empty attribute")
	}
	if !tok.Is("a") || string(tok.Raw) != `<a p:path="/x&quot;y" UUID="U" n="5" empty=""/>` {
		t.Errorf("%q", tok.Raw)
	}
	if _, ok := tok.AttrFold("nope"); ok {
		t.Error("AttrFold miss")
	}
}

func TestSelfClosingYieldsEnd(t *testing.T) {
	got := strings.Join(collect(t, `<a><b/><c x="1"/></a>`), " ")
	if got != "<a> <b> </b> <c x=1> </c> </a>" {
		t.Error(got)
	}
}

func TestErrors(t *testing.T) {
	for name, src := range map[string]string{
		"pi":          "<?xml",
		"comment":     "<!-- x",
		"cdata":       "<![CDATA[ x",
		"declaration": "<!DOCTYPE",
		"end tag":     "<a></a",
		"start tag":   "<a b=\"1\"",
		"name":        "<a",
		"unquoted":    "<a b=1>",
		"value":       "<a b=\"1>",
		"stray slash": "<a / b>",
	} {
		sc := New([]byte(src))
		var err error
		for {
			var ok bool
			_, ok, err = sc.Next()
			if err != nil || !ok {
				break
			}
		}
		if err == nil {
			t.Errorf("%s: %q must fail", name, src)
		}
	}
}

func TestLenientAttributeWithoutValue(t *testing.T) {
	got := strings.Join(collect(t, `<a checked b="1">`), " ")
	if got != "<a checked= b=1>" {
		t.Error(got)
	}
}

func TestUnescape(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                   "plain",
		"a &amp; b":               "a & b",
		"&lt;&gt;&quot;&apos;":    `<>"'`,
		"&#65;&#x42;&#X43;":       "ABC",
		"&unknown;":               "&unknown;",
		"& alone":                 "& alone",
		"&#zz;":                   "&#zz;",
		"&averyveryverylongname;": "&averyveryverylongname;",
	} {
		if got := Unescape([]byte(in)); got != want {
			t.Errorf("Unescape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOffsetAndLargeInput(t *testing.T) {
	var b strings.Builder
	b.WriteString("<m>")
	for i := 0; i < 20000; i++ {
		b.WriteString(`<vertex x="1" y="2" z="3"/>`)
	}
	b.WriteString("</m>")
	sc := New([]byte(b.String()))
	n := 0
	for {
		tok, ok, err := sc.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if tok.Kind == Start && tok.Is("vertex") {
			n++
		}
	}
	if n != 20000 || sc.Offset() != b.Len() {
		t.Errorf("%d vertices, offset %d of %d", n, sc.Offset(), b.Len())
	}
}
