package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/guide"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

func (s *Server) registerStatusTools() {
	addTool(s.mcpServer, "get_slicer_status", inputSchema[statusInput](nil), s.getSlicerStatus)
	addTool(s.mcpServer, "get_guide", inputSchema[guideInput](nil), s.getGuide)
}

// --- get_slicer_status ---

type statusInput struct {
	Refresh *bool `json:"refresh,omitempty"`
}

type statusFront struct {
	Installed       bool   `yaml:"installed"`
	Supported       bool   `yaml:"supported"`
	Version         string `yaml:"version,omitempty"`
	Build           string `yaml:"build,omitempty"`
	Dialect         string `yaml:"dialect,omitempty"`
	Exe             string `yaml:"exe,omitempty"`
	DataDir         string `yaml:"data_dir,omitempty"`
	ProfileVersion  string `yaml:"profile_version,omitempty"`
	ProfileSource   string `yaml:"profile_source,omitempty"`
	GUIRunning      bool   `yaml:"gui_running"`
	ProjectsDir     string `yaml:"projects_dir,omitempty"`
	CatalogVersion  string `yaml:"catalog_version,omitempty"`
	TooltipCoverage string `yaml:"tooltip_coverage,omitempty"`
	CatalogDrift    *int   `yaml:"catalog_drift,omitempty"`
	Reason          string `yaml:"reason,omitempty"`
}

func (s *Server) getSlicerStatus(ctx context.Context, _ *mcp.CallToolRequest, in statusInput) (*mcp.CallToolResult, any, error) {
	return s.SlicerStatus(ctx, boolOr(in.Refresh, false)), nil, nil
}

// SlicerStatus is get_slicer_status; the command line calls it too.
func (s *Server) SlicerStatus(ctx context.Context, refresh bool) *mcp.CallToolResult {
	install, err := s.env.install(ctx, refresh)
	if err != nil {
		return failure(ctx, "detect Creality Print", err, "")
	}
	front := statusFront{
		Installed: install.Found, Supported: install.Supported, Version: install.Version, Build: install.Build,
		Dialect: install.Dialect, Exe: install.Exe, DataDir: install.DataDir, ProfileVersion: install.ProfileVersion,
		GUIRunning: install.GUIRunning, Reason: install.Reason,
	}
	if install.Found && install.ProfileRoot != "" {
		front.ProfileSource = "data_dir"
		if filepath.Clean(install.ProfileRoot) == filepath.Clean(filepath.Join(install.Dir, "resources", "profiles")) {
			front.ProfileSource = "install"
		}
	}
	if dir, err := s.env.deps.ProjectsDir(); err == nil {
		front.ProjectsDir = dir
	}

	var textsNote string
	var drift []string
	cat, reason, cerr := s.env.catalog(ctx)
	if cerr == nil {
		st := cat.Stats()
		front.CatalogVersion = st.Version
		if st.TextsAttached {
			front.TooltipCoverage = fmt.Sprintf("%d/%d", st.TooltipsFound, st.TooltipHashes)
		} else if install.Found {
			textsNote = reason
		}
		// Catalog drift: settings the installed K2 presets set that the
		// shipped catalog does not know.
		if install.Found {
			if store, serr := s.env.profileStore(ctx); serr == nil {
				if keys, kerr := store.KeysSetBy(k2Model); kerr == nil && len(keys) > 0 {
					drift = cat.UnknownKeys(keys)
					n := len(drift)
					front.CatalogDrift = &n
				}
			}
		}
	}
	return successResult(front, statusBody(install, front, textsNote, drift, refresh))
}

func statusBody(in slicer.Install, f statusFront, textsNote string, drift []string, refreshed bool) string {
	var b strings.Builder
	switch {
	case !in.Found:
		reason := in.Reason
		if reason == "" {
			reason = "Creality Print was not found"
		}
		fmt.Fprintf(&b, "Creality Print is not available: %s.\n\n", reason)
		fmt.Fprintf(&b, "Settings, presets and guides need an installed Creality Print (presets and setting descriptions come from it); the setting names still work. Slicing needs it too. If it is installed in an unusual place, set %s to the full path of CrealityPrint.exe in the client's config for this server.\n\n", domain.EnvCmd)
		b.WriteString("Next: get_guide with {\"topic\": \"start\"} to read the workflow, or search_settings to look up a setting.")
	case !in.Supported:
		fmt.Fprintf(&b, "Creality Print %s (build %s) is installed but not supported: %s.\n\n", in.Version, in.Build, orDefault(in.Reason, "only versions 7.2 and 7.3 are supported"))
		b.WriteString("What works: get_guide, search_settings, describe_setting, browse_settings, list_presets, get_preset. What does not: slicing (slice_project). Install Creality Print 7.3 (or 7.2) to slice.\n\n")
		b.WriteString("Next: get_guide with {\"topic\": \"start\"}.")
	default:
		fmt.Fprintf(&b, "Creality Print %s (build %s) is installed and supported (dialect %s). Slicing is available.\n\n", in.Version, in.Build, in.Dialect)
		if f.ProfileVersion != "" {
			fmt.Fprintf(&b, "Presets come from profile version %s (%s).\n", f.ProfileVersion, sourceText(f.ProfileSource))
		}
		if in.GUIRunning {
			b.WriteString("The Creality Print window is open; that does not affect slicing here.\n")
		}
		if f.TooltipCoverage != "" {
			fmt.Fprintf(&b, "Setting descriptions: %s settings have one from the installed app.\n", f.TooltipCoverage)
		} else if textsNote != "" {
			fmt.Fprintf(&b, "Setting descriptions are not available (%s); describe_setting will say so.\n", textsNote)
		}
		if f.CatalogDrift != nil {
			b.WriteString(driftLine(f.CatalogVersion, in.Version, drift))
		}
		b.WriteString("\nNext: get_guide with {\"topic\": \"start\"}, then create_project or open_project.")
	}
	if refreshed {
		b.WriteString("\n\nThe install was detected again just now.")
	}
	return b.String()
}

// driftLine says whether the installed app sets settings the catalog lacks.
func driftLine(catalogVersion, appVersion string, drift []string) string {
	if len(drift) == 0 {
		return "Catalog: version " + catalogVersion + " knows every setting the installed K2 presets set.\n"
	}
	shown := drift[:min(len(drift), maxDriftKeys)]
	line := fmt.Sprintf("Catalog drift: the installed app (%s) sets %d setting(s) that catalog %s does not know: %s", appVersion, len(drift), catalogVersion, strings.Join(shown, ", "))
	if len(drift) > len(shown) {
		line += fmt.Sprintf(" and %d more", len(drift)-len(shown))
	}
	return line + "; they are passed through untouched.\n"
}

func sourceText(src string) string {
	if src == "data_dir" {
		return "the app's own data folder"
	}
	return "the install folder"
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// --- get_guide ---

type guideInput struct {
	Topic *string `json:"topic,omitempty"`
	Term  *string `json:"term,omitempty"`
}

type guideFront struct {
	Topic  string   `yaml:"topic,omitempty"`
	Topics []string `yaml:"topics,omitempty"`
	Term   string   `yaml:"term,omitempty"`
}

// topicSummaries are the one-line descriptions listed in the guide index.
var topicSummaries = map[string]string{
	"start":                "the beginner flow from a model to G-code, with the calls",
	"k2-combo":             "K2 facts, default presets, filament pitfalls",
	"multicolor-cfs":       "CFS slots, spool matching, colours, flush, prime tower, handoff to the printer",
	"supports":             "when and how to enable supports",
	"strength":             "walls, infill and where to spend material",
	"surface-quality":      "layer height, seams, ironing, fuzzy skin",
	"speed-vs-quality":     "trading time for quality",
	"modifiers-and-ranges": "per-object, per-part and per-height settings, layer actions",
	"multi-plate":          "several plates in one project",
	"calibration":          "what the app and the printer can calibrate",
	"troubleshooting":      "slice errors with exit codes, crashes, common mistakes",
	"gui-handoff":          "work that needs the Creality Print window, and the round trip back",
	"glossary":             "terms, one line each; pass term to look one up",
}

func (s *Server) getGuide(_ context.Context, _ *mcp.CallToolRequest, in guideInput) (*mcp.CallToolResult, any, error) {
	topic := strings.ToLower(strings.TrimSpace(deref(in.Topic)))
	term := strings.TrimSpace(deref(in.Term))
	names := guide.Topics()
	topicsHint := "Call get_guide with {} to list the topics."

	if term != "" {
		if topic != "" && topic != "glossary" {
			return invalidInput("term is only used with the glossary topic, not "+topic,
				"Call get_guide with {\"term\": \""+term+"\"} (no topic), or with {\"topic\": \""+topic+"\"} without term."), nil, nil
		}
		text, ok := guide.Glossary(term)
		if !ok {
			return notFound(fmt.Sprintf("No glossary term contains %q", term),
				"Call get_guide with {\"topic\": \"glossary\"} to read every term, or try a shorter word."), nil, nil
		}
		return successResult(guideFront{Topic: "glossary", Term: term}, "Glossary entries matching \""+term+"\":\n\n"+text), nil, nil
	}
	if topic == "" {
		var b strings.Builder
		b.WriteString("Guide topics (call get_guide with {\"topic\": \"<name>\"}); start with `start`:\n\n")
		for _, n := range names {
			fmt.Fprintf(&b, "- `%s`: %s\n", n, topicSummaries[n])
		}
		return successResult(guideFront{Topics: names}, strings.TrimRight(b.String(), "\n")), nil, nil
	}
	text, ok := guide.Topic(topic)
	if !ok {
		return notFound(fmt.Sprintf("No guide topic %q", topic), topicsHint+" Topics: "+strings.Join(names, ", ")+"."), nil, nil
	}
	return successResult(guideFront{Topic: topic}, text), nil, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
