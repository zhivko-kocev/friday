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
	return runWith(cfg, opts, DirPull, planImport)
}

// planImport reverses one adapter: target files → store files. Concatenate
// and frontmatter-stripped rules are lossy in reverse and reported as
// unsupported; replace rules invert cleanly.
func planImport(_ *drift.Owned, adapterName string, ad *config.Adapter, storeAbs, targetAbs string) ([]Change, error) {
	var out []Change
	skip := func(ri int, to, reason string) {
		out = append(out, Change{
			Adapter:   adapterName,
			Direction: DirPull,
			RuleIndex: ri,
			DestRel:   to,
			Action:    ActionUnsupported,
			Reason:    reason,
		})
	}

	for ri, r := range ad.Rules {
		if r.Strategy == rules.StrategyMergeJSON {
			skip(ri, r.To, "merge-json rule cannot be imported (the target co-owns keys friday does not manage)")
			continue
		}
		if r.Strategy == rules.StrategyConcatenate {
			skip(ri, r.To, "concatenate rule cannot be imported (multi-source → single target is irreversible)")
			continue
		}
		if len(r.FrontmatterStrip) > 0 {
			skip(ri, r.To, "rule has frontmatter_strip — importing would re-inject stripped fields")
			continue
		}
		if r.Strategy == rules.StrategyMDToTOML || r.Strategy == rules.StrategyMDToJSON {
			skip(ri, r.To, "md-to-toml/md-to-json rule cannot be imported (markdown→structured-config drops frontmatter, irreversible)")
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
			var storeRel string
			if glob == r.To { // literal template: one file, maps to the from-pattern
				storeRel = literalStoreDest(r.From, storeAbs)
			} else if storeRel = invertToFrom(r, m); storeRel == "" {
				skip(ri, m, "matches the rule's target glob but no from-pattern maps it into the store")
				continue
			}
			ch, err := planImportFile(adapterName, r, storeAbs, targetAbs, storeRel, m)
			if err != nil {
				return nil, err
			}
			ch.RuleIndex = ri
			out = append(out, ch)
		}
	}
	return out, nil
}

// invertToFrom maps a target file back to the store path of the first
// from-pattern that accepts it. Inversion must run at the same granularity
// as expansion: every pattern has its own anchor, and a file no pattern
// matches has no store home — importing it anyway would create an orphan
// that push never consumes, breaking the push/import round trip.
func invertToFrom(r *rules.Rule, targetRel string) string {
	for _, pat := range r.From {
		storeRel, ok := rules.Invert(r.To, targetRel, rules.Anchor(pat))
		if ok && rules.Match(pat, storeRel) {
			return storeRel
		}
	}
	return ""
}

// literalStoreDest picks where a literal-template rule's file lands in the
// store: the first from-variant that already exists (so a store shaped like
// core/core.md doesn't grow a duplicate root core.md), else the first
// pattern — the canonical spelling.
func literalStoreDest(from rules.FromSpec, storeAbs string) string {
	for _, pat := range from {
		if strings.ContainsAny(pat, "*?[") {
			continue
		}
		if _, err := os.Stat(filepath.Join(storeAbs, filepath.FromSlash(pat))); err == nil {
			return pat
		}
	}
	return from[0]
}

func planImportFile(adapterName string, r *rules.Rule, storeAbs, targetAbs, storeRel, targetRel string) (Change, error) {
	targetAbsFile := filepath.Join(targetAbs, filepath.FromSlash(targetRel))
	storeAbsFile := filepath.Join(storeAbs, filepath.FromSlash(storeRel))

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
