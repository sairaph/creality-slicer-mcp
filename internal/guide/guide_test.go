package guide

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

func TestSkillCarriesItsFrontmatter(t *testing.T) {
	text := SkillMarkdown()
	front, body := splitFrontmatter(text)
	for _, want := range []string{"name: " + Name, "description: ", "  " + Generator, "  version: "} {
		if !strings.Contains(front, want) {
			t.Errorf("frontmatter lacks %q:\n%s", want, front)
		}
	}
	if strings.TrimSpace(body) == "" || Body() != body {
		t.Errorf("Body() = %q, want the text after the frontmatter", Body())
	}
	if !IsOurs(text) {
		t.Error("the embedded skill is not recognised as ours")
	}
	if !strings.Contains(Full(), body) {
		t.Error("Full() does not contain the body")
	}
}

func TestIsOursReadsOnlyTheFrontmatter(t *testing.T) {
	cases := []struct {
		name, text string
		want       bool
	}{
		{"marker in the frontmatter", "---\nname: x\nmetadata:\n  " + Generator + "\n---\nbody\n", true},
		{"crlf line endings", "---\r\nname: x\r\nmetadata:\r\n  " + Generator + "\r\n---\r\nbody\r\n", true},
		{"marker only in the body", "---\nname: x\n---\n  " + Generator + "\n", false},
		{"no frontmatter", "  " + Generator + "\n", false},
		{"another generator", "---\nname: x\nmetadata:\n  generator: someone-else\n---\n", false},
	}
	for _, c := range cases {
		if got := IsOurs(c.text); got != c.want {
			t.Errorf("%s: IsOurs = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTopicsAndTopic(t *testing.T) {
	fsys := fstest.MapFS{
		"SKILL.md":    {Data: []byte("skill")},
		"presets.md":  {Data: []byte("presets text\r\nmore\r\n")},
		"glossary.md": {Data: []byte("glossary text")},
		"notes.txt":   {Data: []byte("not a topic")},
		"sub/deep.md": {Data: []byte("deep")},
	}
	if got := strings.Join(topicsIn(fsys), ","); got != "glossary,presets" {
		t.Errorf("topics = %q, want glossary,presets", got)
	}
	if got, ok := topicIn(fsys, "presets"); !ok || got != "presets text\nmore" {
		t.Errorf("presets = %q, %v", got, ok)
	}
	for _, bad := range []string{"", "SKILL", "nope", "../presets", "sub/deep", "presets.md", `a\b`} {
		if _, ok := topicIn(fsys, bad); ok {
			t.Errorf("topic %q was served", bad)
		}
	}
}

func TestEmbeddedTopicsAreConsistent(t *testing.T) {
	for _, name := range Topics() {
		if text, ok := Topic(name); !ok || text == "" {
			t.Errorf("listed topic %q cannot be read", name)
		}
	}
}
