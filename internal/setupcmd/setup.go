// Package setupcmd implements `friday setup`: interactively apply selected
// knowledge from the user store into the current project's agent config,
// which the project's own git tracks. There is no project-level .friday —
// the store stays the single source at user scope, and setup passes chosen
// pieces one level down.
package setupcmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// Options controls a single setup run.
type Options struct {
	Agent  string // preset name; "" prompts
	DryRun bool
	Force  bool
}

// Item is one selectable piece of the store: the entry file, a single rule /
// agent / command / standard / connector file, a whole skill directory, or
// the hooks tree.
type Item struct {
	Category string   // core | rules | agents | commands | standards | connectors | skills | hooks
	Name     string   // display name (file stem or skill dir)
	Patterns []string // store-relative from-patterns selecting the item
	Probe    string   // a real store-relative file, used to test rule coverage
}

// catalogSpecs drive per-file item discovery; skills are handled separately
// (one item per skill directory), hooks as a single item.
var catalogSpecs = []struct{ category, glob string }{
	{"rules", "rules/*.md"},
	{"agents", "agents/*.md"},
	{"commands", "commands/*.md"},
	{"standards", "standards/*.md"},
	{"connectors", "connectors/*.md"},
}

// Catalog enumerates what the store offers, in stable display order.
func Catalog(storeDir string) ([]Item, error) {
	var items []Item
	for _, v := range []string{"core.md", "core/core.md", "identity.md"} {
		matches, err := rules.Expand(storeDir, v)
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			items = append(items, Item{Category: "core", Name: "core", Patterns: []string{v}, Probe: v})
			break
		}
	}
	for _, spec := range catalogSpecs {
		matches, err := rules.Expand(storeDir, spec.glob)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			stem := strings.TrimSuffix(path.Base(m), path.Ext(m))
			items = append(items, Item{Category: spec.category, Name: stem, Patterns: []string{m}, Probe: m})
		}
	}
	skillDirs, err := os.ReadDir(filepath.Join(storeDir, "skills"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range skillDirs {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pat := "skills/" + e.Name() + "/**/*"
		matches, err := rules.Expand(storeDir, pat)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			continue
		}
		items = append(items, Item{Category: "skills", Name: e.Name(), Patterns: []string{pat}, Probe: matches[0]})
	}
	if hooks, err := rules.Expand(storeDir, "hooks/**/*"); err != nil {
		return nil, err
	} else if len(hooks) > 0 {
		items = append(items, Item{Category: "hooks", Name: "hooks", Patterns: []string{"hooks/**/*"}, Probe: hooks[0]})
	}
	return items, nil
}

// FilterAdapter narrows the preset's project rules to the selected items.
// Concatenate rules get their from-list rebuilt from the selected files (the
// output must contain exactly the selection). Copy rules keep their original
// patterns — narrowing them would move the {relpath} anchor and mangle
// destination paths — and the returned `only` globs filter their planned
// changes down to the selection instead. Rules covering nothing are dropped;
// items the preset has no mapping for come back as skipped.
func FilterAdapter(p presets.Preset, selected []Item) (ad *config.Adapter, only []string, skipped []Item) {
	ad = &config.Adapter{Target: p.ProjectTarget}
	used := make([]bool, len(selected))
	for _, r := range p.ProjectRules {
		var covered []Item
		for i, it := range selected {
			if ruleCovers(r, it) {
				covered = append(covered, it)
				used[i] = true
			}
		}
		if len(covered) == 0 {
			continue
		}
		nr := *r
		if r.Strategy == rules.StrategyConcatenate {
			nr.From = nil
			for _, it := range covered {
				nr.From = append(nr.From, it.Patterns...)
			}
		}
		for _, it := range covered {
			only = append(only, it.Patterns...)
		}
		ad.Rules = append(ad.Rules, &nr)
	}
	for i, u := range used {
		if !u {
			skipped = append(skipped, selected[i])
		}
	}
	if len(ad.Rules) == 0 {
		return nil, nil, skipped
	}
	return ad, only, skipped
}

func ruleCovers(r *rules.Rule, it Item) bool {
	for _, pat := range r.From {
		if slices.Contains(it.Patterns, pat) || rules.Match(pat, it.Probe) {
			return true
		}
	}
	return false
}

// Run drives the whole flow: pick an agent, pick items, push them into the
// project at cwd. The prompt reader is injected so tests can script it;
// production passes os.Stdin. The conflict resolver comes from the caller so
// this package stays free of prompt duplication.
func Run(prompt io.Reader, cwd string, opts Options, onConflict engine.ConflictResolver) ([]engine.Change, error) {
	storeDir, err := config.UserStoreDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(storeDir); err != nil {
		return nil, fmt.Errorf("no user store at %s — run `friday init` first", storeDir)
	}

	reader := bufio.NewReader(prompt)

	agent := opts.Agent
	if agent == "" {
		if agent, err = chooseAgent(reader); err != nil {
			return nil, err
		}
	}
	preset, ok := presets.Get(agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (available: %s)", agent, strings.Join(presets.Names(), ", "))
	}
	if len(preset.ProjectRules) == 0 {
		return nil, fmt.Errorf("preset %q has no project-scope mapping", agent)
	}

	items, err := Catalog(storeDir)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("store at %s has nothing to apply — add core.md, rules/, skills/, ...", storeDir)
	}

	states := itemStates(preset, agent, items, storeDir, cwd)
	selected, err := chooseItems(reader, items, states)
	if err != nil {
		return nil, err
	}

	ad, only, skipped := FilterAdapter(preset, selected)
	for _, it := range skipped {
		output.Skip("%s/%s — %s has no project mapping for it", it.Category, it.Name, agent)
	}
	if ad == nil {
		return nil, fmt.Errorf("nothing selected maps to %s at project scope", agent)
	}

	cfg := projectConfig(agent, ad, storeDir, cwd)
	return engine.Push(cfg, engine.Options{DryRun: opts.DryRun, Force: opts.Force, OnConflict: onConflict, Only: only})
}

// projectConfig builds an in-memory config whose relative targets resolve to
// the project dir instead of $HOME. NewDefault also normalizes the rules.
func projectConfig(agent string, ad *config.Adapter, storeDir, cwd string) *config.Config {
	return config.NewDefault(config.ScopeUser, storeDir, cwd, map[string]*config.Adapter{agent: ad})
}

// PromoteOptions controls a promote run — setup's inverse.
type PromoteOptions struct {
	Agent   string // preset name; "" prompts
	DryRun  bool
	Force   bool
	Filters []string // project-relative paths/globs to promote; empty = everything
}

// Promote captures project-level agent config back into the user store —
// the upward counterpart of Run. Knowledge that landed in a project from
// somewhere else (a hand-added skill in .claude/skills/, a teammate's agent
// file) becomes store knowledge, ready for `friday remote propose`.
// Reverse expansion means files friday never wrote are picked up too;
// concatenated instruction files (CLAUDE.md, AGENTS.md) are irreversible and
// reported as unsupported.
func Promote(prompt io.Reader, cwd string, opts PromoteOptions, onConflict engine.ConflictResolver) ([]engine.Change, error) {
	storeDir, err := config.UserStoreDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(storeDir); err != nil {
		return nil, fmt.Errorf("no user store at %s — run `friday init` first", storeDir)
	}

	agent := opts.Agent
	if agent == "" {
		if agent, err = chooseAgent(bufio.NewReader(prompt)); err != nil {
			return nil, err
		}
	}
	preset, ok := presets.Get(agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (available: %s)", agent, strings.Join(presets.Names(), ", "))
	}
	if len(preset.ProjectRules) == 0 {
		return nil, fmt.Errorf("preset %q has no project-scope mapping", agent)
	}

	cfg := projectConfig(agent, preset.ProjectAdapter(), storeDir, cwd)
	return engine.Import(cfg, engine.Options{
		DryRun:     opts.DryRun,
		Force:      opts.Force,
		OnConflict: onConflict,
		Only:       promoteGlobs(opts.Filters),
	})
}

// promoteGlobs widens each user-supplied filter so a bare directory path
// selects everything under it: ".claude/skills/foo" also matches
// ".claude/skills/foo/**/*". Import sources are project-relative, which is
// exactly what users see on disk.
func promoteGlobs(filters []string) []string {
	var out []string
	for _, f := range filters {
		f = strings.Trim(strings.ReplaceAll(f, "\\", "/"), "/")
		if f == "" {
			continue
		}
		out = append(out, f, f+"/**/*")
	}
	return out
}

// itemStates dry-runs the full project adapter and labels each item by what
// already sits at its destinations, so a re-run shows what's applied.
func itemStates(p presets.Preset, agent string, items []Item, storeDir, cwd string) map[int]string {
	states := make(map[int]string, len(items))
	changes, err := engine.Push(projectConfig(agent, p.ProjectAdapter(), storeDir, cwd), engine.Options{DryRun: true})
	if err != nil {
		return states // labels are advisory; selection still works without them
	}
	for i, it := range items {
		for _, ch := range changes {
			if !itemCoversAnySource(it, ch.Sources) {
				continue
			}
			switch ch.Action {
			case engine.ActionUpdate, engine.ActionConflict:
				states[i] = "differs"
			case engine.ActionInSync:
				if states[i] == "" {
					states[i] = "in-sync"
				}
			}
		}
	}
	return states
}

func itemCoversAnySource(it Item, sources []string) bool {
	for _, s := range sources {
		for _, pat := range it.Patterns {
			if rules.Match(pat, s) {
				return true
			}
		}
	}
	return false
}

func chooseAgent(reader *bufio.Reader) (string, error) {
	names := presets.Names()
	output.Info("Which agent will this project use?")
	for i, n := range names {
		fmt.Printf("  %d) %s\n", i+1, n)
	}
	fmt.Print("  > ")
	line, err := readLine(reader)
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("an agent is required (pick a number or name)")
	}
	if n, err := strconv.Atoi(line); err == nil {
		if n < 1 || n > len(names) {
			return "", fmt.Errorf("pick 1-%d", len(names))
		}
		return names[n-1], nil
	}
	return line, nil
}

func chooseItems(reader *bufio.Reader, items []Item, states map[int]string) ([]Item, error) {
	output.Info("Select what to apply to this project:")
	lastCategory := ""
	for i, it := range items {
		if it.Category != lastCategory {
			fmt.Printf("  %s\n", it.Category)
			lastCategory = it.Category
		}
		label := ""
		if s := states[i]; s != "" {
			label = "  [" + s + "]"
		}
		fmt.Printf("    %2d) %s%s\n", i+1, it.Name, label)
	}
	fmt.Print("  choose (e.g. 1,3,5-7 | all | blank = all) > ")
	line, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	idx, err := parseSelection(line, len(items))
	if err != nil {
		return nil, err
	}
	selected := make([]Item, 0, len(idx))
	for _, i := range idx {
		selected = append(selected, items[i])
	}
	return selected, nil
}

// readLine returns the next line; EOF with no data reads as blank, matching
// initcmd's prompt behavior for piped stdin.
func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// parseSelection turns "1,3,5-7" into zero-based indices; "a"/"all"/blank
// selects everything.
func parseSelection(line string, n int) ([]int, error) {
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" || line == "a" || line == "all" {
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		return all, nil
	}
	seen := make(map[int]bool)
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("bad selection %q", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, fmt.Errorf("bad selection %q", part)
			}
		}
		if a > b {
			a, b = b, a
		}
		for v := a; v <= b; v++ {
			if v < 1 || v > n {
				return nil, fmt.Errorf("selection %d out of range 1-%d", v, n)
			}
			seen[v-1] = true
		}
	}
	out := make([]int, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, nil
}
