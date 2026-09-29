package threemf

import (
	"reflect"
	"strings"
	"testing"
)

func TestConfigRoundTripKeepsOrderAndFormat(t *testing.T) {
	// Deliberately not sorted: order is the file's, not alphabetical.
	src := strings.ReplaceAll(`{
    "z_last": "1",
    "a_first": [
        "x",
        "y"
    ],
    "empty": [],
    "text": "quote \" backslash \\ newline \n tab \t slash / unicode é",
    "number_like": "0.4"
}
`, "\n", "\r\n")
	c, err := ParseConfig([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Keys(), []string{"z_last", "a_first", "empty", "text", "number_like"}) || c.Len() != 5 {
		t.Errorf("%v", c.Keys())
	}
	if got := string(c.Marshal()); got != src {
		t.Errorf("round trip differs:\n%q\n%q", got, src)
	}
	if c.String("text") != "quote \" backslash \\ newline \n tab \t slash / unicode é" || c.String("a_first") != "x" || c.String("missing") != "" {
		t.Errorf("values")
	}
	if !reflect.DeepEqual(c.List("a_first"), []string{"x", "y"}) || !reflect.DeepEqual(c.List("z_last"), []string{"1"}) || c.List("nope") != nil {
		t.Errorf("lists")
	}
	// LF files stay LF.
	lf, err := ParseConfig([]byte("{\n    \"a\": \"1\"\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(lf.Marshal()); got != "{\n    \"a\": \"1\"\n}\n" {
		t.Errorf("%q", got)
	}
}

func TestConfigSetInsertsSortedWhileSorted(t *testing.T) {
	c := NewConfig()
	c.SetString("m", "1")
	c.SetString("a", "1")
	c.SetString("z", "1")
	c.SetList("k", "1", "2")
	if !reflect.DeepEqual(c.Keys(), []string{"a", "k", "m", "z"}) {
		t.Errorf("%v", c.Keys())
	}
	c.SetString("m", "2") // replace keeps the place
	if c.String("m") != "2" || !reflect.DeepEqual(c.Keys(), []string{"a", "k", "m", "z"}) {
		t.Errorf("%v", c.Keys())
	}
	// An unsorted file appends new keys.
	u, _ := ParseConfig([]byte(`{"b": "1", "a": "1"}`))
	u.SetString("c", "1")
	u.SetString("0", "1")
	if !reflect.DeepEqual(u.Keys(), []string{"b", "a", "c", "0"}) {
		t.Errorf("%v", u.Keys())
	}
	if !c.Delete("k") || c.Delete("k") || !reflect.DeepEqual(c.Keys(), []string{"a", "m", "z"}) {
		t.Errorf("%v", c.Keys())
	}
	if c.Marshal()[0] != '{' || !strings.Contains(string(c.Marshal()), "\r\n") {
		t.Error("a new config writes CRLF like the app")
	}
}

func TestConfigRawValuesAndErrors(t *testing.T) {
	c, err := ParseConfig([]byte(`{"n": 5, "o": {"a": [1, 2]}, "s": "x", "dup": "1", "dup": "2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get("n"); v.Raw == nil || string(v.Raw) != "5" {
		t.Errorf("%+v", v)
	}
	if c.String("n") != "" || c.List("o") != nil {
		t.Errorf("non string values have no string form")
	}
	out := string(c.Marshal())
	if !strings.Contains(out, "\"n\": 5,") || !strings.Contains(out, `"o": {"a": [1, 2]}`) {
		t.Errorf("raw values are written back as they were:\n%s", out)
	}
	if c.String("dup") != "2" || c.Len() != 4 {
		t.Errorf("duplicate keys: last wins, one key: %d %q", c.Len(), c.String("dup"))
	}
	for _, bad := range []string{``, `[]`, `{"a": }`, `{"a": "1"`, `nope`} {
		if _, err := ParseConfig([]byte(bad)); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
	// A BOM is accepted.
	if _, err := ParseConfig([]byte("\xef\xbb\xbf{\"a\": \"1\"}")); err != nil {
		t.Error(err)
	}
	if v := String("s"); v.First() != "s" || List("a", "b").First() != "a" || List().First() != "" {
		t.Error("First")
	}
	if got := jsonString("a\x01b\bc\fd"); got != `"a\u0001b\bc\fd"` {
		t.Errorf("%q", got)
	}
}

func TestKVs(t *testing.T) {
	var k KVs
	k.Set("b", "1")
	k.Set("a", "2") // sorted so far: a goes first
	k.Set("c", "3")
	if !reflect.DeepEqual(k, KVs{{"a", "2"}, {"b", "1"}, {"c", "3"}}) {
		t.Errorf("%v", k)
	}
	unsorted := KVs{{"z", "1"}, {"a", "1"}}
	unsorted.Set("m", "1")
	if unsorted[2].Key != "m" {
		t.Errorf("%v", unsorted)
	}
	if v, ok := k.Get("b"); !ok || v != "1" || k.Value("zz") != "" {
		t.Error("Get")
	}
	if !k.Delete("b") || k.Delete("b") || len(k) != 2 {
		t.Error("Delete")
	}
	c := k.clone()
	c[0].Value = "x"
	if k[0].Value == "x" {
		t.Error("clone shares memory")
	}
}

func TestNodeTreeKeepsUnknownElements(t *testing.T) {
	src := `<?xml version="1.0"?><config><object id="1"><slic3rpe:text font="a &amp; b" note="line1&#10;line2"><child x="1"/>hello &lt;there&gt;</slic3rpe:text><empty/></object></config>`
	root, err := parseTree([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	root.child("object").write(&b, "  ")
	want := "  <object id=\"1\">\n    <slic3rpe:text font=\"a &amp; b\" note=\"line1&#10;line2\">\n      <child x=\"1\"/>\n    </slic3rpe:text>\n    <empty/>\n  </object>\n"
	// Mixed content (a child and text) keeps the children; the text of an
	// element with children is dropped by design (the app never writes it).
	if b.String() != want {
		t.Errorf("\n%q\n%q", b.String(), want)
	}
	if _, err := parseTree([]byte(`   `)); err == nil {
		t.Error("no root")
	}
	if _, err := parseTree([]byte(`<a><b></a>`)); err != nil {
		t.Logf("lenient parse: %v", err)
	}
	if got := attrEscape("a\r\nb\t\"'<&>"); got != "a&#13;&#10;b&#9;&quot;&apos;&lt;&amp;&gt;" {
		t.Errorf("%q", got)
	}
}

func TestSliceInfoVersionAndFormatting(t *testing.T) {
	for in, want := range map[string]string{"7.2.2.5483": "07.02.02.5483", "7.2.2": "07.02.02", "10.0.1.99": "10.00.01.99", "x": "x"} {
		if got := sliceInfoVersion(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	if fmtG17(0) != "0" || fmtG17(0.45) != "0.45000000000000001" || fmtG17(2.5) != "2.5" || fmtG17(10) != "10" {
		t.Errorf("fmtG17")
	}
}
