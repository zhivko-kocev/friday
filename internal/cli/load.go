package cli

import (
	"errors"
	"os"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// loadUserOrDefault loads the user manifest, or falls back to all built-in
// presets if friday.yaml is absent. The store dir itself must exist (run
// `friday init` first).
func loadUserOrDefault() (*config.Config, error) {
	exists, err := config.StoreExists()
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("no user store — run `friday init` first")
	}
	cfg, err := config.LoadUser()
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, config.ErrNoManifest) {
		return nil, err
	}
	storeDir, _ := config.UserStoreDir()
	home, _ := os.UserHomeDir()
	return config.NewDefault(config.ScopeUser, storeDir, home, presetAdapters()), nil
}

// installedAdapters returns the names of every adapter in cfg whose target
// directory exists on disk. Used by `friday push` (no args) to mean "every
// agent that's actually installed on this machine".
func installedAdapters(cfg *config.Config) []string {
	var out []string
	for _, name := range cfg.AdapterNames() {
		abs, err := cfg.AdapterTargetAbs(name)
		if err != nil {
			continue
		}
		if dirExists(abs) {
			out = append(out, name)
		}
	}
	return out
}

// presetAdapters renders the fallback adapter set from the built-in presets,
// used when the store has no friday.yaml manifest.
func presetAdapters() map[string]*config.Adapter {
	out := map[string]*config.Adapter{}
	for name, p := range presets.AllAdapters() {
		out[name] = p.Adapter()
	}
	return out
}
