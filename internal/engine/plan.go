package engine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/frontmatter"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// planPush walks one adapter's rules and produces the changes that a push
// would perform — without writing anything or consulting the drift store.
func planPush(adapterName string, ad *config.Adapter, storeAbs, targetAbs string) ([]Change, error) {
	var out []Change
	for _, r := range ad.Rules {
		switch r.Strategy {
		case rules.StrategyConcatenate:
			ch, err := planConcatenate(adapterName, r, storeAbs, targetAbs)
			if err != nil {
				return nil, err
			}
			out = append(out, ch)
		case rules.StrategyCopy:
			chs, err := planCopy(adapterName, r, storeAbs, targetAbs)
			if err != nil {
				return nil, err
			}
			out = append(out, chs...)
		default:
			return nil, fmt.Errorf("adapter %s: unknown strategy %q", adapterName, r.Strategy)
		}
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
	for _, pat := range r.From {
		matches, err := rules.Expand(storeAbs, pat)
		if err != nil {
			return nil, fmt.Errorf("expand %q: %w", pat, err)
		}
		if len(matches) == 0 {
			out = append(out, Change{
				Adapter:   adapterName,
				Direction: DirPush,
				Sources:   []string{pat},
				Action:    ActionMissingSource,
				Reason:    "no files matched in store",
			})
			continue
		}
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
			content := []byte(frontmatter.Strip(string(raw), r.FrontmatterStrip))
			tokens := rules.TokensFor(m, anchor)
			destRel := tokens.Expand(r.To)
			destAbs := filepath.Join(targetAbs, destRel)

			ch := Change{
				Adapter:    adapterName,
				Direction:  DirPush,
				Sources:    []string{m},
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
	return out, nil
}

// planPull reverses each rule: target file → store file. Concatenate rules and
// rules with frontmatter_strip are skipped (lossy in reverse).
func planPull(adapterName string, ad *config.Adapter, storeAbs, targetAbs string) ([]Change, error) {
	var out []Change
	for _, r := range ad.Rules {
		if r.Strategy == rules.StrategyConcatenate {
			out = append(out, Change{
				Adapter:   adapterName,
				Direction: DirPull,
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
				DestRel:   r.To,
				Action:    ActionUnsupported,
				Reason:    "rule has frontmatter_strip — pulling would re-inject stripped fields",
			})
			continue
		}
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
					Sources:    []string{destRel},
					DestPath:   storeAbsFile,
					DestRel:    m,
					NewContent: targetContent,
					OldContent: storeContent,
					Mode:       targetInfo.Mode().Perm(),
				}
				if equalNormalized(targetContent, storeContent) {
					ch.Action = ActionInSync
				} else {
					ch.Action = ActionUpdate
				}
				out = append(out, ch)
			}
		}
	}
	return out, nil
}
