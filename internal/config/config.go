// Package config loads friday.yaml and resolves paths for the user-level store.
//
// The manifest at $HOME/.friday/friday.yaml controls every agent dir on the
// machine (~/.claude, ~/.codex, ~/.gemini, ...). The store dir IS the
// directory that contains the manifest — flat, no nested store/.
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
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/rules"
)

const ManifestName = "friday.yaml"

// ErrNoManifest signals that no friday.yaml was found at the expected
// location. Callers (CLI, initcmd) decide how to react — typically by
// falling back to the built-in presets.
var ErrNoManifest = errors.New("no friday.yaml")

// Scope identifies which store the config was loaded from: the user-level
// store (~/.friday) or a project's own git-tracked config (friday setup /
// promote).
type Scope int

const (
	ScopeUser Scope = iota
	ScopeProject
)

func (s Scope) String() string {
	switch s {
	case ScopeProject:
		return "project"
	default:
		return "user"
	}
}

// Config is the parsed friday.yaml plus runtime context (scope, paths).
type Config struct {
	Version  int                 `yaml:"version"`
	Adapters map[string]*Adapter `yaml:"adapters"`
	// Sources lists additional git-backed content sources composed into the
	// logical store, lowest precedence first. Absent (the common case) leaves
	// the primary store as the only root — see internal/source.
	Sources []*SourceSpec `yaml:"sources,omitempty"`

	// Filled by Load.
	Scope        Scope  `yaml:"-"`
	ManifestPath string `yaml:"-"` // absolute path to friday.yaml
	StoreDir     string `yaml:"-"` // dir containing the manifest = canonical store
	TargetRoot   string `yaml:"-"` // $HOME for user scope, CWD for project scope

	// Warnings collects non-fatal manifest problems found at load time — a
	// retired strategy that was dropped, an adapter whose name was renamed
	// in a later release. The CLI prints these once per run so a manifest
	// written by an older friday degrades loudly instead of silently.
	Warnings []string `yaml:"-"`
}

// SourceSpec is one entry under `sources:` in friday.yaml: a git remote
// cloned to <StoreDir>/.sources/<name>. Ref pins a branch, tag, or sha;
// empty means the remote's default branch.
type SourceSpec struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	Ref  string `yaml:"ref,omitempty"`
}

// sourceNamePattern is the same shape as an adapter name plus a leading
// digit allowance — "local" is reserved for the primary, so a manifest
// source may never claim it.
var sourceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// LegacyAdapterNames maps an adapter key seeded by an older release to its
// current name. Kept as data so doctor, migrate, and load all agree on what
// counts as a stale manifest.
var LegacyAdapterNames = map[string]string{
	"antigravity": "gemini",
	"pi":          "oh-my-pi",
}

// reservedDeviceNames are the DOS device basenames Windows resolves anywhere
// in a path, whatever the directory. They match sourceNamePattern, so without
// an explicit check `.sources/nul` validates and then cannot be materialized:
// mkdir reports success and creates nothing, and writes beneath it vanish into
// the null device. The result is a source that is permanently "uncloned",
// which is indistinguishable from a legitimately empty one.
var reservedDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateSourceName reports whether name is usable as a `sources:` entry.
// Exported because every writer (`friday source add`) must apply the same
// rule load() does — validating only on read lets a writer persist a
// manifest friday can no longer open, and lets a name escape .sources/.
//
// The rules are checked on every platform, not just Windows: a manifest is
// meant to be shared, so a name that breaks on a teammate's machine has to be
// refused on the machine that writes it.
func ValidateSourceName(name string) error {
	if !sourceNamePattern.MatchString(name) {
		return fmt.Errorf("source name %q must match %s", name, sourceNamePattern.String())
	}
	if name == "local" {
		return fmt.Errorf("source name %q is reserved for the primary store", name)
	}
	if reservedDeviceNames[name] {
		return fmt.Errorf("source name %q is a reserved Windows device name — no directory can hold it", name)
	}
	return nil
}

// Adapter is one named entry under `adapters:` in friday.yaml.
type Adapter struct {
	Target string        `yaml:"target"`
	Rules  []*rules.Rule `yaml:"rules"`
	// ProjectTarget and ProjectRules mirror Target/Rules for project scope
	// (friday setup / promote): the place this adapter's config lives inside
	// a repo, and the rules that render into it. Both empty means the
	// adapter has no project-scope story (the shared "agents" adapter, whose
	// ~/.agents skills tree is a machine-wide discovery path with no
	// in-repo equivalent).
	ProjectTarget string        `yaml:"project_target,omitempty"`
	ProjectRules  []*rules.Rule `yaml:"project_rules,omitempty"`
}

// UserStoreDir returns the canonical user-level store directory:
// $FRIDAY_HOME if set (following RUSTUP_HOME/CARGO_HOME/GH_CONFIG_DIR —
// the value is the store dir itself, not a parent ".friday" gets appended
// to; friday's own FRIDAY_INSTALL_DIR is a different, installer-only
// variable and is never consulted here), else $HOME/.friday. One store per
// user, shared across every project on the machine.
//
// The env value is resolved, not trusted verbatim: a leading "~/" expands
// like it does everywhere else in a manifest (no shell expands a tilde
// inside $env:FRIDAY_HOME on Windows, so the literal would otherwise become
// a directory named "~"), and the result is made absolute because StoreDir
// seeds ManifestPath and every drift key derived from it — a relative value
// would silently point at a different store per invocation CWD.
func UserStoreDir() (string, error) {
	if dir := os.Getenv("FRIDAY_HOME"); dir != "" {
		if strings.HasPrefix(dir, "~/") || dir == "~" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
		return filepath.Abs(dir)
	}
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
		for _, r := range a.ProjectRules {
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
	// A manifest seeded by an older release names adapters and strategies
	// that no longer exist. Dropping those rules with a warning keeps every
	// command usable; `friday migrate` rewrites the manifest properly.
	//
	// Sorted, like the legacy-name loop below: warnings are user-facing text
	// that ends up in bug reports, so their order must not change run to run
	// for an identical manifest.
	var retired []string
	for _, name := range cfg.AdapterNames() {
		a := cfg.Adapters[name]
		if a == nil {
			return nil, fmt.Errorf("adapter %q is empty", name)
		}
		if strings.HasPrefix(name, "~") {
			return nil, fmt.Errorf("adapter name %q: names starting with '~' are reserved", name)
		}
		if a.Target == "" {
			return nil, fmt.Errorf("adapter %q: target is required", name)
		}
		keep := make([]*rules.Rule, 0, len(a.Rules))
		keepProject := make([]*rules.Rule, 0, len(a.ProjectRules))
		for i, r := range a.Rules {
			if err := r.Normalize(); err != nil {
				if errors.Is(err, rules.ErrRetiredStrategy) {
					retired = append(retired, fmt.Sprintf("adapter %q rule[%d]: %v", name, i, err))
					continue
				}
				return nil, fmt.Errorf("adapter %q rule[%d]: %w", name, i, err)
			}
			keep = append(keep, r)
		}
		for i, r := range a.ProjectRules {
			if err := r.Normalize(); err != nil {
				if errors.Is(err, rules.ErrRetiredStrategy) {
					retired = append(retired, fmt.Sprintf("adapter %q project_rule[%d]: %v", name, i, err))
					continue
				}
				return nil, fmt.Errorf("adapter %q project_rule[%d]: %w", name, i, err)
			}
			keepProject = append(keepProject, r)
		}
		// An adapter whose every rule was retired loads fine and then does
		// nothing forever — zero changes on every push, with no message saying
		// why. The per-rule notes above explain each drop but never that the
		// adapter is now inert, which is the part the user has to act on.
		if len(a.Rules) > 0 && len(keep) == 0 {
			retired = append(retired, fmt.Sprintf(
				"adapter %q: every rule was retired — it will sync nothing until you run `friday migrate` or add rules", name))
		}
		if len(a.ProjectRules) > 0 && len(keepProject) == 0 {
			retired = append(retired, fmt.Sprintf(
				"adapter %q: every project_rule was retired — `friday setup` will apply nothing for it until you run `friday migrate`", name))
		}
		a.Rules = keep
		a.ProjectRules = keepProject
	}
	if err := validateSources(cfg.Sources); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	cfg.Warnings = append(cfg.Warnings, retired...)
	for _, name := range cfg.AdapterNames() {
		if canonical, ok := LegacyAdapterNames[name]; ok {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
				"adapter %q was renamed to %q — run `friday migrate` to update friday.yaml", name, canonical))
		}
	}
	return cfg, nil
}

// validateSources enforces the manifest-source naming rules: a lowercase
// slug (never "local", the primary's reserved name), unique across the
// list, with a non-empty url — the one field a source cannot function
// without. Absent sources (the common case) validates trivially.
func validateSources(specs []*SourceSpec) error {
	seen := make(map[string]bool, len(specs))
	for i, s := range specs {
		if s == nil {
			return fmt.Errorf("sources[%d] is empty", i)
		}
		if err := ValidateSourceName(s.Name); err != nil {
			return fmt.Errorf("sources[%d]: %w", i, err)
		}
		if seen[s.Name] {
			return fmt.Errorf("sources[%d]: duplicate source name %q", i, s.Name)
		}
		seen[s.Name] = true
		if s.URL == "" {
			return fmt.Errorf("sources[%d] %q: url is required", i, s.Name)
		}
	}
	return nil
}

// SourcesDir returns the directory additional sources clone into:
// <StoreDir>/.sources. The dot prefix is load-bearing, not cosmetic, but it
// only works because the walkers enforce it: rules.Expand prunes dot-dirs and
// its `**` matcher stops at a dot segment, and lint.Run's walk prunes them the
// same way (its agnix pass additionally drops SARIF paths under this dir). So
// a source's content reaches an agent, and a finding reaches the user, once —
// through that source's own root, never a second time through the primary's
// globs. A pattern whose literal anchor names .sources still opts in.
// ValidateSourceName keeps every clone inside this dir.
func (c *Config) SourcesDir() string {
	return filepath.Join(c.StoreDir, ".sources")
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
	// A writer must never persist a manifest friday can no longer open,
	// because every command — including the one that would undo the damage —
	// loads it first. EditManifest, the other writer, validates its own
	// output for the same reason.
	if err := validateSources(c.Sources); err != nil {
		return err
	}
	for _, name := range c.AdapterNames() {
		a := c.Adapters[name]
		if a == nil {
			return fmt.Errorf("adapter %q is empty", name)
		}
		if a.Target == "" {
			return fmt.Errorf("adapter %q: target is required", name)
		}
	}
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
