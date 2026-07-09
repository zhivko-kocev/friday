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
	"github.com/zhivko-kocev/friday/internal/ui"
)

// Options controls a single setup run.
type Options struct {
	Agent       string // preset name; "" prompts
	DryRun      bool
	Force       bool
	Interactive bool // allow the rich TUI selection when the terminal supports it
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
	for _, v := range presets.EntryFiles {
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
		// An item that selects a whole tree (e.g. hooks with "hooks/**/*")
		// covers every rule whose from-pattern falls under it — not just the one
		// that happens to match the single probe file. Without this, a rule like
		// "hooks/hooks.json" is dropped whenever another file under hooks/ sorts
		// ahead of it and becomes the probe.
		for _, ip := range it.Patterns {
			if rules.Match(ip, pat) {
				return true
			}
		}
	}
	return false
}

// Run drives the whole flow: pick an agent, pick items, push them into the
// project at cwd. The prompt reader is injected so tests can script it;
// production passes os.Stdin. The conflict resolver comes from the caller so
// this package stays free of prompt duplication.
func Run(prompt io.Reader, cwd string, opts Options, onConflict engine.ConflictResolver, confirmWrite engine.ConfirmWriter) ([]engine.Change, error) {
	storeDir, err := config.UserStoreDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(storeDir); err != nil {
		return nil, fmt.Errorf("no user store at %s — run `friday init` first", storeDir)
	}

	reader := bufio.NewReader(prompt)

	tui := opts.Interactive && ui.Interactive()

	agent := opts.Agent
	if agent == "" {
		if agent, err = selectAgent(reader, presets.Names(), tui); err != nil {
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

	states := ItemStates(preset, agent, items, storeDir, cwd)
	selected, err := selectItems(reader, items, states, tui)
	if err != nil {
		return nil, err
	}

	cfg, only, skipped, err := Resolve(agent, selected, storeDir, cwd)
	// Surface unmapped items BEFORE pushing, so these notices precede any
	// interactive conflict prompts and the writes themselves.
	for _, it := range skipped {
		output.Skip("%s/%s — %s has no project mapping for it", it.Category, it.Name, agent)
	}
	if err != nil {
		return nil, err
	}
	return engine.Push(cfg, engine.Options{DryRun: opts.DryRun, Force: opts.Force, OnConflict: onConflict, ConfirmWrite: confirmWrite, Only: only})
}

// Resolve narrows the preset to the selected items and builds the project-scoped
// config the caller then pushes into. It is the non-pushing core shared by Run
// and the control room: it returns the `only` globs and the items no rule mapped
// (skipped) so the caller controls conflict resolution AND the order in which it
// reports skips relative to the push. Does not touch disk.
func Resolve(agent string, selected []Item, storeDir, cwd string) (cfg *config.Config, only []string, skipped []Item, err error) {
	preset, ok := presets.Get(agent)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unknown agent %q (available: %s)", agent, strings.Join(presets.Names(), ", "))
	}
	if len(preset.ProjectRules) == 0 {
		return nil, nil, nil, fmt.Errorf("preset %q has no project-scope mapping", agent)
	}
	ad, only, skipped := FilterAdapter(preset, selected)
	if ad == nil {
		return nil, nil, skipped, fmt.Errorf("nothing selected maps to %s at project scope", agent)
	}
	return projectConfig(agent, ad, storeDir, cwd), only, skipped, nil
}

// projectConfig builds an in-memory config whose relative targets resolve to
// the project dir instead of $HOME. NewDefault also normalizes the rules.
func projectConfig(agent string, ad *config.Adapter, storeDir, cwd string) *config.Config {
	return config.NewDefault(config.ScopeUser, storeDir, cwd, map[string]*config.Adapter{agent: ad})
}

// PromoteOptions controls a promote run — setup's inverse.
type PromoteOptions struct {
	Agent       string // preset name; "" prompts
	DryRun      bool
	Force       bool
	Interactive bool     // allow the rich TUI agent picker when supported
	Filters     []string // project-relative paths/globs to promote; empty = everything
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
		if agent, err = selectAgent(bufio.NewReader(prompt), presets.Names(), opts.Interactive && ui.Interactive()); err != nil {
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

// Suggestion is the display label and suggested-checked state for one catalog
// item. Both selection UIs — the text/huh path and the control room — build
// their rows from this, so the presented labels and pre-checked baseline can't
// diverge between them.
type Suggestion struct {
	Label   string
	Checked bool
}

// Suggestions derives per-item labels and the suggested selection from each
// item's applied/differs state. On a fresh project (no states) it pre-checks the
// universal baseline — core + rules — leaving skills/agents opt-in; on a re-run
// it pre-checks whatever is already applied.
func Suggestions(items []Item, states map[int]string) []Suggestion {
	fresh := len(states) == 0
	out := make([]Suggestion, len(items))
	for i, it := range items {
		label := it.Category + " / " + it.Name
		switch states[i] {
		case "in-sync":
			label += "  (applied)"
		case "differs":
			label += "  (differs)"
		}
		baseline := it.Category == "core" || it.Category == "rules"
		out[i] = Suggestion{Label: label, Checked: states[i] != "" || (fresh && baseline)}
	}
	return out
}

// ItemStates dry-runs the full project adapter and labels each item by what
// already sits at its destinations, so a re-run shows what's applied. Exported
// for the control room, which builds its own checklist and needs the same
// applied/differs labels and pre-check baseline the text/huh paths use.
func ItemStates(p presets.Preset, agent string, items []Item, storeDir, cwd string) map[int]string {
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

// selectAgent picks the agent via the rich TUI when the terminal supports it,
// falling back to the numbered text prompt otherwise (pipes, CI, tests).
func selectAgent(reader *bufio.Reader, names []string, tui bool) (string, error) {
	if !tui {
		return chooseAgent(reader, names)
	}
	choices := make([]ui.Choice, len(names))
	for i, n := range names {
		choices[i] = ui.Choice{Value: n, Label: n}
	}
	return ui.SelectOne("Which agent will this project use?", choices)
}

// selectItems is the TUI counterpart of chooseItems: a checkbox list where
// items already present in the project start checked (a suggested set), each
// labeled "category / name" with its in-sync/differs state.
func selectItems(reader *bufio.Reader, items []Item, states map[int]string, tui bool) ([]Item, error) {
	if !tui {
		return chooseItems(reader, items, states)
	}
	sugg := Suggestions(items, states)
	choices := make([]ui.Choice, len(items))
	for i := range items {
		choices[i] = ui.Choice{Value: strconv.Itoa(i), Label: sugg[i].Label, On: sugg[i].Checked}
	}
	vals, err := ui.MultiSelect("Select what to apply to this project", choices)
	if err != nil {
		return nil, err
	}
	// Return items in catalog order regardless of the order huh reports the
	// selection — concatenate rules build their output in selected order, so
	// this must match the text path (parseSelection sorts) or the two flows
	// would generate differently ordered files.
	idx := make([]int, 0, len(vals))
	for _, v := range vals {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(items) {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	selected := make([]Item, 0, len(idx))
	for _, i := range idx {
		selected = append(selected, items[i])
	}
	return selected, nil
}

func chooseAgent(reader *bufio.Reader, names []string) (string, error) {
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
