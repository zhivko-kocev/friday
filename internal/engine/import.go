package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// Import materializes an adapter's on-disk target back into the store by
// reverse-expanding each rule's `to` template. Unlike Pull — which iterates
// store matches and therefore can't see files that exist only on the target
// side — Import walks the target, so it bootstraps empty stores and captures
// files authored directly in an agent dir.
func Import(cfg *config.Config, opts Options) ([]Change, error) {
	return runWith(cfg, opts, DirPull, func(owned *drift.Owned, name string, ad *config.Adapter, roots []rules.Root, targetAbs string) ([]Change, error) {
		return planImport(owned, name, ad, roots, targetAbs, opts.ToSource)
	})
}

// planImport reverses one adapter: target files → store files. Concatenate
// and frontmatter-stripped rules are lossy in reverse and reported as
// unsupported; replace rules invert cleanly.
func planImport(_ *drift.Owned, adapterName string, ad *config.Adapter, roots []rules.Root, targetAbs, toSource string) ([]Change, error) {
	var out []Change
	// claimed holds every target-relative path already owned by an earlier
	// rule, so a later rule cannot capture it a second time. See the comment
	// below; declared here because skip() reserves paths into it too.
	claimed := map[string]bool{}
	skip := func(ri int, to, reason string) {
		// A rule that cannot be IMPORTED still WRITES this path on push
		// (concatenate composes CLAUDE.md; merge-json/merge-toml/render
		// compose settings.json). Reserving its literal target keeps the
		// native mirror from also capturing that file, which would leave the
		// store with a whole-file copy that clobbers the composition on the
		// next push.
		if !rules.HasWildcard(to) {
			claimed[to] = true
		}
		out = append(out, Change{
			Adapter:   adapterName,
			Direction: DirPull,
			RuleIndex: ri,
			DestRel:   to,
			Action:    ActionUnsupported,
			Reason:    reason,
		})
	}

	// A target file may match several rules' templates — every preset pairs
	// its canonical rules (agents/*.md, commands/*.md, ...) with a
	// native/<adapter>/**/* mirror whose "{relpath}" template matches the
	// adapter's whole tree. On import that would capture one file twice, into
	// both the canonical dir and native/, leaving two store copies that then
	// both render to the same target — a real dest-collision friday's own
	// `claim` created. Rules are ordered most-specific-first, so the first
	// rule to claim a target file owns it and the mirror only picks up what
	// nothing above it models, which is exactly what the escape hatch means.
	for ri, r := range ad.Rules {
		if r.Strategy == rules.StrategyMergeJSON {
			skip(ri, r.To, "merge-json rule cannot be imported (the target co-owns keys friday does not manage)")
			continue
		}
		if r.Strategy == rules.StrategyMergeTOML {
			skip(ri, r.To, "merge-toml rule cannot be imported (the target co-owns tables friday does not manage)")
			continue
		}
		if r.Strategy == rules.StrategyConcatenate {
			// This is the one refusal a user hits on their very first
			// `friday claim`, so it names the fix rather than just the
			// reason: their existing instruction file stays on disk, and
			// folding it into the entry file is what makes friday own it.
			skip(ri, r.To, fmt.Sprintf(
				"%s is composed from several store files, so friday cannot split it back apart — copy the parts you want to keep into the store's entry file (core.md), then `friday push`",
				r.To))
			continue
		}
		if len(r.FrontmatterStrip) > 0 {
			skip(ri, r.To, "rule has frontmatter_strip — importing would re-inject stripped fields")
			continue
		}
		if r.Strategy == rules.StrategyRender {
			chs, err := importRender(adapterName, ri, r, roots, targetAbs)
			if err != nil {
				return nil, err
			}
			out = append(out, chs...)
			continue
		}
		glob, ok := rules.ToGlob(r.To)
		if !ok {
			skip(ri, r.To, fmt.Sprintf("template %q is not invertible", r.To))
			continue
		}
		matches, err := rules.Expand(targetAbs, glob)
		if err != nil {
			return nil, fmt.Errorf("expand %q against target: %w", glob, err)
		}
		for _, m := range matches {
			if claimed[m] {
				continue
			}
			var storeRel string
			if glob == r.To { // literal template: one file, maps to the from-pattern
				storeRel = literalStoreDest(r.From, roots)
			} else if storeRel = invertToFrom(r, m); storeRel == "" {
				skip(ri, m, "matches the rule's target glob but no from-pattern maps it into the store")
				continue
			}
			ch, err := planImportFile(adapterName, r, roots, targetAbs, storeRel, m, toSource)
			if err != nil {
				return nil, err
			}
			ch.RuleIndex = ri
			claimed[m] = true
			out = append(out, ch)
		}
	}
	return out, nil
}

// importRender captures a render rule's target back into its canonical schema
// where an inverse exists.
//
// Two of the five surfaces do have one, and `friday pull` already uses it:
// mcp always, and permissions whenever the target is co-owned rather than
// friday-owned outright. Reporting every render rule unsupported meant claim
// could never capture a user's existing native MCP servers or permission
// policy — the single most valuable thing to adopt off an already-configured
// agent — while pull recovered the identical content from the identical rule.
// The other three are genuinely one-way: defaults' native enums are narrower
// than the canonical union they were clamped from, and the two markdown
// surfaces drop frontmatter on the way out.
func importRender(adapterName string, ri int, r *rules.Rule, roots []rules.Root, targetAbs string) ([]Change, error) {
	unsupported := func(reason string) []Change {
		return []Change{{
			Adapter:   adapterName,
			Direction: DirPull,
			RuleIndex: ri,
			DestRel:   r.To,
			Action:    ActionUnsupported,
			Reason:    reason,
		}}
	}
	switch r.Surface {
	case rules.SurfaceMCP:
		ch, emit, err := pullRenderMCP(adapterName, r, roots, targetAbs)
		if err != nil {
			return nil, err
		}
		if !emit {
			return nil, nil
		}
		ch.RuleIndex = ri
		return []Change{ch}, nil
	case rules.SurfacePermissions:
		if r.Merge == rules.StrategyCopy {
			return unsupported("permissions rule with merge: copy cannot be imported (the target is friday-owned outright — push regenerates it instead)"), nil
		}
		chs, err := pullRenderPermissions(adapterName, r, roots, targetAbs)
		if err != nil {
			return nil, err
		}
		for i := range chs {
			chs[i].RuleIndex = ri
		}
		return chs, nil
	case rules.SurfaceDefaults:
		return unsupported("defaults rule cannot be imported (a narrowed native enum cannot recover the canonical union value it clamped from)"), nil
	case rules.SurfaceCodexAgent, rules.SurfaceGeminiCommand:
		return unsupported("codex-agent/gemini-command rule cannot be imported (markdown→structured-config drops frontmatter, irreversible)"), nil
	}
	return unsupported(fmt.Sprintf("render surface %q has no import inverse", r.Surface)), nil
}

// invertToFrom maps a target file back to the store path of the first
// from-pattern that accepts it. Inversion must run at the same granularity
// as expansion: every pattern has its own anchor, and a file no pattern
// matches has no store home — importing it anyway would create an orphan
// that push never consumes, breaking the push/import round trip.
func invertToFrom(r *rules.Rule, targetRel string) string {
	for _, pat := range r.From {
		storeRel, ok := rules.Invert(r.To, targetRel, rules.Anchor(pat))
		if ok && rules.Matches(pat, storeRel) {
			return storeRel
		}
	}
	return ""
}

// literalStoreDest picks where a literal-template rule's file lands in the
// store: the first from-variant that already exists in any source (so a
// store shaped like core/core.md doesn't grow a duplicate root core.md),
// else the first pattern — the canonical spelling.
func literalStoreDest(from rules.FromSpec, roots []rules.Root) string {
	for _, pat := range from {
		if strings.ContainsAny(pat, "*?[") {
			continue
		}
		if _, _, ok := rules.ResolveSingle(roots, pat); ok {
			return pat
		}
	}
	return from[0]
}

// destRoot picks which root a captured import edit is written into: the
// existing owner if any source already holds rel, else the caller's
// requested toSource by name, else the primary — roots' last entry, per
// source.Load's construction invariant.
//
// An owner that is a configured source is refused rather than written to: a
// source checkout is read-only, so the capture would be discarded by the next
// `friday source update`. toSource is different — it names a source the user
// asked for explicitly, this invocation (`friday claim --to`), so it is
// honored.
func destRoot(roots []rules.Root, rel, toSource string) (root rules.Root, refusal string) {
	if owner, _, ok := rules.ResolveSingle(roots, rel); ok {
		if reason, refused := refuseSourceWriteBack(roots, owner, rel); refused {
			return rules.Root{}, reason
		}
		return owner, ""
	}
	if toSource != "" {
		for _, r := range roots {
			if r.Name == toSource {
				return r, ""
			}
		}
	}
	return primaryRoot(roots), ""
}

func planImportFile(adapterName string, r *rules.Rule, roots []rules.Root, targetAbs, storeRel, targetRel, toSource string) (Change, error) {
	targetAbsFile := filepath.Join(targetAbs, filepath.FromSlash(targetRel))
	root, refusal := destRoot(roots, storeRel, toSource)
	if refusal != "" {
		return Change{
			Adapter:   adapterName,
			Direction: DirPull,
			Sources:   []string{targetRel},
			SrcAbs:    targetAbsFile,
			DestRel:   storeRel,
			Action:    ActionUnsupported,
			Reason:    refusal,
		}, nil
	}
	storeAbsFile := filepath.Join(root.Dir, filepath.FromSlash(storeRel))

	info, err := os.Stat(targetAbsFile)
	if err != nil {
		return Change{}, fmt.Errorf("stat %s: %w", targetAbsFile, err)
	}
	raw, err := os.ReadFile(targetAbsFile)
	if err != nil {
		return Change{}, fmt.Errorf("read %s: %w", targetAbsFile, err)
	}

	ch := Change{
		Adapter:    adapterName,
		Direction:  DirPull,
		Sources:    []string{targetRel},
		SrcAbs:     targetAbsFile,
		SrcContent: raw,
		DestPath:   storeAbsFile,
		DestRel:    storeRel,
		Mode:       info.Mode().Perm(),
	}
	old, err := os.ReadFile(storeAbsFile)
	switch {
	case os.IsNotExist(err):
		ch.Action = ActionCreate
		ch.NewContent = r.ApplyReplaceInverse(raw)
	case err != nil:
		return ch, fmt.Errorf("read %s: %w", storeAbsFile, err)
	default:
		ch.OldContent = old
		// Compare in target-space, like planPull: inverting the target
		// instead would flag natural occurrences of replace values in the
		// store as phantom edits — and a forced import (eject) would then
		// rewrite them in place.
		if equalNormalized(raw, r.ApplyReplace(old)) {
			ch.Action = ActionInSync
			ch.NewContent = old
		} else {
			ch.Action = ActionUpdate
			ch.NewContent = pullContent(r, old, raw)
		}
	}
	return ch, nil
}
