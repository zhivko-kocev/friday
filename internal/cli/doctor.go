package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"mvdan.cc/sh/v3/syntax"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/lint"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// newDoctorCommand runs a read-only health check on the local install.
// Surfaces:
//   - whether the user store exists and is a git repo
//   - manifest validity (or fallback-to-presets mode)
//   - per-adapter installed-vs-missing status
//   - drift state across every installed adapter
//
// Exits non-zero only when something is actually broken; "no agents
// installed" is informational, not an error. `friday doctor <file>` (the
// folded-in `explain`) answers which adapter + rule produces that file
// instead of running the full check.
func newDoctorCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "doctor",
		Category:  "advanced",
		Usage:     "health-check the install + lint the store; `doctor <file>` explains a mapping",
		ArgsUsage: "[target-file]",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "json", Usage: "machine-readable store-check findings (for CI)"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			pos := cmd.Args().Slice()
			if len(pos) == 1 {
				return exitFromCode(explainFile(pos[0]))
			}
			if len(pos) > 1 {
				output.Err("usage: friday doctor [target-file] [--json]")
				return ucli.Exit("", 1)
			}
			if cmd.Bool("json") {
				return exitFromCode(doctorJSON())
			}
			return exitFromCode(runDoctorHealthCheck())
		},
	}
}

// exitFromCode adapts the package's many int-returning helpers to urfave's
// error-returning Action signature.
func exitFromCode(code int) error {
	if code != 0 {
		return ucli.Exit("", code)
	}
	return nil
}

// runDoctorHealthCheck is the full human-readable health check, run with no
// positional file argument and without --json.
func runDoctorHealthCheck() int {

	output.Header("friday doctor")
	bad := 0

	// 1. store directory
	storeDir, err := config.UserStoreDir()
	if err != nil {
		output.Err("resolve store dir: %v", err)
		return 1
	}
	if _, err := os.Stat(storeDir); err != nil {
		output.Err("store missing: %s", storeDir)
		output.Dim("hint: run `friday init`")
		return 1
	}
	output.OK("store: %s", storeDir)

	// 2. git status
	if git.Available() {
		if git.IsRepo(storeDir) {
			output.OK("git: store is a git repo")
			if dirty, _ := git.HasUncommitted(storeDir); dirty {
				output.Warn("git: uncommitted changes in store (run `friday remote push -m ...`)")
			}
		} else {
			output.Warn("git: store is not a git repo (run `git init` inside %s to enable remote sync)", storeDir)
		}
	} else {
		output.Warn("git: not in PATH — `friday remote` will not work")
	}

	// 3. entry file (core.md / core/core.md / legacy identity.md).
	// Hooks state is reported alongside drift below, where the config and the
	// dry-run plan are already available.
	checkEntryFiles(storeDir)

	// 4. manifest
	cfg, err := config.LoadUser()
	switch {
	case err == nil:
		output.OK("manifest: %s (%d adapter(s))", cfg.ManifestPath, len(cfg.Adapters))
	case err == config.ErrNoManifest:
		output.Warn("manifest: no friday.yaml — falling back to all built-in presets")
		cfg, err = loadUserOrDefault()
		if err != nil {
			output.Err("preset fallback failed: %v", err)
			return 1
		}
	default:
		output.Err("manifest invalid: %v", err)
		return 1
	}
	// A manifest friday could load but had to degrade (a retired strategy
	// dropped, an adapter key renamed since it was written) is exactly the
	// upgrade-path problem doctor exists to explain — silently loading it
	// would leave the user with rules that no longer run and no idea why.
	// Through warnStaleManifest, because it is the one path that puts these
	// on stderr: doctor's own probe bypasses loadUserOrDefault, and rendering
	// them with output.Warn instead both leaked them into a parsed stdout and
	// let --quiet silence the single diagnostic this command exists to give.
	warnStaleManifest(cfg)

	// 5. adapters
	output.Header("adapters")
	names := cfg.AdapterNames()
	rows := make([][]string, len(names))
	installedAt := make([]bool, len(names))
	for i, name := range names {
		abs, _ := cfg.AdapterTargetAbs(name)
		installedAt[i] = dirExists(abs)
		status := "missing"
		if installedAt[i] {
			status = "installed"
		}
		rows[i] = []string{name, status, abs}
	}
	for i, line := range alignTable(nil, rows) {
		if installedAt[i] {
			output.OK("%s", line)
		} else {
			output.Skip("%s", line)
		}
	}
	reportStaleAdapters(cfg)

	// 6. drift across installed adapters
	output.Header("drift")
	installed := installedAdapters(cfg)
	if len(installed) == 0 {
		output.Dim("(no installed adapters — nothing to check)")
	} else {
		changes, err := engine.Push(cfg, engine.Options{
			Adapters: installed,
			DryRun:   true,
		})
		if err != nil {
			output.Err("dry-run push: %v", err)
			bad++
		} else {
			conflicts := 0
			for _, ch := range changes {
				if ch.Action == engine.ActionConflict {
					conflicts++
					output.Warn("drift in %s: %s", ch.Adapter, ch.DestRel)
				}
			}
			if conflicts == 0 {
				output.OK("no drift detected")
			} else {
				output.Dim("hint: `friday pull <adapter>` to capture edits, or `friday push --force` to overwrite")
			}
			reportHooks(cfg, changes)
		}
	}

	// 7. drift store file
	driftPath, err := drift.DefaultPath()
	if err == nil {
		if _, err := os.Stat(driftPath); err == nil {
			output.OK("drift cache: %s", driftPath)
		} else {
			output.Dim("drift cache: %s (not created yet — first push writes it)", driftPath)
		}
	}

	// 8. store checks (the folded-in `lint` plus best-practice advice):
	// structural errors fail the check; warnings are advisory. These are
	// strictly friday's own render-graph checks (D26) — content linting is
	// delegated to agnix below.
	output.Header("store checks")
	if findings, err := lint.Run(cfg); err != nil {
		output.Err("lint: %v", err)
		bad++
	} else {
		printFindings(findings, &bad)
	}

	// 9. agnix delegation (D26). friday has 8 render-graph checks; agnix
	// ships hundreds of content-lint rules and updates independently, so
	// doctor folds its findings in — under an "agnix/" Rule prefix — rather
	// than reimplementing any of them. Absent is not a failure: it's an
	// opt-in tool, so the hint names it without alarm.
	output.Header("agnix")
	switch findings, status, detail := lint.RunAgnix(cfg); status {
	case lint.AgnixNotFound:
		output.Dim("not installed — deeper content linting is available via https://github.com/agent-sh/agnix")
	case lint.AgnixFailed:
		output.Warn("agnix is on PATH but its output could not be used: %s", detail)
	case lint.AgnixOK:
		printFindings(findings, &bad)
	}

	if bad > 0 {
		fmt.Println()
		output.Err("%d problem(s) detected", bad)
		return 1
	}
	return 0
}

// printFindings renders a slice of lint findings under an already-open
// section header: "no issues" when empty, otherwise one Err/Warn line per
// finding (bumping *bad for every Error) plus a trailing count of advisory
// warnings. Shared by the store-checks and agnix sections so both render
// identically regardless of which one produced the findings.
func printFindings(findings []lint.Finding, bad *int) {
	if len(findings) == 0 {
		output.OK("no issues")
		return
	}
	rows := make([][]string, len(findings))
	for i, f := range findings {
		rows[i] = []string{f.Rule, f.Path + " — " + f.Msg}
	}
	warns := 0
	for i, line := range alignTable(nil, rows) {
		if findings[i].Severity == lint.Error {
			output.Err("%s", line)
			*bad++
		} else {
			output.Warn("%s", line)
			warns++
		}
	}
	if warns > 0 {
		output.Dim("%d advisory warning(s) — best practices, not failures; silence a rule in %s", warns, lint.ConfigName)
	}
}

// findingJSON is the machine-readable shape of one store-check finding.
type findingJSON struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Message  string `json:"message"`
	Origin   string `json:"origin,omitempty"`
}

// agnixJSON reports whether the delegated linter ran at all. Without it a CI
// job cannot tell "agnix found nothing" from "agnix was never consulted" —
// and those are the same payload.
type agnixJSON struct {
	Status string `json:"status"` // "ok" | "not-found" | "failed"
	Detail string `json:"detail,omitempty"`
}

// adapterJSON is one row of the human health check's adapters table.
type adapterJSON struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Installed bool   `json:"installed"`
}

// driftJSON is one destination whose target has diverged from the store.
type driftJSON struct {
	Adapter string `json:"adapter"`
	Path    string `json:"path"`
}

// doctorJSON is the advisor's scriptable surface: the store-check findings
// plus the health-check state the human path prints, so a CI job can gate on
// the same facts. Agnix findings (D26) fold into the same Findings/Summary
// shape as friday's own — distinguished only by Origin — and the delegated
// linter's own status rides alongside rather than being inferred from an
// empty findings list.
//
// Exit code stays finding-driven: 1 when any error-severity finding is
// present, 0 for warnings alone. Drift is reported here, never fatal here —
// `push` and `status` own the exit-2 drift signal, and doctor answering the
// same question with a different code would make both meaningless.
func doctorJSON() int {
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	findings, err := lint.Run(cfg)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	agnix := agnixJSON{Status: "not-found"}
	switch agnixFindings, status, detail := lint.RunAgnix(cfg); status {
	case lint.AgnixOK:
		agnix.Status = "ok"
		findings = append(findings, agnixFindings...)
	case lint.AgnixFailed:
		agnix.Status = "failed"
		agnix.Detail = detail
	}

	adapters := []adapterJSON{}
	for _, name := range cfg.AdapterNames() {
		abs, _ := cfg.AdapterTargetAbs(name)
		adapters = append(adapters, adapterJSON{Name: name, Target: abs, Installed: dirExists(abs)})
	}
	drifted := []driftJSON{}
	if installed := installedAdapters(cfg); len(installed) > 0 {
		changes, derr := engine.Push(cfg, engine.Options{Adapters: installed, DryRun: true})
		if derr != nil {
			findings = append(findings, lint.Finding{Rule: "drift", Severity: lint.Error,
				Msg: fmt.Sprintf("drift check failed: %v", derr)})
		}
		for _, ch := range changes {
			if ch.Action == engine.ActionConflict {
				drifted = append(drifted, driftJSON{Adapter: ch.Adapter, Path: ch.DestRel})
			}
		}
	}

	out := struct {
		Findings []findingJSON  `json:"findings"`
		Summary  map[string]int `json:"summary"`
		Adapters []adapterJSON  `json:"adapters"`
		Drift    []driftJSON    `json:"drift"`
		Agnix    agnixJSON      `json:"agnix"`
		Warnings []string       `json:"warnings"`
	}{
		Findings: []findingJSON{},
		Summary:  map[string]int{"error": 0, "warn": 0},
		Adapters: adapters,
		Drift:    drifted,
		Agnix:    agnix,
		Warnings: append([]string{}, cfg.Warnings...),
	}
	for _, f := range findings {
		out.Findings = append(out.Findings, findingJSON{
			Rule:     f.Rule,
			Severity: f.Severity.String(),
			Path:     f.Path,
			Message:  f.Msg,
			Origin:   f.Origin,
		})
		out.Summary[f.Severity.String()]++
	}
	if err := printJSON(out); err != nil {
		return 1
	}
	if out.Summary["error"] > 0 {
		return 1
	}
	return 0
}

// explainFile answers "which adapter + rule produced this file?" by planning
// a dry-run push over every adapter — the same code path that writes, so there
// is one source of truth for who writes what. Multiple matches all print: two
// rules writing one destination is a misconfiguration this exists to expose.
// Exposed as `friday doctor <file>`.
func explainFile(arg string) int {
	abs, err := filepath.Abs(arg)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	changes, err := engine.Push(cfg, engine.Options{DryRun: true})
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	found := 0
	overwrites := 0
	for _, ch := range changes {
		if !samePath(ch.DestPath, abs) {
			continue
		}
		found++
		if !lint.ComposesTarget(cfg, ch) {
			overwrites++
		}
		r := cfg.Adapters[ch.Adapter].Rules[ch.RuleIndex]
		output.Header(ch.DestRel)
		output.Info("adapter:   %s", ch.Adapter)
		output.Info("rule:      from %v → %s  (strategy: %s)", []string(r.From), r.To, r.Strategy)
		if r.Strategy == rules.StrategyRender {
			// For a render rule the strategy name alone answers nothing: surface
			// says which canonical schema is projected and merge says whether the
			// target is overwritten or spliced. Both are required by the schema,
			// and together they are the behaviour.
			output.Info("           surface: %s, merge: %s", r.Surface, r.Merge)
		}
		if len(r.FrontmatterStrip) > 0 {
			output.Info("           frontmatter_strip: %v", r.FrontmatterStrip)
		}
		for k, v := range r.Replace {
			output.Info("           replace: %q → %q", k, v)
		}
		output.Info("sources:   %s", strings.Join(ch.Sources, ", "))
		output.Info("state:     %s", ch.Action)
		if ch.Reason != "" {
			output.Dim("           %s", ch.Reason)
		}
	}
	if found == 0 {
		output.Warn("no rule produces %s", abs)
		suggestNearest(changes, arg)
		return 1
	}
	switch {
	case overwrites > 1:
		output.Warn("%d rules overwrite this file — later rules win; consider removing the overlap", overwrites)
	case found > 1:
		// The same predicate dest-collision uses, so the two can never
		// disagree: co-owning merges each splice their own keys and compose.
		output.Info("co-owned by %d rules — each writes its own keys; nothing is overwritten", found)
	}
	return 0
}

// suggestNearest points at planned destinations whose relative path ends in
// the same basename, for the common "right file, wrong directory" miss. One
// line per destination: several rules co-owning it is not news the user asked
// for here, and repeating the identical hint four times reads like a bug.
func suggestNearest(changes []engine.Change, arg string) {
	base := filepath.Base(arg)
	seen := map[string]bool{}
	for _, ch := range changes {
		if filepath.Base(ch.DestRel) != base || seen[ch.DestPath] {
			continue
		}
		seen[ch.DestPath] = true
		output.Dim("did you mean %s (%s)?", ch.DestPath, ch.Adapter)
	}
}

// checkEntryFiles reports on the store's entry file. Concatenate rules match
// core.md, core/core.md, and legacy identity.md — a store carrying more than
// one would concatenate them all, which is almost never intended.
func checkEntryFiles(storeDir string) {
	var present []string
	for _, rel := range presets.EntryFiles {
		if _, err := os.Stat(filepath.Join(storeDir, filepath.FromSlash(rel))); err == nil {
			present = append(present, rel)
		}
	}
	switch {
	case len(present) == 0:
		output.Dim("entry file: none (core.md) — generated instructions will start with your rules")
	case len(present) > 1:
		output.Warn("entry file: %d variants present (%s) — concatenate rules will include all of them; keep one", len(present), strings.Join(present, ", "))
	case present[0] == "identity.md":
		output.Dim("entry file: identity.md (legacy name — rename to core.md; both work)")
	default:
		output.OK("entry file: %s", present[0])
	}
}

// reportStaleAdapters warns about a manifest naming an adapter friday has no
// preset for. A key that was merely RENAMED is already reported through
// cfg.Warnings (config.load owns that vocabulary, and `friday migrate` is the
// fix), so this covers only the names friday recognizes not at all: a typo,
// or a hand-written adapter. Neither is fatal — a hand-written adapter is a
// supported thing to have — but silence is wrong, because doctor otherwise
// lists such an adapter as installed and healthy while `friday push <name>`
// is the only place the mismatch ever surfaces.
func reportStaleAdapters(cfg *config.Config) {
	for _, name := range cfg.AdapterNames() {
		if _, ok := presets.Get(name); ok {
			continue
		}
		if _, renamed := config.LegacyAdapterNames[name]; renamed {
			continue
		}
		output.Warn("%s: no built-in preset by this name — its rules still run, but friday cannot check them against one (known: %s)",
			name, strings.Join(presets.Names(), ", "))
	}
}

// reportHooks surfaces the state of the store's hook WIRING — the writes that
// actually activate a hook, which is a narrower set than "every merge-json
// rule". Four adapters activate declaratively (the agent runs only the hooks
// its own config file declares, so friday merges the store's hooks into it);
// OpenCode and oh-my-pi have no declarative hook file at all, so friday drops
// a confirm-gated TS plugin whose mere presence IS the activation. Both shapes
// live under the store's hooks/ tree, which is what separates them from the
// two rules that share their strategy and have nothing to do with hooks:
// settings/<adapter>.json (the merge-json escape hatch) and native/<adapter>/**
// (a confirm-gated verbatim mirror). Reading the same dry-run plan the drift
// check uses keeps "wired and current" a statement about what a push would
// really do. Stores with no hooks produce missing-source changes and print
// nothing.
func reportHooks(cfg *config.Config, changes []engine.Change) {
	printed := false
	header := func() {
		if !printed {
			output.Header("hooks")
			printed = true
		}
	}
	for _, ch := range changes {
		ad, ok := cfg.Adapters[ch.Adapter]
		if !ok || ch.RuleIndex < 0 || ch.RuleIndex >= len(ad.Rules) {
			continue
		}
		r := ad.Rules[ch.RuleIndex]
		if !hookWiring(r, ch) {
			continue
		}
		plugin := r.Strategy != rules.StrategyMergeJSON
		switch {
		case ch.Action == engine.ActionInSync && plugin:
			header()
			output.OK("%s: guard plugin installed at %s and current", ch.Adapter, ch.DestRel)
		case ch.Action == engine.ActionInSync:
			header()
			output.OK("%s: wired into %s and current", ch.Adapter, ch.DestRel)
		case ch.Action == engine.ActionCreate && plugin:
			header()
			output.Warn("%s: guard plugin %s is not installed — `friday push %s` asks before installing it, since it runs the moment it lands", ch.Adapter, ch.DestRel, ch.Adapter)
		case ch.Action == engine.ActionUpdate && plugin:
			header()
			// The consent gate fires identically for Create and Update, so a
			// stale plugin hits the same prompt a fresh install does. The
			// generic message below never says so.
			output.Warn("%s: guard plugin %s is out of date — `friday push %s` asks before replacing it, since it runs the moment it lands", ch.Adapter, ch.DestRel, ch.Adapter)
		case ch.Action == engine.ActionCreate, ch.Action == engine.ActionUpdate:
			header()
			output.Warn("%s: %s is out of date — run `friday push %s` to wire the store's hooks", ch.Adapter, ch.DestRel, ch.Adapter)
		}
		// A hook command that references an expandable path unquoted breaks when
		// the path contains a space (e.g. a home dir like "C:/Users/First Last").
		// SrcContent is the plan's own bytes for the source it actually resolved,
		// so a hooks fragment living in a configured source is scanned from there
		// instead of guessed under the primary store — and one change per matched
		// source file means every from-pattern is covered, not just the first.
		for _, cmd := range engine.HookCommands(ch.SrcContent) {
			if unquotedExpansion(cmd) {
				header()
				output.Warn("%s: hook command is unquoted and will break on a path with spaces:", ch.Adapter)
				output.Dim("  %s", cmd)
				output.Dim("  quote it in %s, e.g. bash \"${CLAUDE_PLUGIN_ROOT}/hooks/scripts/...\"", ch.Sources[0])
				break
			}
		}
	}
}

// hookWiring reports whether ch's rule is the write that activates a hook.
// The store-side hooks/ prefix is the discriminator: strategy alone cannot
// tell friday's hook merge apart from the settings escape hatch, and
// ConfirmHook alone cannot tell the OpenCode/oh-my-pi guard plugin apart from
// a native/** mirror. A plain copy of the hooks tree (project scope drops an
// inert copy beside the settings merge) is not wiring and is not reported.
func hookWiring(r *rules.Rule, ch engine.Change) bool {
	if len(ch.Sources) == 0 || !strings.HasPrefix(ch.Sources[0], "hooks/") {
		return false
	}
	return r.Strategy == rules.StrategyMergeJSON || r.ConfirmHook
}

// unquotedExpansionNames lists the shell-expandable path variables a hook
// command might reference. Each one can resolve to a path containing a space
// (e.g. a Windows home directory like "C:/Users/First Last"), so an unquoted
// reference is a footgun: the shell word splits at the space and the command
// runs against a truncated path.
var unquotedExpansionNames = map[string]bool{
	"HOME":               true,
	"CLAUDE_PROJECT_DIR": true,
	"CLAUDE_PLUGIN_ROOT": true,
}

// unquotedExpansion reports whether cmd references one of
// unquotedExpansionNames, or a bare leading "~", without double-quoting it.
// It parses cmd as real shell syntax with mvdan.cc/sh/v3/syntax and walks
// every *syntax.Word looking for a *syntax.ParamExp (or a tilde-prefixed
// *syntax.Lit) that sits directly in that word's Parts — i.e. unquoted — as
// opposed to nested one level down inside a *syntax.DblQuoted, which is how
// a quoted reference like "$HOME/x" or "~/x" always parses.
//
// This replaces a bare strings.Contains(cmd, `"`) guard, which cleared the
// whole check the moment any quote appeared anywhere in the command — a
// genuinely unquoted $HOME beside an unrelated quoted string used to pass
// silently. A command mvdan can't parse is left unflagged: friday doctor is
// an advisor, not the shell that will actually run the hook, so a parse
// failure here should never manufacture a false positive.
func unquotedExpansion(cmd string) bool {
	file, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if found {
			return false
		}
		word, ok := node.(*syntax.Word)
		if !ok {
			return true
		}
		for i, part := range word.Parts {
			switch p := part.(type) {
			case *syntax.ParamExp:
				if unquotedExpansionNames[p.Param.Value] {
					found = true
				}
			case *syntax.Lit:
				// A leading "~" only triggers real tilde expansion when it
				// is the word's own first character — "x~/y" is literal.
				if i == 0 && strings.HasPrefix(p.Value, "~") {
					found = true
				}
			}
		}
		return true
	})
	return found
}
