package projects

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// appBedTypes maps the BedType enum Creality Print stores (PrintConfig.hpp:
// btDefault 0, btPC, btEP, btPEI, btPTE, btDEF, btER) to the curr_bed_type
// names (s_keys_map_BedType: "Default Plate" is not a plate type a project can
// hold, btDEF is "Customized Plate", btER "Epoxy Resin Plate").
var appBedTypes = map[int]string{
	1: "Cool Plate", 2: "Engineering Plate", 3: "High Temp Plate",
	4: "Textured PEI Plate", 5: "Customized Plate", 6: "Epoxy Resin Plate",
}

// appBedType is the bed type Creality Print last used for a printer preset: the
// curr_bed_type of the orca_presets entry whose machine is the preset, in
// <data dir>/Creality.conf. The file also holds account data, so only those two
// fields are read and nothing else is kept or reported. It reads only; a missing
// or unreadable file, an unknown printer or an unknown value gives false.
func (s *Store) appBedType(printer string) (string, bool) {
	dir := s.cfg.Install.DataDir
	if dir == "" || printer == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, "Creality.conf"))
	if err != nil {
		return "", false
	}
	// The file is a JSON object followed by a checksum comment line: decode the
	// first value only.
	var doc struct {
		Presets []struct {
			Machine string          `json:"machine"`
			Bed     json.RawMessage `json:"curr_bed_type"`
		} `json:"orca_presets"`
	}
	if err := json.NewDecoder(strings.NewReader(string(data))).Decode(&doc); err != nil {
		return "", false
	}
	// The app overwrites on every entry of the machine (AppConfig.cpp), so the
	// last one wins.
	var last json.RawMessage
	for _, p := range doc.Presets {
		if p.Machine == printer {
			last = p.Bed
		}
	}
	if last == nil {
		return "", false
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimSpace(string(last)), `"`))
	if err != nil {
		return "", false
	}
	name, ok := appBedTypes[n]
	return name, ok
}
