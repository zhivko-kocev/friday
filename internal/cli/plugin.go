package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
	"github.com/zhivko-kocev/friday/internal/ui"
)

// cmdPlugin manages out-of-tree presets in ~/.friday/plugins/. A plugin is a
// declarative YAML preset — never executable code, so trust reduces to "which
// repo and commit did this come from," recorded in plugins/friday.lock. Plugins
// join the no-manifest fallback set (a plugin may shadow a built-in); an
// explicit friday.yaml always wins and is never mutated by plugins.
func cmdPlugin(args []string) int {
	if len(args) == 0 {
		output.Err("usage: friday plugin add|upgrade|remove|list|validate")
		return 1
	}
	storeDir, err := config.UserStoreDir()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	switch args[0] {
	case "list":
		return pluginList(storeDir, args[1:])
	case "validate":
		return pluginValidate(storeDir)
	case "add":
		return pluginAdd(storeDir, args[1:])
	case "upgrade":
		return pluginUpgrade(storeDir, args[1:])
	case "remove":
		return pluginRemove(storeDir, args[1:])
	default:
		output.Err("unknown plugin subcommand %q (want: add, upgrade, remove, list, validate)", args[0])
		return 1
	}
}

func pluginList(storeDir string, args []string) int {
	showURLs := len(args) == 1 && args[0] == "--urls"
	plugins, errs := presets.LoadPlugins(storeDir)
	for _, e := range errs {
		output.Warn("%v", e)
	}
	if len(plugins) == 0 && len(errs) == 0 {
		output.Dim("no plugins — `friday plugin add <name> <git-url>` or drop a .yaml into %s", filepath.Join(storeDir, presets.PluginsDirName))
		return 0
	}
	builtin := map[string]bool{}
	for _, n := range presets.Names() {
		builtin[n] = true
	}
	lock := loadPluginLock(storeDir)
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := plugins[name]
		note := ""
		if builtin[name] {
			note = "  (shadows the built-in preset)"
		}
		output.OK("%-12s → %s  (%d rule(s))%s", name, p.Target, len(p.Rules), note)
		if showURLs {
			if pin, ok := lock.Plugins[name]; ok {
				output.Dim("    %s @ %s", pin.URL, shortSHA(pin.SHA))
			} else {
				output.Dim("    (local — no recorded origin)")
			}
		}
	}
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func pluginValidate(storeDir string) int {
	_, errs := presets.LoadPlugins(storeDir)
	for _, e := range errs {
		output.Err("%v", e)
	}
	if len(errs) > 0 {
		return 1
	}
	output.OK("all plugins valid")
	return 0
}

func pluginAdd(storeDir string, args []string) int {
	if len(args) != 2 {
		output.Err("usage: friday plugin add <name> <git-url>")
		return 1
	}
	name, url := args[0], args[1]
	if err := validatePluginName(name); err != nil {
		output.Err("%v", err)
		return 1
	}
	if !git.Available() {
		output.Err("git not in PATH — needed to fetch plugins")
		return 1
	}
	dest := filepath.Join(storeDir, presets.PluginsDirName, name+".yaml")
	if _, err := os.Stat(dest); err == nil {
		output.Err("plugin %q already installed — use `friday plugin upgrade %s`", name, name)
		return 1
	}

	pin, data, err := fetchPlugin(url)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if err := atomicio.WriteFile(dest, data, 0o644); err != nil {
		output.Err("write %s: %v", dest, err)
		return 1
	}

	lock := loadPluginLock(storeDir)
	pin.File = name + ".yaml"
	lock.Plugins[name] = pin
	if err := lock.save(storeDir); err != nil {
		output.Err("write lock: %v", err)
		return 1
	}
	output.OK("added plugin %q from %s @ %s", name, pin.URL, shortSHA(pin.SHA))
	output.Dim("it now layers into the built-in presets; `friday status --origin` shows it")
	return 0
}

func pluginUpgrade(storeDir string, args []string) int {
	lock := loadPluginLock(storeDir)
	all := len(args) == 1 && args[0] == "--all"
	var names []string
	switch {
	case all:
		for name := range lock.Plugins {
			names = append(names, name)
		}
		sort.Strings(names)
	case len(args) == 1:
		names = []string{args[0]}
	default:
		output.Err("usage: friday plugin upgrade <name> | --all")
		return 1
	}
	if len(names) == 0 {
		output.Dim("no git-fetched plugins to upgrade")
		return 0
	}

	failed := false
	for _, name := range names {
		pin, ok := lock.Plugins[name]
		if !ok {
			output.Warn("%s: not fetched via `plugin add` (no lock entry) — skipping", name)
			continue
		}
		newPin, data, err := fetchPlugin(pin.URL)
		if err != nil {
			output.Err("%s: %v", name, err)
			failed = true
			continue
		}
		dest := filepath.Join(storeDir, presets.PluginsDirName, pin.File)
		if err := atomicio.WriteFile(dest, data, 0o644); err != nil {
			output.Err("%s: write %s: %v", name, dest, err)
			failed = true
			continue
		}
		newPin.File = pin.File
		lock.Plugins[name] = newPin
		if newPin.SHA == pin.SHA {
			output.OK("%s already up to date (%s)", name, shortSHA(pin.SHA))
		} else {
			output.OK("%s upgraded %s → %s", name, shortSHA(pin.SHA), shortSHA(newPin.SHA))
		}
	}
	if err := lock.save(storeDir); err != nil {
		output.Err("write lock: %v", err)
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

func pluginRemove(storeDir string, args []string) int {
	if len(args) != 1 {
		output.Err("usage: friday plugin remove <name>")
		return 1
	}
	name := args[0]
	lock := loadPluginLock(storeDir)
	file := name + ".yaml"
	if pin, ok := lock.Plugins[name]; ok {
		file = pin.File
		delete(lock.Plugins, name)
	}
	dest := filepath.Join(storeDir, presets.PluginsDirName, file)
	if err := os.Remove(dest); err != nil {
		output.Err("remove %s: %v", dest, err)
		return 1
	}
	if err := lock.save(storeDir); err != nil {
		output.Err("write lock: %v", err)
		return 1
	}
	output.OK("removed plugin %q", name)
	return 0
}

// fetchPlugin shallow-clones url into a temp dir, locates and validates the
// preset YAML, and returns the pin (url + resolved commit) plus the file's
// bytes. The clone is discarded — only the declarative YAML is installed, never
// any executable content from the repo.
func fetchPlugin(url string) (pluginPin, []byte, error) {
	tmp, err := os.MkdirTemp("", "friday-plugin-*")
	if err != nil {
		return pluginPin{}, nil, err
	}
	defer os.RemoveAll(tmp)
	repo := filepath.Join(tmp, "repo")

	err = ui.WithSpinner("fetching "+url, func() error { return git.Clone(url, repo) })
	if err != nil {
		return pluginPin{}, nil, fmt.Errorf("clone %s: %w", url, err)
	}
	file, err := findPresetYAML(repo)
	if err != nil {
		return pluginPin{}, nil, err
	}
	if _, err := presets.ValidatePluginFile(file); err != nil {
		return pluginPin{}, nil, fmt.Errorf("%s is not a valid preset: %w", filepath.Base(file), err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return pluginPin{}, nil, err
	}
	return pluginPin{URL: url, SHA: git.HeadSHA(repo)}, data, nil
}

// findPresetYAML locates the preset file in a cloned plugin repo: a
// friday-plugin.yaml if present, else the single .yaml/.yml at the root.
func findPresetYAML(dir string) (string, error) {
	for _, pref := range []string{"friday-plugin.yaml", "friday-plugin.yml"} {
		p := filepath.Join(dir, pref)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var yamls []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
			yamls = append(yamls, n)
		}
	}
	switch len(yamls) {
	case 0:
		return "", fmt.Errorf("no preset .yaml in repo (expected friday-plugin.yaml or a single .yaml at the root)")
	case 1:
		return filepath.Join(dir, yamls[0]), nil
	default:
		sort.Strings(yamls)
		return "", fmt.Errorf("repo has multiple .yaml files (%s) — name the intended preset friday-plugin.yaml", strings.Join(yamls, ", "))
	}
}

func validatePluginName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("plugin name is empty")
	case strings.HasPrefix(name, "~"):
		return fmt.Errorf("plugin names starting with '~' are reserved")
	case name == "." || name == ".." || strings.ContainsAny(name, `/\:`):
		return fmt.Errorf("plugin name %q must be a simple identifier (no path separators)", name)
	}
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "(unknown)"
	}
	return sha
}
