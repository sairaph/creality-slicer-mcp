package profiles

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Filter narrows List. The zero value lists every selectable preset of the
// type.
type Filter struct {
	// Printer is a printer preset name: process and filament presets must be
	// compatible with it; for the printer type only that preset is listed.
	Printer string
	// PrinterModel is a printer_model value ("Creality K2", exact, ignoring
	// case): printer presets of that model are listed, and process and
	// filament presets must be compatible with at least one of them. Ignored
	// when Printer is set.
	PrinterModel string
	// FilamentType keeps filament presets whose filament_type equals it
	// (ignoring case).
	FilamentType string
	// Source is "", "all", "system" or "user".
	Source string
	// IncludeBases also lists the non selectable fdm_* bases.
	IncludeBases bool
}

// Descriptor is the short form of a preset for listings: identity plus the
// key facts of its type.
type Descriptor struct {
	Type       Type
	Name       string
	Source     string
	File       string
	Selectable bool
	Inherits   string // immediate parent

	// Printer facts.
	PrinterModel   string
	PrinterVariant string
	NozzleDiameter string  // first entry of nozzle_diameter
	BedX, BedY     float64 // size of printable_area's bounding box, 0 when unknown

	// Process facts.
	LayerHeight string
	WallLoops   string
	InfillDense string // sparse_infill_density

	// Filament facts.
	FilamentType   string
	FilamentVendor string
	FilamentID     string
	NozzleTemp     string // nozzle_temperature[0], else the initial layer one
}

// List returns the presets of a type that pass the filter, sorted by name
// (system before user on a tie).
func (s *Store) List(t Type, f Filter) ([]Descriptor, error) {
	source := strings.ToLower(strings.TrimSpace(f.Source))
	switch source {
	case "", "all":
		source = ""
	case SourceSystem, SourceUser:
	default:
		return nil, fmt.Errorf("unknown source %q: use system, user or all", f.Source)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var printers []*Preset // compatibility targets
	switch {
	case f.Printer != "":
		p, err := s.getLocked(TypePrinter, f.Printer)
		if err != nil {
			return nil, err
		}
		printers = []*Preset{p}
	case f.PrinterModel != "":
		printers = s.printersOfModelLocked(f.PrinterModel)
	}

	var out []Descriptor
	for _, src := range []string{SourceSystem, SourceUser} {
		if source != "" && source != src {
			continue
		}
		s.load(t, src)
		b := bucket(t, src)
		seen := map[*raw]bool{}
		for _, name := range s.order[b] {
			r := s.index[b][name]
			if seen[r] {
				continue
			}
			seen[r] = true
			p, err := s.flattenLocked(t, r)
			if err != nil {
				s.warn(fmt.Sprintf("%s: %v", r.file, err))
				continue
			}
			if !p.Selectable && !f.IncludeBases {
				continue
			}
			if !s.matchesLocked(p, f, printers) {
				continue
			}
			out = append(out, describe(p))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].Source == SourceSystem && out[j].Source != SourceSystem
	})
	return out, nil
}

// printersOfModelLocked returns every selectable printer preset of a model.
func (s *Store) printersOfModelLocked(model string) []*Preset {
	var out []*Preset
	for _, src := range []string{SourceSystem, SourceUser} {
		s.load(TypePrinter, src)
		b := bucket(TypePrinter, src)
		seen := map[*raw]bool{}
		for _, name := range s.order[b] {
			r := s.index[b][name]
			if seen[r] {
				continue
			}
			seen[r] = true
			p, err := s.flattenLocked(TypePrinter, r)
			if err != nil || !p.Selectable {
				continue
			}
			if strings.EqualFold(p.String("printer_model"), model) {
				out = append(out, p)
			}
		}
	}
	return out
}

func (s *Store) matchesLocked(p *Preset, f Filter, printers []*Preset) bool {
	switch p.Type {
	case TypePrinter:
		if f.Printer != "" && p.Name != f.Printer {
			return false
		}
		if f.Printer == "" && f.PrinterModel != "" && !strings.EqualFold(p.String("printer_model"), f.PrinterModel) {
			return false
		}
		return true
	case TypeFilament:
		if f.FilamentType != "" && !strings.EqualFold(p.String("filament_type"), f.FilamentType) {
			return false
		}
	}
	if f.Printer == "" && f.PrinterModel == "" {
		return true
	}
	for _, pr := range printers {
		if ok, _ := Compatible(*pr, *p); ok {
			return true
		}
	}
	return false
}

// describe extracts the listing facts of a flattened preset.
func describe(p *Preset) Descriptor {
	d := Descriptor{
		Type: p.Type, Name: p.Name, Source: p.Source, File: p.File,
		Selectable: p.Selectable, Inherits: p.Parent(),
	}
	switch p.Type {
	case TypePrinter:
		d.PrinterModel = p.String("printer_model")
		d.PrinterVariant = p.String("printer_variant")
		d.NozzleDiameter = p.String("nozzle_diameter")
		d.BedX, d.BedY = boundingSize(p.String("printable_area"))
	case TypeProcess:
		d.LayerHeight = p.String("layer_height")
		d.WallLoops = p.String("wall_loops")
		d.InfillDense = p.String("sparse_infill_density")
	case TypeFilament:
		d.FilamentType = p.String("filament_type")
		d.FilamentVendor = p.String("filament_vendor")
		d.FilamentID = p.String("filament_id")
		d.NozzleTemp = p.String("nozzle_temperature")
		if d.NozzleTemp == "" {
			d.NozzleTemp = p.String("nozzle_temperature_initial_layer")
		}
	}
	return d
}

// boundingSize measures a printable_area value ("0x0,260x0,260x260,0x260").
func boundingSize(area string) (x, y float64) {
	var minX, minY, maxX, maxY float64
	n := 0
	for _, pt := range strings.Split(area, ",") {
		xy := strings.SplitN(strings.TrimSpace(pt), "x", 2)
		if len(xy) != 2 {
			return 0, 0
		}
		px, err1 := strconv.ParseFloat(xy[0], 64)
		py, err2 := strconv.ParseFloat(xy[1], 64)
		if err1 != nil || err2 != nil {
			return 0, 0
		}
		if n == 0 || px < minX {
			minX = px
		}
		if n == 0 || px > maxX {
			maxX = px
		}
		if n == 0 || py < minY {
			minY = py
		}
		if n == 0 || py > maxY {
			maxY = py
		}
		n++
	}
	return maxX - minX, maxY - minY
}
