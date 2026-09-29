package profiles

import "sort"

// fileMetadata are the keys of a preset file that describe the file, not a
// setting, so KeysSetBy leaves them out: no settings catalog lists them.
var fileMetadata = map[string]bool{
	"type": true, "name": true, "from": true, "instantiation": true, "inherits": true,
	"setting_id": true, "filament_id": true, "version": true, "base_id": true,
	"compatible_printers": true, "compatible_printers_condition": true,
	"compatible_prints": true, "compatible_prints_condition": true,
	"default_print_profile": true, "default_filament_profile": true, "default_bed_type": true,
	"model_id": true, "nozzle_diameter": true, "printer_model": true, "printer_variant": true,
	"printer_technology": true, "family": true, "bed_model": true, "bed_texture": true,
	"hotend_model": true, "cloud_id": true, "is_custom_defined": true, "user_id": true,
	"updated_time": true, "sync_info": true, "description": true,
	// Machine model descriptors (7.3 adds default_materials and machine_tech).
	"default_materials": true, "machine_tech": true,
}

// KeysSetBy returns, sorted and without duplicates, every setting key set by
// the flattened selectable presets of a printer model: its printer presets
// and the process and filament presets compatible with at least one of them.
// Keys that only describe the preset file (name, inherits, setting_id, ...)
// are left out. A model with no printer preset gives no keys.
func (s *Store) KeysSetBy(printerModel string) ([]string, error) {
	keys := map[string]bool{}
	for _, t := range Types {
		descs, err := s.List(t, Filter{PrinterModel: printerModel})
		if err != nil {
			return nil, err
		}
		for _, d := range descs {
			p, err := s.Get(t, d.Name)
			if err != nil {
				continue
			}
			for k := range p.Values {
				if !fileMetadata[k] {
					keys[k] = true
				}
			}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
