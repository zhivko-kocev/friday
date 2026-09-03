package render

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

func TestDefaultsUnknownAdapterErrors(t *testing.T) {
	if _, _, err := Defaults("antigravity", "settings.json", spec.Defaults{}); err == nil {
		t.Error("expected error for unknown adapter")
	}
}

// TestDefaultsOhMyPiHasNoSurfaceErrors covers a different case from the
// wholly-unknown-name check above: oh-my-pi is a real preset (rules.go,
// mcp.go, and permissions.go all know it), but it carries no defaults.yaml
// rule at all — config.yml is YAML, and friday has no merge-yaml strategy
// to reach it with yet (see defaultsAdapters' doc comment) — so it must
// still be rejected here, the same way a name friday has never heard of
// would be.
func TestDefaultsOhMyPiHasNoSurfaceErrors(t *testing.T) {
	if _, _, err := Defaults("oh-my-pi", "settings.json", spec.Defaults{}); err == nil {
		t.Error("expected error: oh-my-pi has no defaults surface")
	}
}

func TestDefaultsEmptyProducesNilPayloadAndNoNotes(t *testing.T) {
	payload, notes, err := Defaults("claude", "settings.json", spec.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil (nothing to render)", payload)
	}
	if notes != nil {
		t.Errorf("notes = %v, want nil", notes)
	}
}

func TestDefaultsClaudeFullFieldCoverage(t *testing.T) {
	d := spec.Defaults{
		Model:      &spec.ModelPref{Name: "claude-sonnet-4-5", Effort: spec.EffortHigh},
		Theme:      "dark",
		Statusline: &spec.Statusline{Command: "statusline.sh", Padding: 2, RefreshInterval: 5},
		Telemetry:  new(false),
	}
	payload, notes, err := Defaults("claude", "settings.json", d)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none (high needs no clamp)", notes)
	}
	got := decodeJSON(t, payload)
	if got["model"] != "claude-sonnet-4-5" {
		t.Errorf("model = %v", got["model"])
	}
	if got["effortLevel"] != "high" {
		t.Errorf("effortLevel = %v", got["effortLevel"])
	}
	if got["theme"] != "dark" {
		t.Errorf("theme = %v", got["theme"])
	}
	sl, ok := got["statusLine"].(map[string]any)
	if !ok || sl["type"] != "command" || sl["command"] != "statusline.sh" || sl["padding"] != float64(2) || sl["refreshInterval"] != float64(5) {
		t.Errorf("statusLine = %v", got["statusLine"])
	}
	env, ok := got["env"].(map[string]any)
	if !ok || env["DISABLE_TELEMETRY"] != "1" {
		t.Errorf("env = %v", got["env"])
	}
}

func TestDefaultsClaudeEffortClampsOutOfRange(t *testing.T) {
	cases := []struct{ in, want string }{
		{spec.EffortOff, spec.EffortLow},
		{spec.EffortMinimal, spec.EffortLow},
		{spec.EffortMax, spec.EffortXHigh},
	}
	for _, c := range cases {
		d := spec.Defaults{Model: &spec.ModelPref{Name: "x", Effort: c.in}}
		payload, notes, err := Defaults("claude", "settings.json", d)
		if err != nil {
			t.Fatal(err)
		}
		got := decodeJSON(t, payload)
		if got["effortLevel"] != c.want {
			t.Errorf("effort %q -> %v, want %q", c.in, got["effortLevel"], c.want)
		}
		if len(notes) != 1 {
			t.Errorf("effort %q: notes = %v, want exactly one clamp note", c.in, notes)
		}
	}
}

func TestDefaultsClaudeEffortInRangeNoClamp(t *testing.T) {
	for _, e := range []string{spec.EffortLow, spec.EffortMedium, spec.EffortHigh, spec.EffortXHigh} {
		d := spec.Defaults{Model: &spec.ModelPref{Name: "x", Effort: e}}
		_, notes, err := Defaults("claude", "settings.json", d)
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != 0 {
			t.Errorf("effort %q: notes = %v, want none", e, notes)
		}
	}
}

func TestDefaultsClaudeTelemetryTrueWritesNothing(t *testing.T) {
	d := spec.Defaults{Telemetry: new(true)}
	payload, _, err := Defaults("claude", "settings.json", d)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil (telemetry:true is a no-op, not an explicit enable key)", payload)
	}
}

type codexDefaultsDoc struct {
	Model                string `toml:"model"`
	ModelProvider        string `toml:"model_provider"`
	ModelReasoningEffort string `toml:"model_reasoning_effort"`
	Tui                  struct {
		Theme string `toml:"theme"`
	} `toml:"tui"`
	Analytics struct {
		Enabled bool `toml:"enabled"`
	} `toml:"analytics"`
}

func TestDefaultsCodexBareScalarsAndOwnedTables(t *testing.T) {
	d := spec.Defaults{
		Model:     &spec.ModelPref{Provider: "openai", Name: "gpt-5-codex", Effort: spec.EffortOff},
		Theme:     "dark",
		Telemetry: new(false),
	}
	payload, notes, err := Defaults("codex", "config.toml", d)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want one clamp note (\"off\" is outside codex's minimal..xhigh enum)", notes)
	}
	var got codexDefaultsDoc
	if err := toml.Unmarshal(payload, &got); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, payload)
	}
	if got.Model != "gpt-5-codex" || got.ModelProvider != "openai" {
		t.Errorf("model/model_provider = %q/%q", got.Model, got.ModelProvider)
	}
	if got.ModelReasoningEffort != spec.EffortMinimal {
		t.Errorf("model_reasoning_effort = %q, want %q (clamped into codex's own enum)", got.ModelReasoningEffort, spec.EffortMinimal)
	}
	if got.Tui.Theme != "dark" {
		t.Errorf("tui.theme = %q", got.Tui.Theme)
	}
	if got.Analytics.Enabled {
		t.Error("analytics.enabled = true, want false")
	}
}

// TestDefaultsCodexEffortClampedToItsOwnEnum: codex types
// model_reasoning_effort as minimal|low|medium|high|xhigh, so the union
// ladder's "off" and "max" must not reach the user's real config.toml.
func TestDefaultsCodexEffortClampedToItsOwnEnum(t *testing.T) {
	cases := []struct {
		in, want string
		clamped  bool
	}{
		{spec.EffortOff, spec.EffortMinimal, true},
		{spec.EffortMinimal, spec.EffortMinimal, false},
		{spec.EffortMedium, spec.EffortMedium, false},
		{spec.EffortXHigh, spec.EffortXHigh, false},
		{spec.EffortMax, spec.EffortXHigh, true},
	}
	for _, c := range cases {
		d := spec.Defaults{Model: &spec.ModelPref{Name: "gpt-5-codex", Effort: c.in}}
		payload, notes, err := Defaults("codex", "config.toml", d)
		if err != nil {
			t.Fatal(err)
		}
		var got codexDefaultsDoc
		if err := toml.Unmarshal(payload, &got); err != nil {
			t.Fatalf("invalid TOML: %v\n%s", err, payload)
		}
		if got.ModelReasoningEffort != c.want {
			t.Errorf("effort %q -> %q, want %q", c.in, got.ModelReasoningEffort, c.want)
		}
		if noted := len(notes) == 1; noted != c.clamped {
			t.Errorf("effort %q: notes = %v, want clamp note = %v", c.in, notes, c.clamped)
		}
	}
}

func TestDefaultsCodexModelProviderOptional(t *testing.T) {
	d := spec.Defaults{Model: &spec.ModelPref{Name: "gpt-5-codex"}}
	payload, _, err := Defaults("codex", "config.toml", d)
	if err != nil {
		t.Fatal(err)
	}
	var got codexDefaultsDoc
	if err := toml.Unmarshal(payload, &got); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, payload)
	}
	if got.Model != "gpt-5-codex" || got.ModelProvider != "" {
		t.Errorf("model/model_provider = %q/%q, want provider omitted", got.Model, got.ModelProvider)
	}
}

func TestDefaultsCodexStatuslineUnsupportedNote(t *testing.T) {
	_, notes, err := Defaults("codex", "config.toml", spec.Defaults{Statusline: &spec.Statusline{Command: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one unsupported-statusline note", notes)
	}
}

// TestDefaultsStatuslineOmitsUnsetOptionalFields: padding and
// refresh_interval are optional canonically, and both adapters document
// refreshInterval as seconds with a minimum of 1 — a written zero is an
// invalid value that costs the whole statusLine object.
func TestDefaultsStatuslineOmitsUnsetOptionalFields(t *testing.T) {
	for _, adapter := range []string{"claude", "copilot"} {
		payload, _, err := Defaults(adapter, "settings.json", spec.Defaults{Statusline: &spec.Statusline{Command: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		sl, ok := decodeJSON(t, payload)["statusLine"].(map[string]any)
		if !ok {
			t.Fatalf("%s: payload = %s, want a statusLine object", adapter, payload)
		}
		if sl["type"] != "command" || sl["command"] != "x" {
			t.Errorf("%s: statusLine = %v", adapter, sl)
		}
		for _, key := range []string{"padding", "refreshInterval"} {
			if v, present := sl[key]; present {
				t.Errorf("%s: statusLine.%s = %v, want the key omitted when unset", adapter, key, v)
			}
		}
	}
}

// TestDefaultsOverridesUnknownAdapterKeyReported: the pre-v1.0 renames
// (antigravity -> gemini, pi -> oh-my-pi) leave stale overrides keys that
// reach no file at all, and silence there loses real user configuration.
func TestDefaultsOverridesUnknownAdapterKeyReported(t *testing.T) {
	d := spec.Defaults{Overrides: map[string]map[string]any{
		"antigravity": {"x": 1},
		"gemini":      {"y": 2},
	}}
	payload, notes, err := Defaults("gemini", "settings.json", d)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeJSON(t, payload)["y"]; got != float64(2) {
		t.Errorf("y = %v, want the valid override applied", got)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "antigravity") {
		t.Errorf("notes = %v, want one entry naming the unknown overrides key", notes)
	}
}

// TestDefaultsModelProviderReportedWhereDropped: only codex and opencode
// have somewhere to put a provider; the other three fold it into the model
// name, and an unrepresentable canonical field is reported, not discarded.
func TestDefaultsModelProviderReportedWhereDropped(t *testing.T) {
	d := spec.Defaults{Model: &spec.ModelPref{Provider: "anthropic", Name: "claude-sonnet-4-5"}}
	for _, adapter := range []string{"claude", "copilot", "gemini"} {
		_, notes, err := Defaults(adapter, "settings.json", d)
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != 1 || !strings.Contains(notes[0], "model.provider") {
			t.Errorf("%s: notes = %v, want one entry naming the dropped provider", adapter, notes)
		}
	}
}

func TestDefaultsCopilotThemeRejectsUnknownValue(t *testing.T) {
	d := spec.Defaults{Theme: "solarized"}
	if _, _, err := Defaults("copilot", "settings.json", d); err == nil {
		t.Error("expected error for a theme outside copilot's fixed enum")
	}
}

func TestDefaultsCopilotThemeAcceptsEveryAllowedValue(t *testing.T) {
	for _, theme := range copilotThemes {
		payload, _, err := Defaults("copilot", "settings.json", spec.Defaults{Theme: theme})
		if err != nil {
			t.Errorf("theme %q: %v", theme, err)
			continue
		}
		got := decodeJSON(t, payload)
		if got["theme"] != theme {
			t.Errorf("theme %q round-tripped as %v", theme, got["theme"])
		}
	}
}

func TestDefaultsCopilotTelemetryReportedNotWritten(t *testing.T) {
	payload, notes, err := Defaults("copilot", "settings.json", spec.Defaults{Telemetry: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil (nothing else set, telemetry unsupported)", payload)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one unsupported-telemetry note", notes)
	}
}

func TestDefaultsOpenCodeSettingsRequiresProviderForModel(t *testing.T) {
	d := spec.Defaults{Model: &spec.ModelPref{Name: "gpt-5"}}
	if _, _, err := Defaults("opencode", "opencode.json", d); err == nil {
		t.Error("expected error: opencode model requires provider")
	}
}

func TestDefaultsOpenCodeSettingsModelAndTelemetry(t *testing.T) {
	d := spec.Defaults{
		Model:     &spec.ModelPref{Provider: "anthropic", Name: "claude-sonnet-4-5", Effort: spec.EffortHigh},
		Telemetry: new(false),
	}
	payload, notes, err := Defaults("opencode", "opencode.json", d)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSON(t, payload)
	if got["model"] != "anthropic/claude-sonnet-4-5" {
		t.Errorf("model = %v, want provider/name", got["model"])
	}
	exp, ok := got["experimental"].(map[string]any)
	if !ok || exp["openTelemetry"] != false {
		t.Errorf("experimental = %v", got["experimental"])
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one unsupported-effort note", notes)
	}
	if _, hasTheme := got["theme"]; hasTheme {
		t.Error("opencode.json must never carry theme — that belongs to tui.json")
	}
}

func TestDefaultsOpenCodeSettingsStatuslineUnsupportedNote(t *testing.T) {
	_, notes, err := Defaults("opencode", "opencode.json", spec.Defaults{Statusline: &spec.Statusline{Command: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one unsupported-statusline note", notes)
	}
}

func TestDefaultsOpenCodeTUITheme(t *testing.T) {
	payload, notes, err := Defaults("opencode", "tui.json", spec.Defaults{Theme: "dark", Telemetry: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	got := decodeJSON(t, payload)
	if got["theme"] != "dark" {
		t.Errorf("theme = %v", got["theme"])
	}
	if _, hasModel := got["model"]; hasModel {
		t.Error("tui.json must never carry model — that belongs to opencode.json")
	}
	if _, hasExp := got["experimental"]; hasExp {
		t.Error("tui.json must never carry telemetry — that belongs to opencode.json")
	}
}

func TestDefaultsOpenCodeUnknownTargetErrors(t *testing.T) {
	if _, _, err := Defaults("opencode", "settings.json", spec.Defaults{Theme: "dark"}); err == nil {
		t.Error("expected error for a target neither opencode.json nor tui.json")
	}
}

func TestDefaultsGeminiNestedModelThemeTelemetry(t *testing.T) {
	d := spec.Defaults{
		Model:     &spec.ModelPref{Name: "gemini-2.5-pro", Effort: spec.EffortHigh},
		Theme:     "dark",
		Telemetry: new(false),
	}
	payload, notes, err := Defaults("gemini", "settings.json", d)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSON(t, payload)
	model, ok := got["model"].(map[string]any)
	if !ok || model["name"] != "gemini-2.5-pro" {
		t.Errorf("model = %v, want nested {name: ...}", got["model"])
	}
	ui, ok := got["ui"].(map[string]any)
	if !ok || ui["theme"] != "dark" {
		t.Errorf("ui = %v, want nested {theme: ...}", got["ui"])
	}
	privacy, ok := got["privacy"].(map[string]any)
	if !ok || privacy["usageStatisticsEnabled"] != false {
		t.Errorf("privacy = %v", got["privacy"])
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one unsupported-effort note", notes)
	}
}

func TestDefaultsGeminiStatuslineUnsupportedNote(t *testing.T) {
	d := spec.Defaults{Statusline: &spec.Statusline{Command: "x"}}
	payload, notes, err := Defaults("gemini", "settings.json", d)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil (nothing else set, statusline unsupported)", payload)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one unsupported-statusline note", notes)
	}
}

func TestDefaultsOverridesWinOverCanonicalField(t *testing.T) {
	d := spec.Defaults{
		Model:     &spec.ModelPref{Name: "gpt-5-codex"},
		Overrides: map[string]map[string]any{"codex": {"model": "overridden", "model_verbosity": "low"}},
	}
	payload, _, err := Defaults("codex", "config.toml", d)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := toml.Unmarshal(payload, &got); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, payload)
	}
	if got["model"] != "overridden" {
		t.Errorf("model = %v, want the override to win over the canonical field", got["model"])
	}
	if got["model_verbosity"] != "low" {
		t.Errorf("model_verbosity = %v", got["model_verbosity"])
	}
}

func TestDefaultsOverridesOnOpenCodeApplyOnlyToSettingsFile(t *testing.T) {
	d := spec.Defaults{Overrides: map[string]map[string]any{"opencode": {"extra": "x"}}}
	settingsPayload, _, err := Defaults("opencode", "opencode.json", d)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeJSON(t, settingsPayload)["extra"]; got != "x" {
		t.Errorf("opencode.json extra = %v, want override applied", got)
	}
	tuiPayload, _, err := Defaults("opencode", "tui.json", d)
	if err != nil {
		t.Fatal(err)
	}
	if tuiPayload != nil {
		t.Errorf("tui.json payload = %s, want nil (overrides do not leak into the theme file)", tuiPayload)
	}
}

func TestClampEffortBoundaries(t *testing.T) {
	cases := []struct {
		effort, floor, ceil, want string
		changed                   bool
	}{
		{spec.EffortOff, spec.EffortLow, spec.EffortXHigh, spec.EffortLow, true},
		{spec.EffortMinimal, spec.EffortLow, spec.EffortXHigh, spec.EffortLow, true},
		{spec.EffortLow, spec.EffortLow, spec.EffortXHigh, spec.EffortLow, false},
		{spec.EffortMedium, spec.EffortLow, spec.EffortXHigh, spec.EffortMedium, false},
		{spec.EffortXHigh, spec.EffortLow, spec.EffortXHigh, spec.EffortXHigh, false},
		{spec.EffortMax, spec.EffortLow, spec.EffortXHigh, spec.EffortXHigh, true},
	}
	for _, c := range cases {
		clamped, changed := ClampEffort(c.effort, c.floor, c.ceil)
		if clamped != c.want || changed != c.changed {
			t.Errorf("ClampEffort(%q, %q, %q) = (%q, %v), want (%q, %v)", c.effort, c.floor, c.ceil, clamped, changed, c.want, c.changed)
		}
	}
}
