package doctorchecks

import (
	"context"

	"github.com/sairaph/mcp-wizard/doctor"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// SettingsCheck reports the settings read from the environment, and fails on
// a value the MCP server would refuse to start with.
type SettingsCheck struct{}

func (SettingsCheck) Name() string { return "Settings" }

func (c SettingsCheck) Run(_ context.Context) doctor.Result {
	settings, err := domain.SettingsFromEnv()
	if err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: err.Error() + " (the MCP server will not start)"}
	}
	if settings.Cmd == "" {
		return doctor.Result{Name: c.Name(), Status: doctor.OK, Detail: domain.EnvCmd + " is not set; Creality Print is detected"}
	}
	return doctor.Result{Name: c.Name(), Status: doctor.OK, Detail: "Creality Print: " + settings.Cmd + " (from " + domain.EnvCmd + ")"}
}
