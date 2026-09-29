package profiles

import (
	"sort"
	"testing"
)

func hasKey(keys []string, k string) bool {
	i := sort.SearchStrings(keys, k)
	return i < len(keys) && keys[i] == k
}

func TestKeysSetBy(t *testing.T) {
	s := open(t)
	keys, err := s.KeysSetBy("Model A")
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(keys) {
		t.Error("keys are not sorted")
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] == keys[i-1] {
			t.Errorf("key %q is listed twice", keys[i])
		}
	}
	// Settings of the printer, a compatible process and a compatible filament.
	for _, want := range []string{"nozzle_volume", "layer_height", "filament_type"} {
		if !hasKey(keys, want) {
			t.Errorf("KeysSetBy(Model A) lacks %q: %v", want, keys)
		}
	}
	// File metadata is not a setting.
	for _, bad := range []string{"name", "inherits", "type", "from", "setting_id", "instantiation", "compatible_printers", "printer_model", "nozzle_diameter"} {
		if hasKey(keys, bad) {
			t.Errorf("metadata key %q was returned", bad)
		}
	}
	// The model name is matched ignoring case; another model has its own keys.
	lower, _ := s.KeysSetBy("model a")
	if len(lower) != len(keys) {
		t.Errorf("model name is case sensitive: %d vs %d keys", len(lower), len(keys))
	}
	none, err := s.KeysSetBy("No Such Model")
	if err != nil || len(none) != 0 {
		t.Errorf("unknown model: %v, %v", none, err)
	}
}

// Machine model descriptors are not settings: a 7.3 model file must not read
// as catalog drift.
func TestKeysSetByLeavesModelDescriptorsOut(t *testing.T) {
	for _, k := range []string{"bed_model", "bed_texture", "default_bed_type", "default_materials", "family", "hotend_model", "machine_tech", "model_id"} {
		if !fileMetadata[k] {
			t.Errorf("%q is not treated as file metadata", k)
		}
	}
}
