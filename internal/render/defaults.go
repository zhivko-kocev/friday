package render

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// defaultsAdapters lists every adapter Defaults can project onto. Unlike
// MCP/Permissions, this set is not "every built-in adapter": oh-my-pi has
// a model/effort/theme/telemetry knob just like the other five, but it all
// lives in config.yml (YAML), and friday has no merge-yaml strategy to
// reach it with — so oh-my-pi carries no defaults.yaml rule at all and is
// absent from this set entirely.
var defaultsAdapters = map[string]bool{
	"claude": true, "codex": true, "copilot": true, "opencode": true, "gemini": true,
}

// effortRank indexes spec's union ladder for ClampEffort's narrowing.
var effortRank = map[string]int{
	spec.EffortOff: 0, spec.EffortMinimal: 1, spec.EffortLow: 2, spec.EffortMedium: 3,
	spec.EffortHigh: 4, spec.EffortXHigh: 5, spec.EffortMax: 6,
}

// ClampEffort narrows effort to the inclusive [floor, ceil] slice of the
// union ladder, reporting whether narrowing actually changed the value so a
// caller reports a clamp only when one truly happened.
func ClampEffort(effort, floor, ceil string) (clamped string, changed bool) {
	if effortRank[effort] < effortRank[floor] {
		return floor, true
	}
	if effortRank[effort] > effortRank[ceil] {
		return ceil, true
	}
	return effort, false
}

// copilotThemes are the only values Copilot CLI's own settings.json schema
// accepts; anything else is a rejected config on Copilot's side, so friday
// refuses to write it rather than shipping a value the agent will reject.
var copilotThemes = []string{"default", "github", "dim", "high-contrast", "colorblind"}

// Defaults projects d onto adapter's native settings surface for the rule
// whose destination is relTo (the rule's own To, e.g. "settings.json" or
// "config.toml") — every adapter but OpenCode has exactly one settings
// surface and ignores relTo; OpenCode splits model/telemetry into
// opencode.json and theme into tui.json (see the opencode preset's mcp.yaml
// rule comment), so it alone branches on which file this call renders for.
// payload is the file content a merge-json rule (claude, copilot, opencode,
// gemini) or merge-toml rule (codex) writes; notes reports every clamp
// and every canonical field that target cannot represent at all, fully
// worded and ready to surface verbatim — unlike MCP/Permissions' unmapped
// (bare entity names folded into a shared "cannot represent:" wrapper by
// the caller), a clamp is not an omission and needs its own words.
func Defaults(adapter, relTo string, d spec.Defaults) (payload []byte, notes []string, err error) {
	if !defaultsAdapters[adapter] {
		return nil, nil, fmt.Errorf("render.Defaults: unknown adapter %q", adapter)
	}
	out := map[string]any{}
	switch adapter {
	case "claude":
		notes = defaultsClaude(out, d)
	case "codex":
		notes, err = defaultsCodex(out, d)
	case "copilot":
		notes, err = defaultsCopilot(out, d)
	case "opencode":
		switch relTo {
		case "opencode.json":
			notes, err = defaultsOpenCodeSettings(out, d)
		case "tui.json":
			notes = defaultsOpenCodeTUI(out, d)
		default:
			return nil, nil, fmt.Errorf("render.Defaults: opencode target %q must be opencode.json or tui.json", relTo)
		}
	case "gemini":
		notes = defaultsGemini(out, d)
	}
	if err != nil {
		return nil, nil, err
	}

	// Overrides are a per-adapter escape hatch merged in last, so they win
	// over anything the canonical fields above already set — an escape
	// hatch that loses to the normal path isn't one. OpenCode's split
	// applies them only to its main settings file: the schema has no
	// per-file distinction, and every documented override example
	// (codex's model_verbosity) is settings-shaped, not theme-file-shaped.
	if adapter != "opencode" || relTo == "opencode.json" {
		// An override keyed by a name no adapter answers to reaches no file
		// at all. Staying silent there loses real user configuration across
		// the pre-v1.0 adapter renames (antigravity, pi), so name it.
		for _, key := range sortedStringKeys(d.Overrides) {
			if !defaultsAdapters[key] {
				notes = append(notes, fmt.Sprintf("defaults.yaml: overrides.%s is not an adapter friday renders defaults for (%s); those settings are written nowhere — run `friday migrate` if it is a pre-v1.0 adapter name", key, strings.Join(sortedStringKeys(defaultsAdapters), ", ")))
			}
		}
		for k, v := range d.Overrides[adapter] {
			out[k] = v
		}
	}

	if len(out) == 0 {
		return nil, notes, nil
	}
	if adapter == "codex" {
		payload, err = toml.Marshal(out)
		return payload, notes, err
	}
	payload, err = json.Marshal(out)
	return payload, notes, err
}

// defaultsClaude renders Claude's settings.json shape: a bare "model", an
// "effortLevel" clamped to low..xhigh, a bare "theme" (no enum — unlike
// Copilot's), a "statusLine" object, and telemetry:false as an
// env.DISABLE_TELEMETRY entry.
func defaultsClaude(out map[string]any, d spec.Defaults) []string {
	var notes []string
	if d.Model != nil && d.Model.Name != "" {
		out["model"] = d.Model.Name
		if d.Model.Provider != "" {
			notes = append(notes, "claude: model.provider has no settings key (the model name carries it); not written")
		}
		if d.Model.Effort != "" {
			clamped, changed := ClampEffort(d.Model.Effort, spec.EffortLow, spec.EffortXHigh)
			out["effortLevel"] = clamped
			if changed {
				notes = append(notes, fmt.Sprintf("claude: effort %q clamped to %q (claude only accepts low..xhigh)", d.Model.Effort, clamped))
			}
		}
	}
	if d.Theme != "" {
		out["theme"] = d.Theme
	}
	if d.Statusline != nil {
		out["statusLine"] = statusLinePayload(d.Statusline)
	}
	if d.Telemetry != nil && !*d.Telemetry {
		out["env"] = map[string]any{"DISABLE_TELEMETRY": "1"}
	}
	return notes
}

// statusLinePayload renders claude's and copilot's shared statusLine object.
// padding and refresh_interval are optional in canonical form, and both
// adapters document refreshInterval as a number of seconds with a minimum
// of 1 — a written zero is an invalid value that costs the whole statusLine
// object, so an unset field is omitted rather than defaulted.
func statusLinePayload(s *spec.Statusline) map[string]any {
	out := map[string]any{"type": "command", "command": s.Command}
	if s.Padding != 0 {
		out["padding"] = s.Padding
	}
	if s.RefreshInterval != 0 {
		out["refreshInterval"] = s.RefreshInterval
	}
	return out
}

func defaultsCodex(out map[string]any, d spec.Defaults) ([]string, error) {
	var notes []string
	if d.Model != nil && d.Model.Name != "" {
		out["model"] = d.Model.Name
		if d.Model.Provider != "" {
			out["model_provider"] = d.Model.Provider
		}
		if d.Model.Effort != "" {
			// Codex's model_reasoning_effort enum is minimal..xhigh; the
			// union ladder's "off" and "max" are outside it.
			clamped, changed := ClampEffort(d.Model.Effort, spec.EffortMinimal, spec.EffortXHigh)
			out["model_reasoning_effort"] = clamped
			if changed {
				notes = append(notes, fmt.Sprintf("codex: effort %q clamped to %q (codex only accepts minimal..xhigh)", d.Model.Effort, clamped))
			}
		}
	}
	if d.Theme != "" {
		out["tui"] = map[string]any{"theme": d.Theme}
	}
	if d.Statusline != nil {
		notes = append(notes, "codex: statusline has no command-based equivalent (tui.status_line is a curated item list); not written")
	}
	if d.Telemetry != nil && !*d.Telemetry {
		out["analytics"] = map[string]any{"enabled": false}
	}
	return notes, nil
}

func defaultsCopilot(out map[string]any, d spec.Defaults) ([]string, error) {
	var notes []string
	if d.Model != nil && d.Model.Name != "" {
		out["model"] = d.Model.Name
		if d.Model.Provider != "" {
			notes = append(notes, "copilot: model.provider has no settings key (the model name carries it); not written")
		}
		if d.Model.Effort != "" {
			clamped, changed := ClampEffort(d.Model.Effort, spec.EffortLow, spec.EffortXHigh)
			out["effortLevel"] = clamped
			if changed {
				notes = append(notes, fmt.Sprintf("copilot: effort %q clamped to %q (copilot only accepts low..xhigh)", d.Model.Effort, clamped))
			}
		}
	}
	if d.Theme != "" {
		if !slices.Contains(copilotThemes, d.Theme) {
			return nil, fmt.Errorf("render.Defaults: copilot theme %q must be one of %s", d.Theme, strings.Join(copilotThemes, ", "))
		}
		out["theme"] = d.Theme
	}
	if d.Statusline != nil {
		out["statusLine"] = statusLinePayload(d.Statusline)
	}
	if d.Telemetry != nil && !*d.Telemetry {
		notes = append(notes, "copilot: telemetry opt-out is not configurable at user scope (env var or MDM policy only); no key written")
	}
	return notes, nil
}

func defaultsOpenCodeSettings(out map[string]any, d spec.Defaults) ([]string, error) {
	var notes []string
	if d.Model != nil && d.Model.Name != "" {
		if d.Model.Provider == "" {
			return nil, fmt.Errorf("render.Defaults: opencode requires model.provider (model is addressed as provider/name)")
		}
		out["model"] = d.Model.Provider + "/" + d.Model.Name
		if d.Model.Effort != "" {
			notes = append(notes, "opencode: effort has no runtime equivalent (only a catalog-defined model variant, not an enum); not written")
		}
	}
	if d.Statusline != nil {
		notes = append(notes, "opencode: statusline has no equivalent; not written")
	}
	if d.Telemetry != nil && !*d.Telemetry {
		out["experimental"] = map[string]any{"openTelemetry": false}
	}
	return notes, nil
}

func defaultsOpenCodeTUI(out map[string]any, d spec.Defaults) []string {
	if d.Theme != "" {
		out["theme"] = d.Theme
	}
	return nil
}

func defaultsGemini(out map[string]any, d spec.Defaults) []string {
	var notes []string
	if d.Model != nil && d.Model.Name != "" {
		out["model"] = map[string]any{"name": d.Model.Name}
		if d.Model.Provider != "" {
			notes = append(notes, "gemini: model.provider has no settings key (the model name carries it); not written")
		}
		if d.Model.Effort != "" {
			notes = append(notes, "gemini: effort has no top-level equivalent (only a per-alias modelConfigs.aliases.*.thinkingConfig override); not written")
		}
	}
	if d.Theme != "" {
		out["ui"] = map[string]any{"theme": d.Theme}
	}
	if d.Statusline != nil {
		notes = append(notes, "gemini: statusline has no command-based equivalent (only a curated ui.footer.items list); not written")
	}
	if d.Telemetry != nil && !*d.Telemetry {
		out["privacy"] = map[string]any{"usageStatisticsEnabled": false}
	}
	return notes
}
