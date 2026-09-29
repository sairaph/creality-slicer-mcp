package slicer

import (
	"errors"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
)

// The dialect is exercised with the real option catalog: the keys that the
// catalog gives command line flags and that the request also sets through a
// dedicated field must be refused, and vector booleans must be one token.
func TestDialectWithTheRealCatalog(t *testing.T) {
	cat, err := catalog.Load("7.2.1")
	if err != nil {
		t.Skipf("catalog: %v", err)
	}
	d, err := NewDialect("v72", cat.CLIFlag)
	if err != nil {
		t.Fatal(err)
	}
	base := func() SliceRequest { return SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o")} }

	// Identity and project keys that have a CLI flag are refused whatever the request holds.
	for _, key := range []string{"printer_settings_id", "print_settings_id", "filament_settings_id", "preset_name", "preset_names", "printer_select_mac", "filament_ids"} {
		if _, _, ok := cat.CLIFlag(key); !ok {
			continue // not in this catalog build: nothing to refuse
		}
		r := base()
		r.Overrides = map[string]string{key: "x"}
		if _, err := d.BuildSliceArgs(r); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", key, err)
		}
	}
	// filament_colour twice would double the colour list.
	if _, _, ok := cat.CLIFlag("filament_colour"); ok {
		r := base()
		r.FilamentColours = []string{"#FFFFFF"}
		r.Overrides = map[string]string{"filament_colour": "#000000"}
		if _, err := d.BuildSliceArgs(r); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%v", err)
		}
	}
	// A vector of booleans with a flag is one --flag=0,1 token, whatever the catalog says about it.
	if flag, _, ok := cat.CLIFlag("filament_is_support"); ok {
		r := base()
		r.Overrides = map[string]string{"filament_is_support": "0,1"}
		args, err := d.BuildSliceArgs(r)
		if err != nil {
			t.Fatal(err)
		}
		want := "--" + cliSpelling(flag, "filament_is_support") + "=0,1"
		if !contains(args, want) {
			t.Errorf("%q does not contain %q", args, want)
		}
	}
	// A scalar boolean and a number.
	r := base()
	r.Overrides = map[string]string{"enable_support": "1", "wall_loops": "3"}
	args, err := d.BuildSliceArgs(r)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(args, "--enable-support=1") || !contains(args, "--wall-loops") {
		t.Errorf("%q", args)
	}
}
