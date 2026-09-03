package spec

import (
	"fmt"
	"slices"
	"strings"
)

// Effort is the union ladder every adapter's own enum narrows from —
// off < minimal < low < medium < high < xhigh < max. internal/render's
// ClampEffort narrows it per adapter; this package only validates a value
// is somewhere on the ladder.
const (
	EffortOff     = "off"
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	EffortXHigh   = "xhigh"
	EffortMax     = "max"
)

// effortLadder is EffortOff..EffortMax in declaration order.
var effortLadder = []string{EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// Theme values — the canonical union every adapter's own theme enum narrows
// from, the same shape Effort has. render.Defaults maps or rejects per
// adapter (Copilot's own settings schema accepts the narrowest set); this
// package only validates a value is on the union, so a typo cannot reach
// five adapters as a garbled string and hard-error the sixth.
const (
	ThemeDefault      = "default"
	ThemeDark         = "dark"
	ThemeLight        = "light"
	ThemeGitHub       = "github"
	ThemeDim          = "dim"
	ThemeHighContrast = "high-contrast"
	ThemeColorblind   = "colorblind"
)

// themeUnion is ThemeDefault..ThemeColorblind in declaration order.
var themeUnion = []string{ThemeDefault, ThemeDark, ThemeLight, ThemeGitHub, ThemeDim, ThemeHighContrast, ThemeColorblind}

// Defaults is the parsed, validated content of defaults.yaml — friday's
// canonical settings surface (model, theme, statusline, telemetry) that
// internal/render projects onto each adapter's own settings shape.
type Defaults struct {
	Version int        `yaml:"version"`
	Model   *ModelPref `yaml:"model,omitempty"`
	// Theme must be one of the union above; render.Defaults maps or
	// rejects it per adapter, the same way Effort is clamped.
	Theme      string      `yaml:"theme,omitempty"`
	Statusline *Statusline `yaml:"statusline,omitempty"`
	// Telemetry is only ever an opt-out switch: an explicit false renders
	// each adapter's own disable knob; true or unset both render nothing,
	// leaving every adapter's native default (telemetry on) in place.
	Telemetry *bool `yaml:"telemetry,omitempty"`
	// Overrides is a per-adapter escape hatch for a vendor-only field the
	// schema above does not model at all (e.g. codex's model_verbosity) —
	// the same inline-escape-hatch pattern rulesync uses for permissions
	// (D20). Each adapter's map is merged directly into that adapter's
	// rendered payload, and always wins over a canonical field mapped to
	// the same key: an escape hatch that loses to the normal path isn't one.
	Overrides map[string]map[string]any `yaml:"overrides,omitempty"`
}

// ModelPref names the model every adapter should default to.
type ModelPref struct {
	// Provider is optional for most adapters (folded into a bare Name, or
	// unused); OpenCode keys a model by provider/name and has no other way
	// to spell a provider, so render.Defaults requires it there.
	Provider string `yaml:"provider,omitempty"`
	Name     string `yaml:"name"`
	// Effort must be one of the union ladder above; render.Defaults clamps
	// it to whatever narrower enum the target adapter actually accepts.
	Effort string `yaml:"effort,omitempty"`
}

// Statusline configures a shell-command status line, on the two adapters
// whose native surface supports one (claude, copilot).
type Statusline struct {
	Command         string `yaml:"command"`
	Padding         int    `yaml:"padding,omitempty"`
	RefreshInterval int    `yaml:"refresh_interval,omitempty"`
}

// ParseDefaults parses and validates defaults.yaml content. Strict decoding
// rejects an unrecognized field anywhere in the document, matching every
// other canonical schema's typo protection.
func ParseDefaults(data []byte) (*Defaults, error) {
	var d Defaults
	if err := decodeStrict(data, &d); err != nil {
		return nil, err
	}
	if d.Version == 0 {
		d.Version = 1
	}
	if d.Version != 1 {
		return nil, fmt.Errorf("unsupported version %d", d.Version)
	}
	if d.Model != nil {
		if d.Model.Name == "" {
			return nil, fmt.Errorf("model.name is required when model is set")
		}
		if d.Model.Effort != "" && !slices.Contains(effortLadder, d.Model.Effort) {
			return nil, fmt.Errorf("model.effort %q must be one of %s", d.Model.Effort, strings.Join(effortLadder, "|"))
		}
	}
	if d.Theme != "" && !slices.Contains(themeUnion, d.Theme) {
		return nil, fmt.Errorf("theme %q must be one of %s", d.Theme, strings.Join(themeUnion, "|"))
	}
	if d.Statusline != nil {
		if d.Statusline.Command == "" {
			return nil, fmt.Errorf("statusline.command is required when statusline is set")
		}
		// Both are UI measurements every adapter treats as non-negative; a
		// negative one is a config the agent rejects after friday reports a
		// successful push.
		if d.Statusline.Padding < 0 {
			return nil, fmt.Errorf("statusline.padding %d must not be negative", d.Statusline.Padding)
		}
		if d.Statusline.RefreshInterval < 0 {
			return nil, fmt.Errorf("statusline.refresh_interval %d must not be negative", d.Statusline.RefreshInterval)
		}
	}
	// Overrides is keyed by adapter name and read only by lookup, so a
	// misspelled key is a total no-op — no error, no warning, no output —
	// on the one field whose whole purpose is to be the last resort.
	// render.Defaults reports separately on an adapter that is real but has
	// no defaults surface to merge into (oh-my-pi's settings are YAML).
	overridden := make([]string, 0, len(d.Overrides))
	for adapter := range d.Overrides {
		overridden = append(overridden, adapter)
	}
	slices.Sort(overridden)
	for _, adapter := range overridden {
		if !slices.Contains(Adapters, adapter) {
			return nil, fmt.Errorf("overrides key %q is not an adapter (must be one of %s); run `friday migrate` if it is a pre-v1.0 adapter name", adapter, strings.Join(Adapters, ", "))
		}
	}
	return &d, nil
}
