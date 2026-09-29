package projects

import (
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
)

// CatalogDrift lists the settings of the installed default K2 presets (the
// default printer, its default process and its default filament) that the
// setting catalog does not know: the installed Creality Print is newer than the
// catalog. The status tool reports it; such settings are not written into new
// projects and cannot be validated. An empty list means the catalog covers the
// installed presets. It returns nil without an error when the default presets
// are not installed.
func (s *Store) CatalogDrift() ([]string, error) {
	printer, err := s.cfg.Profiles.Get(profiles.TypePrinter, DefaultPrinter)
	if err != nil {
		return nil, nil
	}
	in := composeInput{Cat: s.cfg.Catalog, Printer: printer}
	if p, err := s.cfg.Profiles.Get(profiles.TypeProcess, printer.String("default_print_profile")); err == nil {
		in.Process = p
	}
	for _, name := range printer.List("default_filament_profile") {
		if f, err := s.cfg.Profiles.Get(profiles.TypeFilament, name); err == nil {
			in.Filaments = append(in.Filaments, f)
			break
		}
	}
	return in.drift(), nil
}
