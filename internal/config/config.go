// Package config loads friday.yaml and resolves paths for the user-level store.
//
// The manifest at $HOME/.friday/friday.yaml controls every agent dir on the
// machine (~/.claude, ~/.cursor, ...). The store dir IS the directory that
// contains the manifest — flat, no nested store/.
//
// Path resolution for `target:` and rule destinations:
//
//   - absolute path  →  used as-is
//   - leading "~/"   →  expanded to $HOME/...
//   - relative       →  joined with TargetRoot ($HOME for user scope)
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/rules"
)

const ManifestName = "friday.yaml"

// ErrNoManifest signals that no friday.yaml was found at the expected
// location. Callers (CLI, initcmd) decide how to react — typically by
// falling back to the built-in presets.
var ErrNoManifest = errors.New("no friday.yaml")

// Scope identifies which store the config was loaded from. Currently only
// user scope is supported; project scope is reserved for a future release.
type Scope int

const ScopeUser Scope = iota

func (s Scope) String() string { return "user" }

// Config is the parsed friday.yaml plus runtime context (scope, paths).
type Config struct {
	Version  int                 `yaml:"version"`
	Adapters map[string]*Adapter `yaml:"adapters"`

	// Filled by Load.
	Scope        Scope  `yaml:"-"`
	ManifestPath string `yaml:"-"` // absolute path to friday.yaml
	StoreDir     string `yaml:"-"` // dir containing the manifest = canonical store
	TargetRoot   string `yaml:"-"` // $HOME for user scope, CWD for project scope
}

// Adapter is one named entry under `adapters:` in friday.yaml.
type Adapter struct {
	Target string        `yaml:"target"`
	Rules  []*rules.Rule `yaml:"rules"`
}

// UserStoreDir returns the canonical user-level store directory: $HOME/.friday.
// One store per user, shared across every project on the machine.
func UserStoreDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".friday"), nil
}

// LoadUser loads the user-level manifest from $HOME/.friday/friday.yaml.
// Returns ErrNoManifest if the file is missing (the store dir may still exist
// — a cloned repo without friday.yaml is the common case).
func LoadUser() (*Config, error) {
	storeDir, err := UserStoreDir()
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(storeDir, ManifestName)
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoManifest
		}
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return load(manifestPath, ScopeUser, storeDir, home)
}

// NewDefault constructs an in-memory Config with no on-disk manifest. Used as
// a fallback when a repo / store has md content but no friday.yaml. The
// caller supplies the adapter set (typically all presets).
func NewDefault(scope Scope, storeDir, targetRoot string, adapters map[string]*Adapter) *Config {
	cfg := &Config{
		Version:      1,
		Adapters:     adapters,
		Scope:        scope,
		StoreDir:     storeDir,
		TargetRoot:   targetRoot,
		ManifestPath: filepath.Join(storeDir, ManifestName),
	}
	for _, a := range cfg.Adapters {
		for _, r := range a.Rules {
			_ = r.Normalize()
		}
	}
	return cfg
}

// StoreExists reports whether the user-level store directory has been
// created (e.g. via `friday init`).
func StoreExists() (bool, error) {
	storeDir, err := UserStoreDir()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(storeDir); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func load(manifestPath string, scope Scope, storeDir, targetRoot string) (*Config, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestPath, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	cfg.Scope = scope
	cfg.ManifestPath = manifestPath
	cfg.StoreDir = storeDir
	cfg.TargetRoot = targetRoot
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d", manifestPath, cfg.Version)
	}
	for name, a := range cfg.Adapters {
		if a == nil {
			return nil, fmt.Errorf("adapter %q is empty", name)
		}
		if strings.HasPrefix(name, "~") {
			return nil, fmt.Errorf("adapter name %q: names starting with '~' are reserved", name)
		}
		if a.Target == "" {
			return nil, fmt.Errorf("adapter %q: target is required", name)
		}
		for i, r := range a.Rules {
			if err := r.Normalize(); err != nil {
				return nil, fmt.Errorf("adapter %q rule[%d]: %w", name, i, err)
			}
		}
	}
	return cfg, nil
}

// AdapterTargetAbs returns the resolved absolute target path for one adapter.
func (c *Config) AdapterTargetAbs(name string) (string, error) {
	a, ok := c.Adapters[name]
	if !ok {
		return "", fmt.Errorf("unknown adapter: %s", name)
	}
	return c.resolvePath(a.Target), nil
}

// resolvePath applies the path resolution rules described at the top of this file.
func (c *Config) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return filepath.Join(c.TargetRoot, p)
}

// AdapterNames returns adapter names in stable (alphabetical) order.
func (c *Config) AdapterNames() []string {
	names := make([]string, 0, len(c.Adapters))
	for n := range c.Adapters {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Save writes the config back to ManifestPath. The write is atomic — content
// goes to a sibling temp file first, then os.Rename swaps it in. Avoids
// leaving a half-written manifest on Ctrl-C / power loss.
func (c *Config) Save() error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return atomicio.WriteFile(c.ManifestPath, data, 0o644)
}

// SelectAdapters validates the requested adapter names; empty = all.
func (c *Config) SelectAdapters(names []string) ([]string, error) {
	if len(names) == 0 {
		return c.AdapterNames(), nil
	}
	for _, n := range names {
		if _, ok := c.Adapters[n]; !ok {
			return nil, fmt.Errorf("unknown adapter %q (defined: %s)", n, strings.Join(c.AdapterNames(), ", "))
		}
	}
	return names, nil
}
