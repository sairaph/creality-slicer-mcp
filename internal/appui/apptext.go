package appui

import (
	"errors"
	"regexp"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// toolNames are the MCP tools. They are written for an AI client; the app never
// shows one. A test checks that this list holds every registered tool.
var toolNames = []string{
	"analyze_toolpaths", "open_in_app", "add_model", "update_object", "group_objects", "remove_object", "remove_part",
	"update_settings", "set_presets", "add_modifier", "set_height_ranges", "set_layer_actions", "manage_plates",
	"list_presets", "get_preset", "create_project", "open_project", "list_projects", "get_project", "export_project",
	"delete_project", "search_settings", "describe_setting", "browse_settings", "slice_project", "get_slice_status",
	"get_slice_report", "get_slicer_status", "get_guide", "get_view",
}

var toolRE = regexp.MustCompile(`\b(` + strings.Join(toolNames, "|") + `)\b`)

// appHint says what to do in the app where a hint names a tool: the hint is
// replaced by the action of the app that does the same.
func appHint(hint string) string {
	hint = strings.ReplaceAll(hint, "repeat the call", "try again")
	name := toolRE.FindString(hint)
	switch name {
	case "":
		return hint
	case "delete_project":
		return "Close the Creality Print window or the program that holds the file, then press d again."
	case "open_in_app":
		return "Press o again."
	case "export_project":
		return "Press x again."
	case "get_slicer_status":
		return "Open Slicer status from the menu to see what is missing."
	case "slice_project", "get_slice_status", "get_slice_report":
		return "Wait for the slice to end, or ask your AI client to slice the project, then try again."
	}
	return "Ask your AI client to do this, or try again."
}

// appMessage keeps a tool name out of a message.
func appMessage(msg string) string { return toolRE.ReplaceAllString(msg, "that action") }

// ErrText is an error as the app shows it in a notice: its message and, for a
// projects error, its hint, with every tool name turned into an app action.
func ErrText(err error) string {
	var pe *projects.Error
	if errors.As(err, &pe) {
		msg := appMessage(pe.Message)
		if hint := appHint(pe.Hint); hint != "" {
			return strings.TrimRight(msg, ".") + ". " + hint
		}
		return msg
	}
	return appMessage(err.Error())
}
