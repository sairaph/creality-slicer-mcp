// Package doctorchecks implements creality-slicer-mcp's own health checks.
// Each check satisfies github.com/sairaph/mcp-wizard/doctor.Check. A check
// reads: none starts a program or changes what the user has. The one probe
// that touches the disk, DataDirCheck, creates a temporary file in an
// existing folder and removes it again at once.
package doctorchecks

import (
	"github.com/sairaph/mcp-wizard/doctor"
)

// Checks builds every check this package provides with the real
// dependencies, for main.go's newDoctor to add to its doctor.Runner alongside
// the framework's own checks (executable, PATH, AI clients, update).
func Checks() []doctor.Check { return ChecksWith(NewDeps()) }

// ChecksWith is Checks with the given dependencies; tests pass fakes. The
// order is the order of the report: settings, data folder, then Creality Print,
// its data, the profiles, the setting descriptions and the catalog.
func ChecksWith(d Deps) []doctor.Check {
	c := &creality{deps: d}
	checks := []doctor.Check{SettingsCheck{}, DataDirCheck{}}
	return append(checks, c.checks()...)
}
