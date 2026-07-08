package presets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zhivko-kocev/friday/internal/rules"
)

// PluginsDirName is the store subdirectory scanned for out-of-tree presets.
const PluginsDirName = "plugins"

// pluginFile is the YAML schema of one plugin preset — the same shape as a
// friday.yaml adapter, plus an optional name (default: the file stem).
type pluginFile struct {
	Name          string        `yaml:"name,omitempty"`
	Target        string        `yaml:"target"`
	Rules         []*rules.Rule `yaml:"rules"`
	ProjectTarget string        `yaml:"project_target,omitempty"`
	ProjectRules  []*rules.Rule `yaml:"project_rules,omitempty"`
}

// LoadPlugins parses <storeDir>/plugins/*.yaml into presets keyed by name.
// Parse or validation failures are collected per file, never fatal — one
// broken plugin must not take the CLI down. Plugins layer between built-ins
// and friday.yaml: a plugin may shadow a built-in, an explicit manifest
// always wins (it is never silently mutated by plugins).
func LoadPlugins(storeDir string) (map[string]Preset, []error) {
	dir := filepath.Join(storeDir, PluginsDirName)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}

	out := map[string]Preset{}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
			continue
		}
		p, err := loadPluginFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", name, err))
			continue
		}
		if _, dup := out[p.Name]; dup {
			errs = append(errs, fmt.Errorf("plugin %s: duplicate preset name %q", name, p.Name))
			continue
		}
		out[p.Name] = p
	}
	return out, errs
}

func loadPluginFile(path string) (Preset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Preset{}, err
	}
	var pf pluginFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return Preset{}, err
	}
	if pf.Name == "" {
		base := filepath.Base(path)
		pf.Name = strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
	}
	if strings.HasPrefix(pf.Name, "~") {
		return Preset{}, fmt.Errorf("preset names starting with '~' are reserved")
	}
	if pf.Target == "" {
		return Preset{}, fmt.Errorf("target is required")
	}
	if len(pf.Rules) == 0 {
		return Preset{}, fmt.Errorf("at least one rule is required")
	}
	for i, r := range pf.Rules {
		if err := r.Normalize(); err != nil {
			return Preset{}, fmt.Errorf("rule[%d]: %w", i, err)
		}
	}
	for i, r := range pf.ProjectRules {
		if err := r.Normalize(); err != nil {
			return Preset{}, fmt.Errorf("project_rule[%d]: %w", i, err)
		}
	}
	return Preset{
		Name:          pf.Name,
		Target:        pf.Target,
		Rules:         pf.Rules,
		ProjectTarget: pf.ProjectTarget,
		ProjectRules:  pf.ProjectRules,
	}, nil
}

// ValidatePluginFile parses and validates a single plugin preset file,
// returning its resolved preset name. `friday plugin add` uses it to reject a
// malformed file (or one that runs no rules) before installing it.
func ValidatePluginFile(path string) (string, error) {
	p, err := loadPluginFile(path)
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

// AllAdaptersWith returns the built-in presets overlaid with the store's
// plugins — the fallback set used when friday.yaml is absent.
func AllAdaptersWith(storeDir string) (map[string]Preset, []error) {
	out := AllAdapters()
	plugins, errs := LoadPlugins(storeDir)
	for name, p := range plugins {
		out[name] = p
	}
	return out, errs
}

// GetWith resolves a preset name against the built-ins overlaid with the
// store's plugins — the same set push and pull operate on, so a plugin
// preset is equally visible to setup and promote. A plugin may shadow a
// built-in. Load errors are swallowed here; `friday plugin validate`
// surfaces them.
func GetWith(storeDir, name string) (Preset, bool) {
	if plugins, _ := LoadPlugins(storeDir); plugins != nil {
		if p, ok := plugins[name]; ok {
			return p, true
		}
	}
	return Get(name)
}

// NamesWith returns every available preset name — built-ins plus the store's
// plugins — in alphabetical order.
func NamesWith(storeDir string) []string {
	names := Names()
	plugins, _ := LoadPlugins(storeDir)
	for n := range plugins {
		if _, builtin := registry[n]; !builtin {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}
