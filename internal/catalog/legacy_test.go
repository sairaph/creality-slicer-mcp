package catalog

import (
	"strings"
	"testing"
)

func TestLegacyKeysFromEmbeddedCatalogs(t *testing.T) {
	for _, v := range Versions() {
		t.Run(v, func(t *testing.T) {
			c := mustLoad(t, v)
			if n := len(c.LegacyKeys()); n < 60 {
				t.Fatalf("only %d legacy keys", n)
			}
			// the three keys that showed up as drift in real presets
			for key, kind := range map[string]string{"adaptive_layer_height": "ignored", "bed_type": "retired", "wall_infill_order": "renamed"} {
				l, ok := c.LegacyInfo(key)
				if !ok || l.Kind != kind || l.Source == "" {
					t.Errorf("%s: %+v %v, want kind %s with a source", key, l, ok, kind)
				}
				if !c.IsLegacy(key) {
					t.Errorf("IsLegacy(%s) false", key)
				}
			}
			if repl, ok := c.Legacy("wall_infill_order"); !ok || repl != "wall_sequence" {
				t.Errorf("wall_infill_order -> %q %v", repl, ok)
			}
			if repl, ok := c.Legacy("adaptive_layer_height"); !ok || repl != "" {
				t.Errorf("an ignored key has no replacement: %q %v", repl, ok)
			}
			if _, ok := c.Legacy("layer_height"); ok || c.IsLegacy("layer_height") {
				t.Error("a defined setting is not legacy")
			}
			for _, l := range c.LegacyKeys() {
				if _, defined := c.Get(l.Key); defined {
					t.Errorf("legacy key %s is also defined", l.Key)
				}
				switch l.Kind {
				case "renamed":
					if l.Replacement == "" {
						t.Errorf("%s: renamed without replacement", l.Key)
					} else if _, ok := c.Get(l.Replacement); !ok && !c.IsLegacy(l.Replacement) {
						t.Errorf("%s renamed to unknown key %s", l.Key, l.Replacement)
					}
				case "dropped", "ignored", "retired":
					if l.Replacement != "" {
						t.Errorf("%s (%s) has a replacement", l.Key, l.Kind)
					}
				default:
					t.Errorf("%s: kind %q", l.Key, l.Kind)
				}
				if !strings.Contains(l.Source, ".cpp:") {
					t.Errorf("%s: source %q", l.Key, l.Source)
				}
			}
			got := c.UnknownKeys([]string{"adaptive_layer_height", "bed_type", "wall_infill_order", "layer_height", "brand_new_key"})
			if len(got) != 1 || got[0] != "brand_new_key" {
				t.Errorf("UnknownKeys must exclude legacy keys: %q", got)
			}
		})
	}
}

func TestLegacyRenamesOfVersion73(t *testing.T) {
	c := mustLoad(t, "7.3.0")
	for old, want := range map[string]string{"intelligent_infill": "ai_infill", "field_cell_type": "cell_type", "perimeter_extruder": "wall_filament", "enable_wipe_tower": "enable_prime_tower"} {
		if got, ok := c.Legacy(old); !ok || got != want {
			t.Errorf("%s -> %q %v, want %s", old, got, ok, want)
		}
	}
	// 7.2 has no field infill: its old names are not legacy there
	if mustLoad(t, "7.2.1").IsLegacy("intelligent_infill") {
		t.Error("intelligent_infill is not a legacy key of 7.2.1")
	}
}

func TestFromJSONRejectsLegacyKeyThatIsDefined(t *testing.T) {
	bad := `{"format":1,"options":[{"key":"a"}],"legacy_keys":[{"key":"a","kind":"ignored"}]}`
	if _, err := FromJSON([]byte(bad)); err == nil {
		t.Error("accepted a legacy key that is a defined option")
	}
	ok := `{"format":1,"options":[{"key":"a"}],"legacy_keys":[{"key":"old","kind":"renamed","replacement":"a"}]}`
	c, err := FromJSON([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if r, found := c.Legacy("old"); !found || r != "a" || len(c.UnknownKeys([]string{"old", "a", "x"})) != 1 {
		t.Errorf("legacy round trip: %q %v", r, found)
	}
}
