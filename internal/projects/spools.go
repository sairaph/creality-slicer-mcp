package projects

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
)

var slotRE = regexp.MustCompile(`^T[1-4][A-D]$`)

// SpoolSpec is one spool exactly as creality-k2-mcp get_filaments reports a
// CFS slot. The order of the list is the order of the project's filaments.
type SpoolSpec struct {
	Slot      string // T1A .. T4D
	CatalogID string // the 5 character Creality filament id
	Material  string // filament type: PLA, PETG ...
	Colour    string // hex
	Status    string // defined | rfid | undefined | unknown
	Name      string
}

// Spool match kinds.
const (
	MatchExact   = "exact"   // a preset whose filament_id is the spool's catalog id
	MatchGeneric = "generic" // the Generic preset of the material
)

// SpoolLink ties one project filament to the spool it was made from. It is
// kept in job.json, in the order of the filaments.
type SpoolLink struct {
	Slot      string `json:"slot"`
	CatalogID string `json:"catalog_id,omitempty"`
	Match     string `json:"match"`
	Material  string `json:"material,omitempty"`
	Name      string `json:"name,omitempty"`
	Colour    string `json:"colour,omitempty"`
	// Preset is the filament preset chosen: a link stays valid while the
	// filament of its position keeps this preset.
	Preset string `json:"preset"`
	// Note says what was assumed (a missing colour).
	Note string `json:"note,omitempty"`
}

// SpoolInfo is the link as reported by get_project and the edit replies.
type SpoolInfo struct {
	Slot, CatalogID, Match, Material, Name, Colour, Note string
}

// normColour turns #RGB, RGB, #RRGGBB, RRGGBB and #RRGGBBAA into #RRGGBB.
func normColour(c string) (string, bool) {
	c = strings.TrimPrefix(strings.TrimSpace(c), "#")
	switch len(c) {
	case 3:
		c = string([]byte{c[0], c[0], c[1], c[1], c[2], c[2]})
	case 8:
		c = c[:6]
	}
	if len(c) != 6 || !colourRE.MatchString("#"+c) {
		return "", false
	}
	return "#" + strings.ToUpper(c), true
}

// resolveSpools maps spools to filament presets of a printer: the preset whose
// filament_id is the catalog id, else the Generic preset of the material, else
// an error that lists the compatible presets of the material.
func (s *Store) resolveSpools(printer string, spools []SpoolSpec) ([]FilamentSpec, []SpoolLink, error) {
	if len(spools) == 0 {
		return nil, nil, invalidf("give at least one spool: {slot, catalog_id, material, colour}", "a project needs at least one filament")
	}
	all, err := s.cfg.Profiles.List(profiles.TypeFilament, profiles.Filter{Printer: printer, Source: "system"})
	if err != nil {
		return nil, nil, errf(CodeInternal, "", "listing the filament presets failed: %v", err)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	seen := map[string]bool{}
	var specs []FilamentSpec
	var links []SpoolLink
	for i, sp := range spools {
		label := fmt.Sprintf("spool %d", i+1)
		if sp.Slot != "" {
			label = fmt.Sprintf("spool %d (slot %s)", i+1, sp.Slot)
		}
		if slot := strings.ToUpper(strings.TrimSpace(sp.Slot)); slot != "" {
			if !slotRE.MatchString(slot) {
				return nil, nil, invalidf("CFS slots are T1A to T4D (box 1 to 4, slot A to D)", "%s has the slot %q, which is not a CFS slot", label, sp.Slot)
			}
			if seen[slot] {
				return nil, nil, invalidf("each CFS slot holds one spool: list every slot once", "%s: the slot %s is listed twice", label, slot)
			}
			seen[slot] = true
		}
		status := strings.ToLower(strings.TrimSpace(sp.Status))
		material := strings.ToUpper(strings.TrimSpace(sp.Material))
		if status == "undefined" || status == "unknown" || material == "" {
			return nil, nil, invalidf("define the slot on the printer (material and colour), or pass filaments explicitly",
				"%s has no defined filament (status %q, material %q)", label, sp.Status, sp.Material)
		}
		link := SpoolLink{Slot: strings.ToUpper(strings.TrimSpace(sp.Slot)), CatalogID: strings.TrimSpace(sp.CatalogID), Material: material, Name: sp.Name}
		colour, ok := normColour(sp.Colour)
		if !ok {
			colour = "#FFFFFF"
			if strings.TrimSpace(sp.Colour) == "" {
				link.Note = "the spool has no colour: white was used"
			} else {
				link.Note = fmt.Sprintf("the spool colour %q is not a hex colour: white was used", sp.Colour)
			}
		}
		link.Colour = colour
		var chosen *profiles.Descriptor
		if id := strings.ToLower(link.CatalogID); id != "" {
			for j := range all {
				if strings.ToLower(all[j].FilamentID) == id {
					chosen, link.Match = &all[j], MatchExact
					break
				}
			}
		}
		if chosen == nil {
			chosen = genericOf(all, material)
			link.Match = MatchGeneric
		}
		if chosen == nil {
			var names []string
			for _, d := range all {
				if strings.EqualFold(d.FilamentType, material) {
					names = append(names, d.Name)
					if len(names) == 8 {
						break
					}
				}
			}
			hint := "pass filaments explicitly with a preset name (list_presets shows them)"
			if len(names) > 0 {
				hint = "compatible " + material + " presets: " + strings.Join(names, "; ") + "; pass filaments explicitly to use one"
			}
			return nil, nil, invalidf(hint, "%s: no filament preset with id %q and no Generic %s preset is compatible with the printer %q",
				label, link.CatalogID, material, printer)
		}
		link.Preset = chosen.Name
		specs = append(specs, FilamentSpec{Preset: chosen.Name, Colour: colour})
		links = append(links, link)
	}
	return specs, links, nil
}

// genericOf picks the Generic preset of a material: "Generic PLA @..." before
// other Generic variants such as "Generic PLA-CF".
func genericOf(all []profiles.Descriptor, material string) *profiles.Descriptor {
	var loose *profiles.Descriptor
	want := "generic " + strings.ToLower(material)
	for i := range all {
		d := &all[i]
		if !strings.EqualFold(d.FilamentType, material) {
			continue
		}
		n := strings.ToLower(d.Name)
		if n == want || strings.HasPrefix(n, want+" ") || strings.HasPrefix(n, want+"@") {
			return d
		}
		if loose == nil && strings.HasPrefix(n, "generic ") {
			loose = d
		}
	}
	return loose
}

// spoolInfos are the links of a project that still hold: same position, same preset.
func (h *handle) spoolInfos(presets []string) []*SpoolInfo {
	out := make([]*SpoolInfo, len(presets))
	for i, l := range h.meta.Spools {
		if i < len(presets) && l.Preset == presets[i] {
			out[i] = &SpoolInfo{Slot: l.Slot, CatalogID: l.CatalogID, Match: l.Match, Material: l.Material, Name: l.Name, Colour: l.Colour, Note: l.Note}
		}
	}
	return out
}

func sameLinks(a, b []SpoolLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
