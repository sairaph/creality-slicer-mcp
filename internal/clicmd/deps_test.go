package clicmd

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func TestWithDefaultsFillsOnlyUnsetFields(t *testing.T) {
	var out, errOut bytes.Buffer
	yes := func() bool { return true }
	d := Deps{Stdout: &out, Stderr: &errOut, IsInteractive: yes}.withDefaults()
	if d.Stdout != &out || d.Stderr != &errOut || !d.IsInteractive() {
		t.Error("withDefaults replaced a field the caller set")
	}
	def := NewDefaultDeps()
	if def.Stdout == nil || def.Stderr == nil || def.IsInteractive == nil {
		t.Errorf("NewDefaultDeps left a field unset: %+v", def)
	}
}

func TestParseFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"good", []string{"-n", "3"}, -1},
		{"help", []string{"-h"}, 0},
		{"unknown flag", []string{"-bogus"}, 2},
		{"bad value", []string{"-n", "x"}, 2},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.Int("n", 0, "a number")
		var errOut bytes.Buffer
		if got := parseFlags(fs, c.args, &errOut); got != c.want {
			t.Errorf("%s: parseFlags = %d, want %d", c.name, got, c.want)
		}
		if c.want == 2 && errOut.Len() == 0 {
			t.Errorf("%s: nothing was written to stderr", c.name)
		}
	}
}

func TestUsageError(t *testing.T) {
	var errOut bytes.Buffer
	if code := usageError(&errOut, "missing %s", "name"); code != 2 || !strings.Contains(errOut.String(), "missing name") {
		t.Errorf("code %d, stderr %q", code, errOut.String())
	}
}
