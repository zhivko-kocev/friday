package spec

import (
	"strings"
	"testing"
)

func TestParseDefaultsValidEverything(t *testing.T) {
	d, err := ParseDefaults([]byte(`
version: 1
model:
  provider: anthropic
  name: claude-sonnet-4-5
  effort: high
theme: dark
statusline:
  command: ~/.friday/bin/statusline.sh
  padding: 2
  refresh_interval: 5
telemetry: false
overrides:
  codex: { model_verbosity: low }
`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Model == nil || d.Model.Provider != "anthropic" || d.Model.Name != "claude-sonnet-4-5" || d.Model.Effort != EffortHigh {
		t.Errorf("model = %+v", d.Model)
	}
	if d.Theme != "dark" {
		t.Errorf("theme = %q", d.Theme)
	}
	if d.Statusline == nil || d.Statusline.Command != "~/.friday/bin/statusline.sh" || d.Statusline.Padding != 2 || d.Statusline.RefreshInterval != 5 {
		t.Errorf("statusline = %+v", d.Statusline)
	}
	if d.Telemetry == nil || *d.Telemetry != false {
		t.Errorf("telemetry = %v", d.Telemetry)
	}
	if got := d.Overrides["codex"]["model_verbosity"]; got != "low" {
		t.Errorf("overrides.codex.model_verbosity = %v", got)
	}
}

func TestParseDefaultsEmptyIsValid(t *testing.T) {
	d, err := ParseDefaults(nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 {
		t.Errorf("version = %d, want default 1", d.Version)
	}
	if d.Model != nil || d.Theme != "" || d.Statusline != nil || d.Telemetry != nil {
		t.Errorf("empty input produced non-zero fields: %+v", d)
	}
}

func TestParseDefaultsVersionRejectsUnsupported(t *testing.T) {
	if _, err := ParseDefaults([]byte("version: 2\n")); err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestParseDefaultsRejectsUnknownTopLevelField(t *testing.T) {
	_, err := ParseDefaults([]byte("modle:\n  name: x\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown field "modle"`) || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("err = %v, want the key and line named", err)
	}
}

func TestParseDefaultsRejectsUnknownModelField(t *testing.T) {
	_, err := ParseDefaults([]byte("model:\n  name: x\n  effrot: high\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown field "effrot"`) {
		t.Errorf("err = %v, want the key named", err)
	}
	if strings.Contains(err.Error(), "spec.") {
		t.Errorf("err = %q, want no Go type name", err)
	}
}

// A theme typo used to reach five adapters as a garbled string and
// hard-error only the sixth — same input, six different outcomes.
func TestParseDefaultsThemeMustBeOnTheUnion(t *testing.T) {
	_, err := ParseDefaults([]byte("theme: darck\n"))
	if err == nil || !strings.Contains(err.Error(), `theme "darck"`) {
		t.Errorf("err = %v, want the offending theme named", err)
	}
}

func TestParseDefaultsThemeAcceptsEveryUnionValue(t *testing.T) {
	for _, theme := range themeUnion {
		d, err := ParseDefaults([]byte("theme: " + theme + "\n"))
		if err != nil {
			t.Errorf("theme %q: %v", theme, err)
			continue
		}
		if d.Theme != theme {
			t.Errorf("theme %q round-tripped as %q", theme, d.Theme)
		}
	}
}

// Overrides is read only by lookup, so a misspelled adapter name produced
// no error, no warning, and no output — on the one field whose entire
// purpose is to be the last resort.
func TestParseDefaultsRejectsUnknownOverridesAdapter(t *testing.T) {
	_, err := ParseDefaults([]byte("overrides:\n  codexx: { model_verbosity: low }\n"))
	if err == nil || !strings.Contains(err.Error(), `"codexx"`) {
		t.Errorf("err = %v, want the misspelled adapter named", err)
	}
	if !strings.Contains(err.Error(), "friday migrate") {
		t.Errorf("err = %v, want the pre-v1.0 rename route named", err)
	}
}

func TestParseDefaultsAcceptsEveryAdapterInOverrides(t *testing.T) {
	for _, adapter := range Adapters {
		if _, err := ParseDefaults([]byte("overrides:\n  " + adapter + ": { k: v }\n")); err != nil {
			t.Errorf("overrides.%s: %v", adapter, err)
		}
	}
}

func TestParseDefaultsRejectsNegativeStatuslineValues(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"statusline:\n  command: x\n  padding: -3\n", "statusline.padding"},
		{"statusline:\n  command: x\n  refresh_interval: -1\n", "statusline.refresh_interval"},
	} {
		_, err := ParseDefaults([]byte(tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want %s named", err, tc.want)
		}
	}
}

func TestParseDefaultsRejectsSecondDocument(t *testing.T) {
	_, err := ParseDefaults([]byte("theme: dark\n---\ntelemetry: false\n"))
	if err == nil || !strings.Contains(err.Error(), "single YAML document") {
		t.Errorf("err = %v, want a multi-document refusal", err)
	}
}

func TestParseDefaultsRejectsDuplicateKey(t *testing.T) {
	_, err := ParseDefaults([]byte("theme: dark\ntheme: light\n"))
	if err == nil || !strings.Contains(err.Error(), `"theme" already defined`) {
		t.Errorf("err = %v, want the duplicated key named", err)
	}
}

func TestParseDefaultsErrorDoesNotNameTheFile(t *testing.T) {
	_, err := ParseDefaults([]byte("model:\n  provider: anthropic\n"))
	if err == nil || strings.Contains(err.Error(), "defaults.yaml") {
		t.Errorf("err = %v, want no filename prefix (the caller supplies the path)", err)
	}
}

func TestParseDefaultsModelRequiresName(t *testing.T) {
	if _, err := ParseDefaults([]byte("model:\n  provider: anthropic\n")); err == nil {
		t.Error("expected error: model set without model.name")
	}
}

func TestParseDefaultsModelEffortMustBeOnLadder(t *testing.T) {
	if _, err := ParseDefaults([]byte("model:\n  name: x\n  effort: turbo\n")); err == nil {
		t.Error("expected error for effort value not on the union ladder")
	}
}

func TestParseDefaultsModelEffortAcceptsEveryLadderValue(t *testing.T) {
	for _, effort := range []string{EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax} {
		d, err := ParseDefaults([]byte("model:\n  name: x\n  effort: " + effort + "\n"))
		if err != nil {
			t.Errorf("effort %q: %v", effort, err)
			continue
		}
		if d.Model.Effort != effort {
			t.Errorf("effort %q round-tripped as %q", effort, d.Model.Effort)
		}
	}
}

func TestParseDefaultsStatuslineRequiresCommand(t *testing.T) {
	if _, err := ParseDefaults([]byte("statusline:\n  padding: 2\n")); err == nil {
		t.Error("expected error: statusline set without statusline.command")
	}
}

func TestParseDefaultsTelemetryTrueIsDistinguishableFromUnset(t *testing.T) {
	d, err := ParseDefaults([]byte("telemetry: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Telemetry == nil || *d.Telemetry != true {
		t.Errorf("telemetry = %v, want pointer to true", d.Telemetry)
	}
}
