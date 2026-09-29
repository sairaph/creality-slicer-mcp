package projects

import (
	"strings"
)

// SettingValue is the current value of one project level setting.
type SettingValue struct {
	// Found is false when the project's settings do not have the key.
	Found bool
	// Value is the value as the file holds it; a vector is joined with ", ".
	Value string
	// Changed is true when the project changed the key against its presets.
	Changed bool
	// Origin says where the value comes from: "changed in this project" or
	// the name of the preset the project is based on.
	Origin string
}

// SettingValue reads one key of the project's own settings.
func (s *Store) SettingValue(ref, key string) (*SettingValue, error) {
	out := &SettingValue{}
	err := s.read(ref, func(h *handle) error {
		cfg, err := h.cfg()
		if err != nil {
			return err
		}
		v, ok := cfg.Get(key)
		if !ok {
			return nil
		}
		out.Found = true
		list := cfg.List(key)
		if isPlateVector(key) { // the file holds scene coordinates; the tools speak plate positions
			list = plateVectorRelative(cfg, key, len(h.p.Plates))
		}
		if len(list) > 1 {
			out.Value = strings.Join(list, ", ")
		} else {
			out.Value = v.First()
			if len(list) == 1 {
				out.Value = list[0]
			}
		}
		info, err := h.info()
		if err != nil {
			return err
		}
		for _, k := range info.OverrideKeys {
			if k == key {
				out.Changed = true
			}
		}
		if out.Changed {
			out.Origin = "changed in this project"
		} else {
			out.Origin = "the project's presets (process `" + info.Process + "`, printer `" + info.Printer + "`, filaments)"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
