// Package lint statically checks a friday store: structural problems
// (malformed frontmatter, destination collisions) and best-practice advisories
// (oversized or overlong instruction files, broken references, weak skill
// descriptions). A quality gate for `friday doctor` — a one-shot linter for how
// you author your agent config, never a resident process. Every rule is a
// static check over files on disk; runtime operating judgment (context budget,
// session discipline) is deliberately out of scope.
package lint

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
	"go.yaml.in/yaml/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/frontmatter"
	"github.com/zhivko-kocev/friday/internal/rules"
	"github.com/zhivko-kocev/friday/internal/secret"
	"github.com/zhivko-kocev/friday/internal/source"
)

const (
	// maxFileSize is the hard warn threshold — any file past this bloats a
	// context window regardless of type.
	maxFileSize = 64 * 1024
	// maxInstructionLines is the best-practice soft limit for an entry or rule
	// file: past this, an instruction file is usually trying to do too much and
	// should be split.
	maxInstructionLines = 200
	// minSkillDescription is the shortest a skill description can be and still
	// work as a trigger the agent can match on.
	minSkillDescription = 20
)

// Severity ranks a finding. Only Error fails `friday doctor`; Warn is
// advisory — the "best-practice advisor" tone.
type Severity int

const (
	Warn  Severity = iota // advisory: a best practice you're diverging from
	Error                 // a structural problem that will misbehave
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warn"
}

// Finding is one lint hit. Rule is a stable, grep-able slug that doubles as
// the id you disable in .friday-doctor.yaml. Origin is empty for every check
// friday runs itself; a delegated linter (agnix — see RunAgnix) stamps its
// own name there so callers can tell the two apart without parsing Rule.
type Finding struct {
	Path     string // store-relative (or "adapter:dest" for collisions)
	Rule     string
	Severity Severity
	Msg      string
	Origin   string // "" for friday's own checks, "agnix" for delegated ones
}

// mdLink captures the path of a relative markdown link — no scheme, no
// pure-anchor links.
var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)#:\s]+)\)`)

// Run lints the store and the adapter rule set, then drops any findings whose
// rule is disabled in <store>/.friday-doctor.yaml. The error return is for
// I/O-level failures only; lint problems come back as findings.
func Run(cfg *config.Config) ([]Finding, error) {
	set, err := source.Load(cfg)
	if err != nil {
		return nil, err
	}
	roots := set.Roots()
	// relIndex accumulates, per store-relative path, every root that has it
	// (in root order — lowest precedence first) — checkSourceShadow's input.
	relIndex := map[string][]string{}
	var findings []Finding
	for _, root := range roots {
		rootFindings, err := lintRoot(root, relIndex)
		if err != nil {
			return nil, err
		}
		findings = append(findings, rootFindings...)
	}

	findings = append(findings, checkSourceManifest(roots)...)

	// Both the shadow report and the collision report are claims about what a
	// push would actually do, so both read one real dry-run plan rather than
	// re-deriving glob precedence or merge semantics from the filesystem — the
	// duplication that made each of them wrong. A plan that cannot even be
	// built is itself the only actionable finding; guessing on top of it would
	// only add noise.
	changes, planErr := engine.Push(cfg, engine.Options{DryRun: true})
	if planErr != nil {
		findings = append(findings, Finding{Rule: "plan", Severity: Error, Msg: planErr.Error()})
	} else {
		findings = append(findings, checkSourceShadow(roots, relIndex, changes)...)
		findings = append(findings, checkDestCollisions(cfg, changes)...)
	}
	return applyIgnores(cfg.StoreDir, findings), nil
}

// lintRoot walks one source root's files. A finding's Path stays the bare
// store-relative rel for the primary — unchanged from before Phase 2 — and
// gains a "<source>:" prefix for a configured source, the same disambiguating
// convention checkDestCollisions already uses for "adapter:dest". A source
// declared in friday.yaml but not yet cloned (root.Dir doesn't exist)
// contributes no findings rather than failing the whole run. relIndex
// records every file's presence for checkSourceShadow's cross-root pass.
func lintRoot(root rules.Root, relIndex map[string][]string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root.Dir {
				return filepath.SkipDir // .git, .sources, and friends
			}
			return nil
		}
		rel, rerr := filepath.Rel(root.Dir, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		relIndex[rel] = append(relIndex[rel], root.Name)
		label := rel
		if root.Name != "local" {
			label = root.Name + ":" + rel
		}

		info, ierr := d.Info()
		if ierr == nil && info.Size() > maxFileSize {
			findings = append(findings, Finding{Path: label, Rule: "oversized", Severity: Warn,
				Msg: fmt.Sprintf("%d KiB — agent instruction files this large bloat context windows", info.Size()/1024)})
		}
		if strings.HasSuffix(rel, ".md") {
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if _, _, ferr := frontmatter.ParseStrict(string(data)); ferr != nil {
				findings = append(findings, Finding{Path: label, Rule: "frontmatter", Severity: Error, Msg: ferr.Error()})
			}
			findings = append(findings, checkInstructionLength(rel, label, data)...)
			findings = append(findings, checkSkillDescription(rel, label, data)...)
			findings = append(findings, checkRefs(root.Dir, rel, label, data)...)
			return nil
		}
		if secretScanExts[strings.ToLower(filepath.Ext(rel))] {
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			findings = append(findings, checkSecrets(rel, label, data)...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// checkSourceShadow flags a lower-precedence root's copy of a file that a
// push genuinely never reads. "Genuinely" is the whole point: the naive
// version of this check — every path present in more than one root — was
// wrong twice over. A concatenate rule over a literal entry-file name (D11's
// exception) includes EVERY root's copy, so an upstream core.md contributes
// alongside the local one; and a file no rule references at all (README.md,
// LICENSE) contributes nowhere, in any root, so calling one copy shadowed is
// noise. Both live in the plan, not in the filesystem, so contribution is
// read off the real dry-run changes instead of re-derived here.
//
// A single-root store (no configured sources — today's common case) can
// never collide with itself, so this is a no-op there.
func checkSourceShadow(roots []rules.Root, relIndex map[string][]string, changes []engine.Change) []Finding {
	if len(roots) < 2 {
		return nil
	}
	dirOf := make(map[string]string, len(roots))
	for _, root := range roots {
		dirOf[root.Name] = root.Dir
	}
	contributing := contributingSources(changes)
	var out []Finding
	for rel, sources := range relIndex {
		if len(sources) < 2 {
			continue
		}
		winner := sources[len(sources)-1]
		if !contributing[filepath.Join(dirOf[winner], filepath.FromSlash(rel))] {
			continue // no rule reads this path from any root — nothing is shadowed
		}
		for _, loser := range sources[:len(sources)-1] {
			if contributing[filepath.Join(dirOf[loser], filepath.FromSlash(rel))] {
				continue // D11: this copy is concatenated in too
			}
			out = append(out, Finding{
				Path:     loser + ":" + rel,
				Rule:     "source-shadow",
				Severity: Warn,
				Msg:      fmt.Sprintf("shadowed by %s's copy of %s — this file never contributes to any push", winner, rel),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// contributingSources is the set of absolute source paths a push actually
// reads, keyed for lookup. SrcAbs names the single resolved winner for every
// strategy that has one (copy, merge-json, merge-toml, render); it is empty
// exactly for concatenate, whose SourceAbs holds every part the rule
// concatenates — which is where D11's entry-file exception
// (rules.ExpandLiteralAcrossRoots) shows up as "every root's copy
// contributes". Copies a single-winner rule resolved PAST are deliberately
// not consulted here: engine.Change.Shadowed is provenance, not contribution,
// and a shadowed copy is precisely what this check reports. SourceAbs is
// positional with Sources and carries "" where a pattern matched nothing, so
// blanks are skipped rather than recorded as a contributing path.
func contributingSources(changes []engine.Change) map[string]bool {
	out := map[string]bool{}
	for _, ch := range changes {
		switch ch.Action {
		case engine.ActionMissingSource, engine.ActionUnsupported:
			continue
		}
		if ch.SrcAbs != "" {
			out[ch.SrcAbs] = true
			continue
		}
		for _, abs := range ch.SourceAbs {
			if abs != "" {
				out[abs] = true
			}
		}
	}
	return out
}

// checkSourceManifest warns when a non-primary source's own directory
// carries a friday.yaml. source.Load never reads it — only the primary
// store's manifest is authoritative (D13) — so a source repo that happens to
// itself be a friday store silently has its own adapters and sources ignored.
func checkSourceManifest(roots []rules.Root) []Finding {
	var out []Finding
	for _, root := range roots {
		if root.Name == "local" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root.Dir, config.ManifestName)); err == nil {
			out = append(out, Finding{
				Path:     root.Name + ":" + config.ManifestName,
				Rule:     "source-manifest",
				Severity: Warn,
				Msg:      "this source has its own friday.yaml, which is ignored — only the primary store's manifest is read",
			})
		}
	}
	return out
}

// isInstructionFile reports whether rel is an entry file or a behaviour rule —
// the files kept short for a healthy context budget.
func isInstructionFile(rel string) bool {
	switch rel {
	case "core.md", "core/core.md", "identity.md":
		return true
	}
	return strings.HasPrefix(rel, "rules/")
}

// checkInstructionLength warns when an entry or rule file runs long — the
// single most-cited CLAUDE.md best practice is to keep these focused.
func checkInstructionLength(rel, label string, content []byte) []Finding {
	if !isInstructionFile(rel) {
		return nil
	}
	lines := strings.Count(string(content), "\n") + 1
	if lines <= maxInstructionLines {
		return nil
	}
	return []Finding{{Path: label, Rule: "long-instructions", Severity: Warn,
		Msg: fmt.Sprintf("%d lines (> %d) — split focused topics into separate rules/*.md so agents load only what they need", lines, maxInstructionLines)}}
}

// checkSkillDescription warns when a skill lacks a usable description. A
// skill's description is the trigger the agent matches on; a missing or terse
// one means the skill silently never fires.
func checkSkillDescription(rel, label string, content []byte) []Finding {
	if !(strings.HasPrefix(rel, "skills/") && strings.HasSuffix(rel, "/SKILL.md")) {
		return nil
	}
	fields, _ := frontmatter.Parse(string(content))
	desc, _ := fields["description"].(string)
	desc = strings.TrimSpace(desc)
	switch {
	case desc == "":
		return []Finding{{Path: label, Rule: "skill-description", Severity: Warn,
			Msg: "no description in frontmatter — the description is the trigger the agent matches on; without it the skill never fires"}}
	case len(desc) < minSkillDescription:
		return []Finding{{Path: label, Rule: "skill-description", Severity: Warn,
			Msg: fmt.Sprintf("description is only %d chars — make it a specific sentence describing when to use the skill", len(desc))}}
	}
	return nil
}

// checkRefs flags relative markdown links that resolve to nothing, checked
// against both the linking file's directory and the store root.
func checkRefs(storeDir, rel, label string, content []byte) []Finding {
	var out []Finding
	for _, m := range mdLink.FindAllSubmatch(content, -1) {
		ref := string(m[1])
		if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") || strings.Contains(ref, "${") {
			continue // absolute / home / templated — not checkable
		}
		fromFile := filepath.Join(storeDir, filepath.Dir(filepath.FromSlash(rel)), filepath.FromSlash(ref))
		fromRoot := filepath.Join(storeDir, filepath.FromSlash(ref))
		if fileExists(fromFile) || fileExists(fromRoot) {
			continue
		}
		out = append(out, Finding{Path: label, Rule: "broken-ref", Severity: Warn, Msg: fmt.Sprintf("link target %q not found", ref)})
	}
	return out
}

// secretScanExts lists the config file formats a store can hold a pasted
// credential in. internal/secret's predicate is key-name and value-shape
// driven, never schema driven, so it applies to all of them equally: gating
// the scan on the single basename "mcp.yaml" left both documented escape
// hatches (native/<adapter>/**, settings/<adapter>.json) and defaults.yaml's
// free-form statusline command and vendor overrides unscanned.
var secretScanExts = map[string]bool{
	".yaml": true,
	".yml":  true,
	".json": true,
	".toml": true,
}

// checkSecrets flags a literal credential in a store config file: such a
// value is meant to reference an environment variable (${VAR}), never hold
// the real thing, since the store is typically a git repo. A file that does
// not parse is skipped silently — push already reports a malformed canonical
// schema under its own rule, and a hand-written passthrough file friday never
// interprets is none of this check's business.
func checkSecrets(rel, label string, data []byte) []Finding {
	tree, ok := decodeConfig(rel, data)
	if !ok {
		return nil
	}
	var out []Finding
	// The empty prefix keeps the reported key path relative to the file, which
	// Finding.Path already names.
	for _, f := range secret.ScanAny("", tree) {
		out = append(out, Finding{Path: label, Rule: "mcp-secret", Severity: Error, Msg: f.Message()})
	}
	return out
}

// decodeConfig parses one store config file into the plain map/slice/scalar
// tree secret.ScanAny walks. JSON goes through hujson so a JSONC settings
// file — Copilot writes one — is scanned rather than written off as malformed.
func decodeConfig(rel string, data []byte) (any, bool) {
	var tree any
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &tree); err != nil {
			return nil, false
		}
	case ".json":
		// Standardize blanks comments in place, so it never gets the caller's
		// own buffer to mutate.
		std, err := hujson.Standardize(append([]byte(nil), data...))
		if err != nil {
			return nil, false
		}
		if err := json.Unmarshal(std, &tree); err != nil {
			return nil, false
		}
	case ".toml":
		table := map[string]any{}
		if err := toml.Unmarshal(data, &table); err != nil {
			return nil, false
		}
		tree = table
	}
	return tree, tree != nil
}

// ComposesTarget reports whether ch's rule splices its own keys into a
// co-owned target instead of overwriting the whole file. merge-json and
// merge-toml compose by construction, and a render rule composes when its
// Merge does — the standard layout has a render surface and a native
// passthrough rule deliberately co-owning one settings.json/config.toml, so
// two such writes to one destination are not a collision at all.
//
// Exported because `friday doctor <file>`'s "N rules write this file"
// warning is the same claim as dest-collision and must not disagree with it.
func ComposesTarget(cfg *config.Config, ch engine.Change) bool {
	ad, ok := cfg.Adapters[ch.Adapter]
	if !ok || ch.RuleIndex < 0 || ch.RuleIndex >= len(ad.Rules) {
		return false
	}
	r := ad.Rules[ch.RuleIndex]
	lands := r.Strategy
	if lands == rules.StrategyRender {
		lands = r.Merge
	}
	return lands == rules.StrategyMergeJSON || lands == rules.StrategyMergeTOML
}

// checkDestCollisions flags a destination written by more than one change
// where at least one of those writes replaces the whole file: that write
// silently discards whatever the others put there, which is never intended.
// Destinations whose writers ALL compose (ComposesTarget) are the documented
// co-ownership layout — each rule owns its own keys and the rest of the file
// passes through byte for byte — so they are not reported.
func checkDestCollisions(cfg *config.Config, changes []engine.Change) []Finding {
	type writer struct {
		sources  string
		destRel  string
		composes bool
	}
	var keys []string // destination order, for a deterministic report
	seen := map[string][]writer{}
	for _, ch := range changes {
		switch ch.Action {
		case engine.ActionMissingSource, engine.ActionUnsupported:
			continue
		}
		key := ch.Adapter + ":" + ch.DestPath
		if len(seen[key]) == 0 {
			keys = append(keys, key)
		}
		seen[key] = append(seen[key], writer{
			sources:  strings.Join(ch.Sources, "+"),
			destRel:  ch.Adapter + ":" + ch.DestRel,
			composes: ComposesTarget(cfg, ch),
		})
	}
	var out []Finding
	for _, key := range keys {
		writers := seen[key]
		if len(writers) < 2 {
			continue
		}
		overwrites := false
		names := make([]string, 0, len(writers))
		for _, w := range writers {
			overwrites = overwrites || !w.composes
			names = append(names, w.sources)
		}
		if !overwrites {
			continue
		}
		out = append(out, Finding{Path: writers[0].destRel, Rule: "dest-collision", Severity: Error,
			Msg: fmt.Sprintf("written by multiple rules (%s) — the last one wins", strings.Join(names, " and "))})
	}
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
