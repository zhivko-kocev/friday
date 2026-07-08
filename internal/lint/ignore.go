package lint

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigName is the optional per-store lint config. It sits in the store root
// so it versions alongside the content it governs.
const ConfigName = ".friday-doctor.yaml"

// lintConfig is the schema of .friday-doctor.yaml. Kept deliberately small:
// disable silences a rule by its slug across the whole store. Severity
// overrides can follow if the need appears.
type lintConfig struct {
	Disable []string `yaml:"disable"`
}

// applyIgnores drops findings whose rule the store disabled. A missing or
// unreadable config is not an error — linting proceeds with nothing disabled.
func applyIgnores(storeDir string, findings []Finding) []Finding {
	data, err := os.ReadFile(filepath.Join(storeDir, ConfigName))
	if err != nil {
		return findings
	}
	var cfg lintConfig
	if yaml.Unmarshal(data, &cfg) != nil || len(cfg.Disable) == 0 {
		return findings
	}
	disabled := make(map[string]bool, len(cfg.Disable))
	for _, r := range cfg.Disable {
		disabled[r] = true
	}
	kept := findings[:0]
	for _, f := range findings {
		if !disabled[f.Rule] {
			kept = append(kept, f)
		}
	}
	return kept
}
