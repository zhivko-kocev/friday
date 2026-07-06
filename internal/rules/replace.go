package rules

import (
	"bytes"
	"sort"
)

// ApplyReplace rewrites content in the push direction (store → target),
// substituting each replace key with its value.
func (r *Rule) ApplyReplace(b []byte) []byte {
	return applyPairs(b, r.Replace, false)
}

// ApplyReplaceInverse rewrites content in the pull direction (target → store),
// substituting each replace value back to its key. The inverse is well-defined
// because Normalize rejects duplicate values.
func (r *Rule) ApplyReplaceInverse(b []byte) []byte {
	return applyPairs(b, r.Replace, true)
}

func applyPairs(b []byte, m map[string]string, inverse bool) []byte {
	if len(m) == 0 {
		return b
	}
	type pair struct{ old, new string }
	pairs := make([]pair, 0, len(m))
	for k, v := range m {
		if inverse {
			pairs = append(pairs, pair{old: v, new: k})
		} else {
			pairs = append(pairs, pair{old: k, new: v})
		}
	}
	// Longest-first so a marker that contains another marker as a prefix is
	// never shadowed; length ties break lexicographically. The fixed order
	// keeps output byte-identical across runs — the drift hash depends on it.
	sort.Slice(pairs, func(i, j int) bool {
		if len(pairs[i].old) != len(pairs[j].old) {
			return len(pairs[i].old) > len(pairs[j].old)
		}
		return pairs[i].old < pairs[j].old
	})
	for _, p := range pairs {
		b = bytes.ReplaceAll(b, []byte(p.old), []byte(p.new))
	}
	return b
}
