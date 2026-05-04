package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
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

// loadUserForWrite is loadUserOrDefault but materializes the manifest on
// disk so subsequent writes (friday add) have a stable target.
func loadUserForWrite() (*config.Config, error) {
	cfg, err := loadUserOrDefault()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(cfg.ManifestPath); os.IsNotExist(statErr) {
		// First explicit customization — promote in-memory defaults to disk.
		// Start from an empty adapter map so `friday add` only persists what
		// the user actually customized; presets still apply by default
		// because LoadUser/loadUserOrDefault tracks ErrNoManifest fallthrough.
		cfg.Adapters = map[string]*config.Adapter{}
		if err := cfg.Save(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// loadProjectFromURL clones url to a temp dir, loads the manifest (or falls
// back to presets if none), and returns the config plus a cleanup callback.
func loadProjectFromURL(url string) (*config.Config, func(), error) {
	if !git.Available() {
		return nil, nil, errors.New("git not found in PATH")
	}
	if err := git.ValidateURL(url); err != nil {
		return nil, nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp("", "friday-clone-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	repoDir := filepath.Join(tmp, "repo")
	if err := git.Clone(url, repoDir); err != nil {
		cleanup()
		return nil, nil, err
	}
	cfg, err := config.LoadProject(repoDir, cwd)
	if err != nil {
		if !errors.Is(err, config.ErrNoManifest) {
			cleanup()
			return nil, nil, err
		}
		cfg = config.NewDefault(config.ScopeProject, repoDir, cwd, presetAdapters())
	}
	return cfg, cleanup, nil
}

func presetAdapters() map[string]*config.Adapter {
	out := map[string]*config.Adapter{}
	for name, p := range presets.AllAdapters() {
		out[name] = p.Adapter()
	}
	return out
}
