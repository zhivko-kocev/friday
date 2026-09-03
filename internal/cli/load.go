package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// loadUserOrDefault loads the user manifest, or falls back to all built-in
// presets if friday.yaml is absent. The store dir itself must exist (run
// `friday init` first). It is also the one funnel every command's config
// load passes through, which makes it the single place that can surface
// cfg.Warnings without repeating them per adapter or per command phase.
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
		warnStaleManifest(cfg)
		return cfg, nil
	}
	if !errors.Is(err, config.ErrNoManifest) {
		return nil, err
	}
	storeDir, _ := config.UserStoreDir()
	home, _ := os.UserHomeDir()
	return config.NewDefault(config.ScopeUser, storeDir, home, presetAdapters()), nil
}

// manifestWarningsShown collapses cfg.Warnings to one report per process
// run. Several commands load the manifest more than once — doctor probes it
// directly and then falls back, and the bare-invocation hint loads it after
// whatever already ran — and repeating "your manifest is stale" for each
// load trains the reader to skip it. Run resets it so an in-process test
// binary (and `go run` loops) still see the warning on every invocation.
var manifestWarningsShown bool

// warnStaleManifest reports load-time manifest problems on stderr, never
// stdout: `--json` output and the shell-completion candidate stream are both
// machine-parsed, and an interleaved warning would corrupt either one.
func warnStaleManifest(cfg *config.Config) {
	if manifestWarningsShown || len(cfg.Warnings) == 0 {
		return
	}
	manifestWarningsShown = true
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "  ! %s\n", w)
	}
}

// installedAdapters returns the names of every adapter in cfg considered
// installed on this machine. Used by `friday push` (no args) to mean "every
// agent that's actually installed on this machine".
func installedAdapters(cfg *config.Config) []string {
	var out []string
	for _, name := range cfg.AdapterNames() {
		if adapterInstalled(cfg, name) {
			out = append(out, name)
		}
	}
	return out
}

// adapterInstalled reports whether name counts as installed. A preset
// carrying InstalledIf (the shared "agents" adapter, which several other
// agents read natively rather than friday writing anything under its own
// ~/.agents until one of them needs it) is installed when any of the named
// adapters' own target dirs exist — never its own, which a fresh machine
// would never have yet. Every other adapter uses the plain "its own target
// dir exists" check.
//
// The proxy applies only while the manifest still describes the built-in
// preset. A user who repoints that adapter somewhere of their own gets the
// ordinary check against the target they actually declared — otherwise
// friday would answer "installed" based on a directory their adapter has
// nothing to do with.
func adapterInstalled(cfg *config.Config, name string) bool {
	if p, ok := presets.Get(name); ok && len(p.InstalledIf) > 0 && declaresPresetTarget(cfg, name, p) {
		for _, other := range p.InstalledIf {
			if abs, err := cfg.AdapterTargetAbs(other); err == nil && dirExists(abs) {
				return true
			}
		}
		return false
	}
	abs, err := cfg.AdapterTargetAbs(name)
	return err == nil && dirExists(abs)
}

// declaresPresetTarget reports whether cfg's adapter still points at the
// built-in preset's own target, so InstalledIf's proxy check stays scoped to
// the adapter the preset actually describes.
func declaresPresetTarget(cfg *config.Config, name string, p presets.Preset) bool {
	ad, ok := cfg.Adapters[name]
	if !ok {
		// No manifest entry at all: the preset fallback is what is in play.
		return true
	}
	return ad.Target == p.Target
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

// dirExists reports whether path is an existing directory. Used to flag an
// adapter as "installed" — i.e. friday push (no args) will target it.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
