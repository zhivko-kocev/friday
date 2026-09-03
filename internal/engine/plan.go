package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/frontmatter"
	"github.com/zhivko-kocev/friday/internal/render"
	"github.com/zhivko-kocev/friday/internal/rules"
	"github.com/zhivko-kocev/friday/internal/spec"
	"github.com/zhivko-kocev/friday/internal/textdiff"
)

// planPushRule plans one rule of one adapter — the unit runPlanned drives,
// because a merge rule's baseline depends on what the rules before it
// actually wrote. applied holds the content THIS run has already committed to
// each destination path (see readAppliedOrDisk); a destination missing from
// it is read from disk.
func planPushRule(owned *drift.Owned, adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string, applied map[string][]byte) ([]Change, error) {
	switch r.Strategy {
	case rules.StrategyConcatenate:
		ch, err := planConcatenate(adapterName, r, roots, targetAbs)
		return []Change{ch}, err
	case rules.StrategyCopy:
		return planCopy(adapterName, r, roots, targetAbs)
	case rules.StrategyMergeJSON:
		ch, err := planMergeJSON(owned, adapterName, r, roots, targetAbs, applied)
		return []Change{ch}, err
	case rules.StrategyMergeTOML:
		ch, err := planMergeTOML(owned, adapterName, r, roots, targetAbs, applied)
		return []Change{ch}, err
	case rules.StrategyRender:
		return planRender(owned, adapterName, r, roots, targetAbs, applied)
	default:
		return nil, fmt.Errorf("adapter %s: unknown strategy %q", adapterName, r.Strategy)
	}
}

// planPush plans every rule of one adapter in one pass, each rule seeing what
// the rules before it WOULD write. That is the right answer for a preview and
// the wrong one for a real push — a rule whose write is later declined,
// conflict-protected or filtered out must not feed the next rule's merge — so
// the push path drives planPushRule directly (runPlanned) and only pull-side
// planners keep the whole-adapter shape.
func planPush(owned *drift.Owned, adapterName string, ad *config.Adapter, roots []rules.Root, targetAbs string) ([]Change, error) {
	var out []Change
	planned := map[string][]byte{}
	for i, r := range ad.Rules {
		chs, err := planPushRule(owned, adapterName, r, roots, targetAbs, planned)
		if err != nil {
			return nil, err
		}
		for j := range chs {
			chs[j].RuleIndex = i
			if (chs[j].Action == ActionCreate || chs[j].Action == ActionUpdate) && chs[j].NewContent != nil {
				planned[chs[j].DestPath] = chs[j].NewContent
			}
		}
		out = append(out, chs...)
	}
	return out, nil
}

// expandForConcatenate resolves one From pattern across every source root for
// a concatenate rule. A literal pattern takes every root's own copy, in
// precedence order (lowest first), so an upstream identity and the local one
// both contribute (D11's entry-file exception, generalised: any literal in a
// concatenate from-list is a distinct contribution, not a shadowed
// duplicate). A glob pattern instead unions distinct files and keeps only the
// highest-precedence copy of a colliding relative path — concatenating the
// same file twice would duplicate its content.
func expandForConcatenate(roots []rules.Root, pattern string) ([]rules.Match, error) {
	if rules.HasWildcard(pattern) {
		matched, _, err := rules.ExpandAll(roots, pattern)
		return matched, err
	}
	return rules.ExpandLiteralAcrossRoots(roots, pattern)
}

func planConcatenate(adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string) (Change, error) {
	var (
		parts     [][]byte
		sources   []string
		sourceAbs []string
	)
	for _, pat := range r.From {
		matches, err := expandForConcatenate(roots, pat)
		if err != nil {
			return Change{}, fmt.Errorf("expand %q: %w", pat, err)
		}
		for _, m := range matches {
			abs := filepath.Join(m.Root, filepath.FromSlash(m.Rel))
			data, err := os.ReadFile(abs)
			if err != nil {
				return Change{}, fmt.Errorf("read %s: %w", m.Rel, err)
			}
			data = []byte(frontmatter.Strip(string(data), r.FrontmatterStrip))
			data = r.ApplyReplace(data)
			parts = append(parts, data)
			sources = append(sources, m.Rel)
			sourceAbs = append(sourceAbs, abs)
		}
	}
	dest := filepath.Join(targetAbs, r.To)
	ch := Change{
		Adapter:   adapterName,
		Direction: DirPush,
		Sources:   sources,
		SourceAbs: sourceAbs,
		DestPath:  dest,
		DestRel:   r.To,
	}
	if len(parts) == 0 {
		ch.Action = ActionMissingSource
		ch.Reason = fmt.Sprintf("no source files matched %v", []string(r.From))
		return ch, nil
	}
	ch.NewContent = bytes.Join(parts, []byte(r.Sep()))
	if r.MaxBytes > 0 && len(ch.NewContent) > r.MaxBytes {
		// Warning, not Reason: conflict resolution rewrites Reason, and the
		// oversize advisory must survive it.
		ch.Warning = fmt.Sprintf("%d bytes exceeds the agent's %d-byte limit — it may truncate or ignore this file; trim the from-list", len(ch.NewContent), r.MaxBytes)
	}
	old, err := os.ReadFile(dest)
	switch {
	case os.IsNotExist(err):
		ch.Action = ActionCreate
	case err != nil:
		return ch, fmt.Errorf("read %s: %w", dest, err)
	default:
		ch.OldContent = old
		if equalNormalized(old, ch.NewContent) {
			ch.Action = ActionInSync
		} else {
			ch.Action = ActionUpdate
		}
	}
	return ch, nil
}

func planCopy(adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string) ([]Change, error) {
	return planTokenized(adapterName, r, roots, targetAbs, func(raw []byte, _ string) ([]byte, error) {
		return r.ApplyReplace([]byte(frontmatter.Strip(string(raw), r.FrontmatterStrip))), nil
	})
}

// stemOf returns a path's base filename without its extension.
func stemOf(rel string) string {
	base := rel
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return base
}

// planTokenized is the shared push skeleton for the per-file strategies (copy,
// and render's codex-agent/gemini-command surfaces): it expands the
// from-patterns, renders each match with `render`, and diffs the result
// against the destination. render maps a source file's bytes (and its
// store-relative path) to the bytes to write.
func planTokenized(adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string, render func(raw []byte, srcRel string) ([]byte, error)) ([]Change, error) {
	var out []Change
	// Tokenized templates make the from-patterns independent content globs —
	// each one failing to match is worth reporting. A literal template's
	// from-list carries alternative spellings (core.md vs core/core.md) where
	// only one is expected to exist, so those report per rule instead.
	perPattern := rules.HasToken(r.To)
	matchedAny := false
	// A literal template (or colliding filenames) can map several sources to
	// one destination; the from-list is ordered most-preferred first, so the
	// first source wins.
	seenDest := map[string]bool{}
	for _, pat := range r.From {
		matches, _, err := rules.ExpandAll(roots, pat)
		if err != nil {
			return nil, fmt.Errorf("expand %q: %w", pat, err)
		}
		if len(matches) == 0 {
			if perPattern {
				out = append(out, Change{
					Adapter:   adapterName,
					Direction: DirPush,
					Sources:   []string{pat},
					SourceAbs: []string{""}, // no file resolved this pattern
					Action:    ActionMissingSource,
					Reason:    fmt.Sprintf("no source files matched %q", pat),
				})
			}
			continue
		}
		matchedAny = true
		anchor := rules.Anchor(pat)
		for _, m := range matches {
			srcAbs := filepath.Join(m.Root, filepath.FromSlash(m.Rel))
			info, err := os.Stat(srcAbs)
			if err != nil {
				return nil, fmt.Errorf("stat %s: %w", m.Rel, err)
			}
			raw, err := os.ReadFile(srcAbs)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", m.Rel, err)
			}
			content, err := render(raw, m.Rel)
			if err != nil {
				return nil, fmt.Errorf("render %s: %w", m.Rel, err)
			}
			tokens := rules.TokensFor(m.Rel, anchor)
			destRel := tokens.Expand(r.To)
			destAbs := filepath.Join(targetAbs, destRel)
			if seenDest[destAbs] {
				continue
			}
			seenDest[destAbs] = true

			ch := Change{
				Adapter:    adapterName,
				Direction:  DirPush,
				Sources:    []string{m.Rel},
				SourceAbs:  []string{srcAbs},
				Root:       rootName(roots, m.Source),
				SrcAbs:     srcAbs,
				SrcContent: raw,
				DestPath:   destAbs,
				DestRel:    destRel,
				NewContent: content,
				Mode:       info.Mode().Perm(),
			}
			// A confirm_hook source can be executed by the agent the moment
			// it lands (an auto-loaded plugin, or a native mirror dropping a
			// settings file with hook commands), so gate the write behind the
			// confirmer and hand it the rendered body to describe.
			if r.ConfirmHook {
				ch.hookWrite = true
				ch.mergeSource = content
			}
			old, err := os.ReadFile(destAbs)
			switch {
			case os.IsNotExist(err):
				ch.Action = ActionCreate
			case err != nil:
				return nil, fmt.Errorf("read %s: %w", destAbs, err)
			default:
				ch.OldContent = old
				if equalNormalized(old, content) {
					ch.Action = ActionInSync
				} else {
					ch.Action = ActionUpdate
				}
			}
			out = append(out, ch)
		}
	}
	if !matchedAny && !perPattern {
		out = append(out, Change{
			Adapter:   adapterName,
			Direction: DirPush,
			Sources:   []string(r.From),
			SourceAbs: make([]string, len(r.From)), // no file resolved any of them
			Action:    ActionMissingSource,
			Reason:    fmt.Sprintf("no source files matched %v", []string(r.From)),
		})
	}
	return out, nil
}

// planMergeJSON wires the single source JSON file's top-level keys into a
// co-owned target JSON file (e.g. hooks.json → settings.json). Only friday's
// keys are overwritten; the target's other keys pass through untouched, which
// is why the change is drift-exempt. The InSync test canonicalizes the existing
// target so the agent's own formatting (key order, indent) never reads as drift.
func planMergeJSON(owned *drift.Owned, adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string, applied map[string][]byte) (Change, error) {
	return planMerge(owned, adapterName, r, roots, targetAbs, applied, "JSON", mergeEntries)
}

// planMergeTOML is merge-json's TOML analog: it splices the single source TOML
// file's owned tables into a co-owned target TOML file (e.g. settings/codex.toml
// into ~/.codex/config.toml), resolving the owned-table set from the source
// itself rather than from a fixed key list.
func planMergeTOML(owned *drift.Owned, adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string, applied map[string][]byte) (Change, error) {
	return planMerge(owned, adapterName, r, roots, targetAbs, applied, "TOML", func(old, source, prev []byte) ([]byte, bool, error) {
		tables, err := Tables(source)
		if err != nil {
			return nil, false, err
		}
		return Splice(old, source, prev, tables)
	})
}

// planMerge is the shared push skeleton for the co-owned merge strategies:
// resolve one source file across the roots, merge it into whatever the target
// currently holds, and leave everything friday does not own alone. format
// names the syntax in the "fix the X, then push" advisory; merge does the
// format-specific work. A non-empty but unparseable target is reported and
// skipped — friday writes nothing rather than clobber a file whose contents
// it cannot understand.
func planMerge(owned *drift.Owned, adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string, applied map[string][]byte, format string, merge func(old, source, prev []byte) ([]byte, bool, error)) (Change, error) {
	src := r.From[0]
	dest := filepath.Join(targetAbs, filepath.FromSlash(r.To))
	// A merge rule wants exactly one winner across sources (unlike
	// concatenate's every-source-contributes semantics) — the highest-
	// precedence root that has it, or the primary as a display fallback when
	// none do (the stat below then reports it missing, exactly as the
	// single-source path did).
	root, shadowed, ok := rules.ResolveSingle(roots, src)
	if !ok {
		root = primaryRoot(roots)
	}
	srcAbs := filepath.Join(root.Dir, filepath.FromSlash(src))
	// prev is friday's last-written source for this target (nil on first push or
	// a cleared cache). It lets the merge drop friday's own stale entries after a
	// store edit; without it the merge still preserves user hooks and stays
	// idempotent, it just can't remove a since-changed entry.
	var prev []byte
	if owned != nil {
		prev = owned.Get(adapterName, src, dest)
	}
	ch := Change{
		Adapter:     adapterName,
		Direction:   DirPush,
		Sources:     []string{src},
		SourceAbs:   []string{srcAbs},
		Root:        rootName(roots, root.Name),
		DestPath:    dest,
		DestRel:     r.To,
		driftExempt: true,
		hookWrite:   installsHookWiring(src, nil),
	}
	// A lower-precedence root's own copy of the source is dropped, not merged.
	// Nothing else reports it for a literal single-source rule, so without this
	// a team source's hook wiring could stop being applied the day the user
	// adds a local copy, in silence.
	if len(shadowed) > 0 {
		names := make([]string, len(shadowed))
		for i, s := range shadowed {
			names[i] = s.Name
			ch.Shadowed = append(ch.Shadowed, filepath.Join(s.Dir, filepath.FromSlash(src)))
		}
		ch.Warning = fmt.Sprintf("%s also defined in %s (shadowed by %s)", src, strings.Join(names, ", "), root.Name)
	}

	info, err := os.Stat(srcAbs)
	if err != nil {
		if os.IsNotExist(err) {
			ch.Action = ActionMissingSource
			ch.Reason = fmt.Sprintf("no source file at %q", src)
			return ch, nil
		}
		return ch, fmt.Errorf("stat %s: %w", src, err)
	}
	raw, err := os.ReadFile(srcAbs)
	if err != nil {
		return ch, fmt.Errorf("read %s: %w", src, err)
	}
	ch.SrcAbs = srcAbs
	ch.SrcContent = raw
	source := r.ApplyReplace(raw)
	ch.mergeSource = source
	ch.hookWrite = installsHookWiring(src, source)

	old, exists, destMode, err := readAppliedOrDisk(applied, dest)
	if err != nil {
		return ch, fmt.Errorf("read %s: %w", dest, err)
	}
	if exists {
		ch.OldContent = old
		// Preserve the target's mode — settings.json may be 0600.
		ch.Mode = destMode
	}
	merged, changed, err := merge(old, source, prev)
	if err != nil {
		// Unparseable input — either the store's or a hand-edited target's —
		// is the user's to fix; skip this one write rather than abort the
		// whole run, and never clobber a file friday can't understand.
		ch.Action = ActionUnsupported
		if exists {
			ch.Reason = fmt.Sprintf("%s: %v — fix the %s, then push", r.To, err, format)
		} else {
			ch.Reason = fmt.Sprintf("cannot merge %s: %v", src, err)
		}
		return ch, nil
	}
	ch.NewContent = merged
	switch {
	case !exists:
		ch.Action = ActionCreate
		ch.Mode = info.Mode().Perm()
	case changed:
		ch.Action = ActionUpdate
	default:
		ch.Action = ActionInSync
	}
	return ch, nil
}

// installsHookWiring reports whether a write installs executable hook wiring —
// a command an agent runs unattended on a tool call. Consent is gated on this
// rather than on "is this a co-owned merge": mcp.yaml's server list,
// permissions.yaml's policy and defaults.yaml's model settings are declarative
// config that must still land in a --no-interactive push, while a hooks blob
// must not, whichever strategy carries it.
//
// Two independent signals, because either alone leaks. The store layout
// AGENTS.md defines puts every hook source under hooks/, so the rule's own
// source path catches a hooks rule even before its payload is read. But the
// payload matters too: several rules legitimately merge into one target, and a
// passthrough like settings/claude.json can carry a "hooks" key straight into
// the same settings.json the gated hooks/ rule owns. Gating on the source path
// alone let that through unprompted, even under --no-interactive.
func installsHookWiring(srcRel string, payload []byte) bool {
	if strings.HasPrefix(filepath.ToSlash(srcRel), "hooks/") {
		return true
	}
	return declaresHookWiring(payload)
}

// readAppliedOrDisk resolves dest's baseline the way the next rule writing it
// would see it: content this run has ALREADY written to dest (recorded in
// applied — see runPlanned) takes precedence over the on-disk file, since the
// write has happened but the planner would otherwise re-read a stale snapshot.
// exists is false only when neither has dest at all — the "create" case for
// whichever rule gets there first. mode is the destination's current mode, or
// 0 when it does not exist, so plan and apply agree on it either way.
func readAppliedOrDisk(applied map[string][]byte, dest string) (content []byte, exists bool, mode fs.FileMode, err error) {
	info, serr := os.Stat(dest)
	if serr == nil {
		mode = info.Mode().Perm()
	}
	if c, ok := applied[dest]; ok {
		return c, true, mode, nil
	}
	data, rerr := os.ReadFile(dest)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, false, 0, nil
		}
		return nil, false, 0, rerr
	}
	return data, true, mode, nil
}

// planRender dispatches a render rule by surface. mcp, permissions and
// defaults each resolve one canonical schema file across roots — the same
// single-winner way merge-json resolves its source (see Rule.Normalize) —
// project it through internal/render, then land the payload per r.Merge in
// planRenderCanonical. codex-agent and gemini-command instead transform every
// matched markdown source into its own target file through the shared
// planTokenized skeleton, one target file per matched markdown source.
func planRender(owned *drift.Owned, adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string, applied map[string][]byte) ([]Change, error) {
	switch r.Surface {
	case rules.SurfaceMCP:
		ch, err := planRenderCanonical(owned, adapterName, r, roots, targetAbs, applied, func(raw []byte) ([]byte, []string, error) {
			m, err := spec.ParseMCP(raw)
			if err != nil {
				return nil, nil, schemaParseError(err)
			}
			payload, unmapped, err := render.MCP(adapterName, *m)
			return payload, append(render.MCPSecretWarnings(*m), formatUnmapped(adapterName, unmapped)...), err
		})
		return []Change{ch}, err
	case rules.SurfacePermissions:
		ch, err := planRenderCanonical(owned, adapterName, r, roots, targetAbs, applied, func(raw []byte) ([]byte, []string, error) {
			p, err := spec.ParsePermissions(raw)
			if err != nil {
				return nil, nil, schemaParseError(err)
			}
			payload, unmapped, err := render.Permissions(adapterName, *p)
			return payload, formatUnmapped(adapterName, unmapped), err
		})
		return []Change{ch}, err
	case rules.SurfaceDefaults:
		ch, err := planRenderCanonical(owned, adapterName, r, roots, targetAbs, applied, func(raw []byte) ([]byte, []string, error) {
			d, err := spec.ParseDefaults(raw)
			if err != nil {
				return nil, nil, schemaParseError(err)
			}
			return render.Defaults(adapterName, r.To, *d)
		})
		return []Change{ch}, err
	case rules.SurfaceCodexAgent:
		return planTokenized(adapterName, r, roots, targetAbs, func(raw []byte, srcRel string) ([]byte, error) {
			return render.CodexAgent(parseAgentMD(raw, stemOf(srcRel), r.ApplyReplace))
		})
	case rules.SurfaceGeminiCommand:
		return planTokenized(adapterName, r, roots, targetAbs, func(raw []byte, srcRel string) ([]byte, error) {
			fields, body := frontmatter.Parse(string(raw))
			desc, _ := fields["description"].(string)
			return render.GeminiCommand(spec.CommandDoc{
				Description: strings.TrimSpace(desc),
				Body:        strings.TrimSpace(string(r.ApplyReplace([]byte(body)))),
			})
		})
	default:
		return nil, fmt.Errorf("adapter %s: unknown render surface %q", adapterName, r.Surface)
	}
}

// errSchemaParse marks a canonical schema file friday could not decode.
// Unlike a foreign target friday refuses to clobber, a broken mcp.yaml /
// permissions.yaml / defaults.yaml is friday's OWN input: skipping it would
// leave every adapter rendering nothing from it while `friday push` still
// exits 0, so a CI run provisions agents with no policy applied and reports
// success. planRenderCanonical fails the run on it instead.
var errSchemaParse = errors.New("invalid canonical schema")

func schemaParseError(err error) error { return fmt.Errorf("%w: %v", errSchemaParse, err) }

// formatUnmapped renders MCP/Permissions' bare unmapped entity names (server
// names, permission residue) into one ready-to-surface warning sentence, or
// nil if there is nothing to report. Kept separate from planRenderCanonical
// itself so a surface whose notes are already fully worded (SurfaceDefaults'
// clamp/unsupported sentences) is not forced through this one fixed phrasing.
func formatUnmapped(adapterName string, unmapped []string) []string {
	if len(unmapped) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s cannot represent: %s", adapterName, strings.Join(unmapped, "; "))}
}

// planRenderCanonical resolves r.From[0] across every root — one winner, the
// highest-precedence root that has it, exactly like the merge strategies'
// rules.ResolveSingle — projects its bytes through project, then lands the
// result at r.To per r.Merge. A lower-precedence root's own copy of the same
// file is reported as a Warning (and listed in Shadowed): unlike a glob rule
// there is no "shadowed" report anywhere else for a literal single-source
// rule, so without this the caller could never learn one exists and silently
// loses its content.
func planRenderCanonical(owned *drift.Owned, adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string, applied map[string][]byte, project func(raw []byte) (payload []byte, notes []string, err error)) (Change, error) {
	src := r.From[0]
	dest := filepath.Join(targetAbs, filepath.FromSlash(r.To))
	ch := Change{
		Adapter:     adapterName,
		Direction:   DirPush,
		Sources:     []string{src},
		SourceAbs:   []string{""},
		DestPath:    dest,
		DestRel:     r.To,
		driftExempt: r.Merge != rules.StrategyCopy,
		hookWrite:   installsHookWiring(src, nil),
	}
	matches, err := rules.ExpandLiteralAcrossRoots(roots, src)
	if err != nil {
		return ch, fmt.Errorf("expand %q: %w", src, err)
	}
	if len(matches) == 0 {
		ch.Action = ActionMissingSource
		ch.Reason = fmt.Sprintf("no source file at %q", src)
		return ch, nil
	}
	winner := matches[len(matches)-1]
	ch.SrcAbs = filepath.Join(winner.Root, filepath.FromSlash(winner.Rel))
	ch.SourceAbs[0] = ch.SrcAbs
	ch.Root = rootName(roots, winner.Source)
	srcInfo, err := os.Stat(ch.SrcAbs)
	if err != nil {
		return ch, fmt.Errorf("stat %s: %w", src, err)
	}
	raw, err := os.ReadFile(ch.SrcAbs)
	if err != nil {
		return ch, fmt.Errorf("read %s: %w", src, err)
	}
	ch.SrcContent = raw

	var warnings []string
	if len(matches) > 1 {
		names := make([]string, 0, len(matches)-1)
		for _, m := range matches[:len(matches)-1] {
			names = append(names, m.Source)
			ch.Shadowed = append(ch.Shadowed, filepath.Join(m.Root, filepath.FromSlash(m.Rel)))
		}
		warnings = append(warnings, fmt.Sprintf("%s also defined in %s (shadowed by %s)", src, strings.Join(names, ", "), winner.Source))
	}

	payload, notes, err := project(raw)
	if err != nil {
		if errors.Is(err, errSchemaParse) {
			return ch, fmt.Errorf("%s: %w", src, err)
		}
		ch.Action = ActionUnsupported
		ch.Reason = fmt.Sprintf("cannot render %s for %s: %v", src, adapterName, err)
		return ch, nil
	}
	warnings = append(warnings, notes...)
	if len(warnings) > 0 {
		ch.Warning = strings.Join(warnings, "; ")
	}
	if payload == nil {
		ch.Action = ActionMissingSource
		ch.Reason = fmt.Sprintf("nothing in %s applies to %s", src, adapterName)
		return ch, nil
	}
	payload = r.ApplyReplace(payload)
	ch.mergeSource = payload
	ch.hookWrite = installsHookWiring(src, payload)

	old, exists, destMode, err := readAppliedOrDisk(applied, dest)
	if err != nil {
		return ch, fmt.Errorf("read %s: %w", dest, err)
	}
	if exists {
		ch.OldContent = old
		ch.Mode = destMode
	} else {
		ch.Mode = srcInfo.Mode().Perm()
	}
	var prev []byte
	if owned != nil && ch.driftExempt {
		prev = owned.Get(adapterName, src, dest)
	}

	var merged []byte
	var changed bool
	switch r.Merge {
	case rules.StrategyCopy:
		merged, changed = payload, exists && !equalNormalized(old, payload)
	case rules.StrategyMergeJSON:
		if adapterName == "opencode" && r.Surface == rules.SurfacePermissions {
			merged, changed, err = mergeReplace(old, payload)
		} else {
			merged, changed, err = mergeEntries(old, payload, prev)
		}
	case rules.StrategyMergeTOML:
		var tables []string
		if tables, err = Tables(payload); err == nil {
			merged, changed, err = Splice(old, payload, prev, tables)
		}
	default:
		return ch, fmt.Errorf("planRender: unsupported merge %q", r.Merge)
	}
	if err != nil {
		ch.Action = ActionUnsupported
		ch.Reason = fmt.Sprintf("cannot merge into %s: %v", r.To, err)
		return ch, nil
	}
	ch.NewContent = merged
	switch {
	case !exists:
		ch.Action = ActionCreate
	case changed:
		ch.Action = ActionUpdate
	default:
		ch.Action = ActionInSync
	}
	return ch, nil
}

// planPull reverses each rule: target file → store file. Only copy rules, the
// mcp surface, and permissions merged as merge-json/merge-toml have a real
// inverse. Everything else is reported as an unsupported skip with its own
// reason rather than silently omitted — concatenate and frontmatter_strip are
// lossy in reverse, plain merge-json/merge-toml and the defaults surface land
// in targets whose other keys friday does not manage, codex-agent and
// gemini-command drop the markdown frontmatter they rendered from, and a
// permissions rule merged as copy owns its target outright (push regenerates
// it). A capture whose winning copy lives in a configured source is refused
// too: those checkouts stay read-only (see refuseSourceWriteBack).
func planPull(_ *drift.Owned, adapterName string, ad *config.Adapter, roots []rules.Root, targetAbs string) ([]Change, error) {
	var out []Change
	for ri, r := range ad.Rules {
		if r.Strategy == rules.StrategyMergeJSON {
			out = append(out, Change{
				Adapter:   adapterName,
				Direction: DirPull,
				RuleIndex: ri,
				DestRel:   r.To,
				Action:    ActionUnsupported,
				Reason:    "merge-json rule cannot be pulled (the target co-owns keys friday does not manage)",
			})
			continue
		}
		if r.Strategy == rules.StrategyMergeTOML {
			out = append(out, Change{
				Adapter:   adapterName,
				Direction: DirPull,
				RuleIndex: ri,
				DestRel:   r.To,
				Action:    ActionUnsupported,
				Reason:    "merge-toml rule cannot be pulled (the target co-owns tables friday does not manage)",
			})
			continue
		}
		if r.Strategy == rules.StrategyConcatenate {
			out = append(out, Change{
				Adapter:   adapterName,
				Direction: DirPull,
				RuleIndex: ri,
				DestRel:   r.To,
				Action:    ActionUnsupported,
				Reason:    "concatenate rule cannot be pulled (multi-source → single target is irreversible)",
			})
			continue
		}
		if r.Strategy == rules.StrategyRender {
			switch r.Surface {
			case rules.SurfaceCodexAgent, rules.SurfaceGeminiCommand:
				out = append(out, Change{
					Adapter:   adapterName,
					Direction: DirPull,
					RuleIndex: ri,
					DestRel:   r.To,
					Action:    ActionUnsupported,
					Reason:    "codex-agent/gemini-command rule cannot be pulled (markdown→structured-config drops frontmatter, irreversible)",
				})
			case rules.SurfaceMCP:
				mch, emit, merr := pullRenderMCP(adapterName, r, roots, targetAbs)
				if merr != nil {
					return nil, merr
				}
				if emit {
					mch.RuleIndex = ri
					out = append(out, mch)
				}
			case rules.SurfaceDefaults:
				out = append(out, Change{
					Adapter:   adapterName,
					Direction: DirPull,
					RuleIndex: ri,
					DestRel:   r.To,
					Action:    ActionUnsupported,
					Reason:    "defaults rule cannot be pulled (the target co-owns keys friday does not manage, and a narrowed native enum cannot recover the canonical union value it clamped from)",
				})
			case rules.SurfacePermissions:
				if r.Merge == rules.StrategyCopy {
					// A whole-file-owned target (gemini): nothing co-owned to
					// reconcile against and no passthrough fragment shares
					// this target, so there is nowhere for a residue to go
					// either. Re-run `friday push` to regenerate it instead.
					out = append(out, Change{
						Adapter:   adapterName,
						Direction: DirPull,
						RuleIndex: ri,
						DestRel:   r.To,
						Action:    ActionUnsupported,
						Reason:    "permissions rule with merge: copy cannot be pulled (the target is friday-owned outright — push regenerates it instead)",
					})
					continue
				}
				pchs, perr := pullRenderPermissions(adapterName, r, roots, targetAbs)
				if perr != nil {
					return nil, perr
				}
				for _, pc := range pchs {
					pc.RuleIndex = ri
					out = append(out, pc)
				}
			default:
				return nil, fmt.Errorf("adapter %s: unknown render surface %q", adapterName, r.Surface)
			}
			continue
		}
		if len(r.FrontmatterStrip) > 0 {
			out = append(out, Change{
				Adapter:   adapterName,
				Direction: DirPull,
				RuleIndex: ri,
				DestRel:   r.To,
				Action:    ActionUnsupported,
				Reason:    "rule has frontmatter_strip — pulling would re-inject stripped fields",
			})
			continue
		}
		// A literal template maps several from-variants to one target file;
		// only the first (most-preferred) variant may capture it — pulling
		// the same target into every variant would fan one file's content
		// out over unrelated store files.
		seenTarget := map[string]bool{}
		for _, pat := range r.From {
			matches, _, err := rules.ExpandAll(roots, pat)
			if err != nil {
				return nil, fmt.Errorf("expand %q: %w", pat, err)
			}
			anchor := rules.Anchor(pat)
			for _, m := range matches {
				tokens := rules.TokensFor(m.Rel, anchor)
				destRel := tokens.Expand(r.To)
				targetAbsFile := filepath.Join(targetAbs, destRel)
				// m.Root is already the highest-precedence source holding
				// m.Rel (ExpandAll's own collision resolution), so the pull
				// naturally writes back to the same source push would have
				// rendered from — no separate owner lookup needed.
				storeAbsFile := filepath.Join(m.Root, filepath.FromSlash(m.Rel))
				if seenTarget[targetAbsFile] {
					continue
				}
				seenTarget[targetAbsFile] = true

				targetInfo, err := os.Stat(targetAbsFile)
				if os.IsNotExist(err) {
					// Nothing to pull from this side.
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("stat %s: %w", targetAbsFile, err)
				}
				targetContent, err := os.ReadFile(targetAbsFile)
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", targetAbsFile, err)
				}
				storeContent, err := os.ReadFile(storeAbsFile)
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", storeAbsFile, err)
				}

				ch := Change{
					Adapter:    adapterName,
					Direction:  DirPull,
					RuleIndex:  ri,
					Sources:    []string{destRel},
					SrcAbs:     targetAbsFile,
					SrcContent: targetContent,
					DestPath:   storeAbsFile,
					DestRel:    m.Rel,
					OldContent: storeContent,
					Mode:       targetInfo.Mode().Perm(),
				}
				// Compare in target-space: in-sync means the target matches
				// what push would write. Inverting the target instead would
				// flag natural occurrences of replace values as edits.
				switch {
				case equalNormalized(targetContent, r.ApplyReplace(storeContent)):
					ch.Action = ActionInSync
					ch.NewContent = storeContent
				default:
					// There is a real edit to capture, but the winning copy
					// lives in a source checkout friday keeps read-only.
					// Refuse rather than write a local modification the next
					// `friday source update` would silently discard.
					if reason, refused := refuseSourceWriteBack(roots, rules.Root{Name: m.Source, Dir: m.Root}, m.Rel); refused {
						ch.Action = ActionUnsupported
						ch.Reason = reason
						ch.DestPath = ""
						break
					}
					ch.Action = ActionUpdate
					ch.NewContent = pullContent(r, storeContent, targetContent)
				}
				out = append(out, ch)
			}
		}
	}
	return out, nil
}

// pullContent computes the store-side content for a pull/import update. The
// replace inverse is textual, so applying it to the whole target would also
// rewrite natural occurrences of a replace value (a literal "~/.friday" in
// prose) that the push direction left alone. Lines the target didn't touch
// therefore keep their original store form — aligned via LCS against what
// push rendered — and only edited or new lines go through the inverse.
func pullContent(r *rules.Rule, storeContent, targetContent []byte) []byte {
	if len(r.Replace) == 0 {
		return targetContent
	}
	// The line alignment assumes replace pairs stay within a line; a pair
	// carrying a newline breaks it, so fall back to the whole-file inverse.
	for k, v := range r.Replace {
		if strings.ContainsRune(k, '\n') || strings.ContainsRune(v, '\n') {
			return r.ApplyReplaceInverse(targetContent)
		}
	}
	storeLines := strings.Split(string(storeContent), "\n")
	rendered := strings.Split(string(r.ApplyReplace(storeContent)), "\n")
	if len(storeLines) != len(rendered) {
		return r.ApplyReplaceInverse(targetContent)
	}
	targetLines := strings.Split(string(targetContent), "\n")
	unchanged := textdiff.LCSPairs(targetLines, rendered)
	out := make([]string, len(targetLines))
	for i, line := range targetLines {
		if j, ok := unchanged[i]; ok {
			out[i] = storeLines[j]
		} else {
			out[i] = string(r.ApplyReplaceInverse([]byte(line)))
		}
	}
	return []byte(strings.Join(out, "\n"))
}
