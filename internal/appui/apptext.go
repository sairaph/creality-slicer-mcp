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

// toolList is the alternation of the tool names, for the clause patterns.
var toolList = strings.Join(toolNames, "|")

var (
	// textRules say what the app wording is where a text names a tool and
	// the app has its own way to the same thing.
	textRules = []struct {
		re   *regexp.Regexp
		with string
	}{
		{regexp.MustCompile(`slice_project reports the tower grams and the flush per plate after slicing`), "the slice report shows the tower grams and the flush per plate"},
		{regexp.MustCompile(`slice_project with arrange true packs the plate with the needed clearance`), "slicing again with the objects arranged packs the plate with the needed clearance"},
		{regexp.MustCompile(`: update_settings \{.*?\}\} then slice_project with arrange true \(`), ": set the plate to print by object and slice it again with the objects arranged ("},
		{regexp.MustCompile(`when set_presets rebuilds its settings`), "when its presets are replaced"},
		{regexp.MustCompile(`describe_setting will say so`), "the setting texts will say so"},
		{regexp.MustCompile(`describe_setting will say a setting has no description`), "a setting will show no description"},
	}
	// Clauses that still name a tool are dropped: a parenthesis, the part
	// after a semicolon, or a whole sentence.
	parenClause = regexp.MustCompile(`\s*\([^()]*\b(?:` + toolList + `)\b[^()]*\)`)
	semiClause  = regexp.MustCompile(`;\s*[^;.]*\b(?:` + toolList + `)\b[^;.]*`)
	spaceRun    = regexp.MustCompile(`\s{2,}`)
)

// AppText is a text of the server, a project or a slice as the app shows it:
// those are written for an AI client and name its tools. A reference to a tool
// becomes app wording where a rule has one and is dropped where there is none.
// appMessage is only the last guard; a test checks that the rules alone leave
// no tool name.
func AppText(s string) string { return appMessage(rewriteText(s)) }

// rewriteText applies the rules of AppText.
func rewriteText(s string) string {
	if !toolRE.MatchString(s) {
		return s
	}
	for _, r := range textRules {
		s = r.re.ReplaceAllString(s, r.with)
	}
	s = parenClause.ReplaceAllString(s, "")
	s = semiClause.ReplaceAllString(s, "")
	s = dropSentences(s)
	return spaceRun.ReplaceAllString(strings.TrimSpace(s), " ")
}

// dropSentences removes the sentences that still name a tool. A sentence ends
// at a period followed by a space or the end of the text.
func dropSentences(s string) string {
	var kept []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t' || s[i+1] == '\n') {
			if sent := s[start : i+1]; !toolRE.MatchString(sent) {
				kept = append(kept, strings.TrimSpace(sent))
			}
			start = i + 1
		}
	}
	if rest := s[start:]; strings.TrimSpace(rest) != "" && !toolRE.MatchString(rest) {
		kept = append(kept, strings.TrimSpace(rest))
	}
	return strings.Join(kept, " ")
}
