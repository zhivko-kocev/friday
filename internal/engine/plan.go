package engine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/frontmatter"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// planPush walks one adapter's rules and produces the changes that a push
// would perform — without writing anything or consulting the drift store.
func planPush(owned *drift.Owned, adapterName string, ad *config.Adapter, storeAbs, targetAbs string) ([]Change, error) {
	var out []Change
	for i, r := range ad.Rules {
		var chs []Change
		switch r.Strategy {
		case rules.StrategyConcatenate:
			ch, err := planConcatenate(adapterName, r, storeAbs, targetAbs)
			if err != nil {
				return nil, err
			}
			chs = []Change{ch}
		case rules.StrategyCopy:
			var err error
			chs, err = planCopy(adapterName, r, storeAbs, targetAbs)
			if err != nil {
				return nil, err
			}
		case rules.StrategyMergeJSON:
			ch, err := planMergeJSON(owned, adapterName, r, storeAbs, targetAbs)
			if err != nil {
				return nil, err
			}
			chs = []Change{ch}
		default:
			return nil, fmt.Errorf("adapter %s: unknown strategy %q", adapterName, r.Strategy)
		}
		for j := range chs {
			chs[j].RuleIndex = i
		}
		out = append(out, chs...)
	}
	return out, nil
}

func planConcatenate(adapterName string, r *rules.Rule, storeAbs, targetAbs string) (Change, error) {
	var (
		parts   [][]byte
		sources []string
	)
	for _, pat := range r.From {
		matches, err := rules.Expand(storeAbs, pat)
		if err != nil {
			return Change{}, fmt.Errorf("expand %q: %w", pat, err)
		}
		for _, m := range matches {
			data, err := os.ReadFile(filepath.Join(storeAbs, m))
			if err != nil {
				return Change{}, fmt.Errorf("read %s: %w", m, err)
			}
			data = []byte(frontmatter.Strip(string(data), r.FrontmatterStrip))
			data = r.ApplyReplace(data)
			parts = append(parts, data)
			sources = append(sources, m)
		}
	}
	dest := filepath.Join(targetAbs, r.To)
	ch := Change{
		Adapter:   adapterName,
		Direction: DirPush,
		Sources:   sources,
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

func planCopy(adapterName string, r *rules.Rule, storeAbs, targetAbs string) ([]Change, error) {
	var out []Change
	// Tokenized templates make the from-patterns independent content globs —
	// each one failing to match is worth reporting. A literal template's
	// from-list carries alternative spellings (core.md vs core/core.md) where
	// only one is expected to exist, so those report per rule instead.
	perPattern := rules.HasToken(r.To)
	matchedAny := false
	// A literal template (or colliding filenames) can map several sources to
	// one destination; the from-list is ordered most-preferred first, so the
	// first source wins. `friday doctor` reports the collision.
	seenDest := map[string]bool{}
	for _, pat := range r.From {
		matches, err := rules.Expand(storeAbs, pat)
		if err != nil {
			return nil, fmt.Errorf("expand %q: %w", pat, err)
		}
		if len(matches) == 0 {
			if perPattern {
				out = append(out, Change{
					Adapter:   adapterName,
					Direction: DirPush,
					Sources:   []string{pat},
					Action:    ActionMissingSource,
					Reason:    fmt.Sprintf("no source files matched %q", pat),
				})
			}
			continue
		}
		matchedAny = true
		anchor := rules.Anchor(pat)
		for _, m := range matches {
			srcAbs := filepath.Join(storeAbs, m)
			info, err := os.Stat(srcAbs)
			if err != nil {
				return nil, fmt.Errorf("stat %s: %w", m, err)
			}
			raw, err := os.ReadFile(srcAbs)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", m, err)
			}
			content := r.ApplyReplace([]byte(frontmatter.Strip(string(raw), r.FrontmatterStrip)))
			tokens := rules.TokensFor(m, anchor)
			destRel := tokens.Expand(r.To)
			destAbs := filepath.Join(targetAbs, destRel)
			if seenDest[destAbs] {
				continue
			}
			seenDest[destAbs] = true

			ch := Change{
				Adapter:    adapterName,
				Direction:  DirPush,
				Sources:    []string{m},
				SrcAbs:     srcAbs,
				SrcContent: raw,
				DestPath:   destAbs,
				DestRel:    destRel,
				NewContent: content,
				Mode:       info.Mode().Perm(),
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
// A non-empty but unparseable target is an error — friday writes nothing rather
// than clobber a file whose contents it cannot understand.
func planMergeJSON(owned *drift.Owned, adapterName string, r *rules.Rule, storeAbs, targetAbs string) (Change, error) {
	src := r.From[0]
	srcAbs := filepath.Join(storeAbs, filepath.FromSlash(src))
	dest := filepath.Join(targetAbs, filepath.FromSlash(r.To))
	// prev is friday's last-written source for this target (nil on first push or
	// a cleared cache). It lets the merge drop friday's own stale entries after a
	// store edit; without it the merge still preserves user hooks and stays
	// idempotent, it just can't remove a since-changed entry.
	var prev []byte
	if owned != nil {
		prev = owned.Get(adapterName, dest)
	}
	ch := Change{
		Adapter:     adapterName,
		Direction:   DirPush,
		Sources:     []string{src},
		DestPath:    dest,
		DestRel:     r.To,
		driftExempt: true,
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

	old, err := os.ReadFile(dest)
	switch {
	case os.IsNotExist(err):
		merged, err := mergeEntries(nil, source, prev)
		if err != nil {
			// Unparseable store JSON is the user's to fix; skip this one write
			// rather than abort the whole run (and never clobber the target).
			ch.Action = ActionUnsupported
			ch.Reason = fmt.Sprintf("cannot merge %s: %v", src, err)
			return ch, nil
		}
		ch.NewContent = merged
		ch.Action = ActionCreate
		ch.Mode = info.Mode().Perm()
	case err != nil:
		return ch, fmt.Errorf("read %s: %w", dest, err)
	default:
		ch.OldContent = old
		if fi, serr := os.Stat(dest); serr == nil {
			// Preserve the target's mode — settings.json may be 0600.
			ch.Mode = fi.Mode().Perm()
		}
		merged, err := mergeEntries(old, source, prev)
		if err != nil {
			// A hand-edited, unparseable settings.json (or a broken store file):
			// surface it as a skip so unrelated adapters still sync, and write
			// nothing rather than overwrite a file friday can't understand.
			ch.Action = ActionUnsupported
			ch.Reason = fmt.Sprintf("%s: %v — fix the JSON, then push", r.To, err)
			return ch, nil
		}
		// mergeEntries already parsed old, so canonicalize won't fail here.
		canonOld, err := canonicalize(old)
		if err != nil {
			ch.Action = ActionUnsupported
			ch.Reason = fmt.Sprintf("%s is not valid JSON: %v", r.To, err)
			return ch, nil
		}
		ch.NewContent = merged
		if equalNormalized(canonOld, merged) {
			ch.Action = ActionInSync
		} else {
			ch.Action = ActionUpdate
		}
	}
	return ch, nil
}

// planPull reverses each rule: target file → store file. Concatenate rules and
// rules with frontmatter_strip are skipped (lossy in reverse).
func planPull(_ *drift.Owned, adapterName string, ad *config.Adapter, storeAbs, targetAbs string) ([]Change, error) {
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
			matches, err := rules.Expand(storeAbs, pat)
			if err != nil {
				return nil, fmt.Errorf("expand %q: %w", pat, err)
			}
			anchor := rules.Anchor(pat)
			for _, m := range matches {
				tokens := rules.TokensFor(m, anchor)
				destRel := tokens.Expand(r.To)
				targetAbsFile := filepath.Join(targetAbs, destRel)
				storeAbsFile := filepath.Join(storeAbs, m)
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
					DestRel:    m,
					OldContent: storeContent,
					Mode:       targetInfo.Mode().Perm(),
				}
				// Compare in target-space: in-sync means the target matches
				// what push would write. Inverting the target instead would
				// flag natural occurrences of replace values as edits.
				if equalNormalized(targetContent, r.ApplyReplace(storeContent)) {
					ch.Action = ActionInSync
					ch.NewContent = storeContent
				} else {
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
	unchanged := conflict.LCSPairs(targetLines, rendered)
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
