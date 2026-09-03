package engine

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable/edit"
)

// Tables returns the top-level keys a rendered payload defines, sorted. It
// bounds which of the target's root entries Splice may look at; what
// friday actually owns *inside* one of them is decided per-leaf by Splice
// itself, so a top-level key is safe to name here whether the payload fills
// it entirely ([mcp_servers.<name>]) or contributes a single field to a
// table the user also populates ([tui] theme, next to their notifications).
//
// A top-level key whose value is not a table at all — a bare scalar or
// array at the document root, like codex's own `model = "..."` — is
// returned the same way; Splice treats it as its own single leaf.
func Tables(payload []byte) ([]string, error) {
	doc, err := edit.Parse(stripBOM(payload))
	if err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}
	root, ok := doc.Get(nil)
	if !ok {
		return nil, nil
	}
	rootMap, ok := root.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("payload root is not a table")
	}
	tables := make([]string, 0, len(rootMap))
	for key := range rootMap {
		tables = append(tables, key)
	}
	sort.Strings(tables)
	return tables, nil
}

// Splice merges payload's owned content into existing. Ownership is
// per-leaf, never per-table: friday owns the scalar/array positions its
// payload actually renders, so a user's own sibling key inside a table
// friday also writes into — codex's `[tui] notifications` next to friday's
// `[tui] theme` — is not friday's and is never touched. tables is normally
// Tables(payload) and bounds which top-level paths are considered at all.
//
// prev (may be nil) is friday's previously rendered payload for the same
// target. A leaf prev defines that payload no longer does is retracted —
// but only when the target still holds exactly what friday recorded writing
// there. A since-hand-edited value is the user's now and stays, the same
// content-based rule mergeValue applies on the JSON side. A table left
// empty by retraction loses its now-orphaned header too.
//
// changed reports whether anything was actually written, so callers can
// treat a no-op splice as in-sync without a second parse. A leaf already
// holding exactly payload's value is left completely untouched, so
// re-splicing identical content never perturbs the target's formatting.
//
// What survives byte for byte is everything friday does not own: unowned
// keys, tables, comments, blank lines, and hand formatting, plus the
// position and attached comments of an owned leaf that already exists
// (go-toml/edit replaces just that value's bytes). Three things do change
// inside the owned set: a brand-new leaf is appended at the end of its
// section rather than in payload order, an owned value is re-rendered by
// go-toml's encoder so its quoting may change ("dark" becomes 'dark'), and
// a retracted leaf takes the comments attached to it with it (edit
// attributes the contiguous full-line comments directly above an
// expression to that expression).
func Splice(existing, payload, prev []byte, tables []string) (spliced []byte, changed bool, err error) {
	bomPrefix, existing := trimBOM(existing)
	dst, err := edit.Parse(existing)
	if err != nil {
		return nil, false, fmt.Errorf("parse target: %w", err)
	}
	src, err := edit.Parse(stripBOM(payload))
	if err != nil {
		return nil, false, fmt.Errorf("parse payload: %w", err)
	}
	owned := ownedLeaves(src, tables)

	if len(bytes.TrimSpace(prev)) > 0 {
		retracted, err := retract(dst, prev, owned)
		if err != nil {
			return nil, false, err
		}
		changed = retracted
	}

	for _, leaf := range owned {
		if cur, had := dst.Get(leaf.path); had && reflect.DeepEqual(cur, leaf.val) {
			continue
		}
		if err := setLeaf(dst, leaf); err != nil {
			return nil, false, err
		}
		changed = true
	}

	out := dst.Bytes()
	if len(bomPrefix) > 0 {
		// Re-emit the BOM the target came with: dropping it silently would
		// change the file's encoding out from under whatever wrote it.
		out = append(append(make([]byte, 0, len(bomPrefix)+len(out)), bomPrefix...), out...)
	}
	return out, changed, nil
}

// ownedLeaf is one scalar/array position friday owns: the unit both the
// write and the retraction operate on.
type ownedLeaf struct {
	path []string
	val  interface{}
}

// ownedLeaves flattens doc's values at the given top-level keys down to the
// leaves they define, sorted for determinism (map iteration is random, and a
// stable write order keeps repeated splices of the same content
// byte-identical). Keys are taken verbatim, never split on ".", so a TOML
// key that contains a dot addresses correctly.
func ownedLeaves(doc *edit.Document, tables []string) []ownedLeaf {
	out := make([]ownedLeaf, 0, len(tables))
	for _, t := range tables {
		path := []string{t}
		val, ok := doc.Get(path)
		if !ok {
			continue // Tables derived these keys from doc itself; defensive only
		}
		out = appendLeaves(out, path, val)
	}
	sort.Slice(out, func(i, j int) bool { return pathKey(out[i].path) < pathKey(out[j].path) })
	return out
}

// appendLeaves walks val down to its scalar/array leaves. An empty table
// yields no leaf: there is nothing to write, and Set needs a concrete leaf
// to create a section at all.
func appendLeaves(out []ownedLeaf, path []string, val interface{}) []ownedLeaf {
	m, ok := val.(map[string]interface{})
	if !ok {
		return append(out, ownedLeaf{path: path, val: val})
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := make([]string, len(path)+1)
		copy(child, path)
		child[len(path)] = k
		out = appendLeaves(out, child, m[k])
	}
	return out
}

// setLeaf writes one owned leaf into doc. Set replaces an existing leaf's
// value bytes in place, keeping its position and attached comments; it
// deliberately refuses to overwrite an existing table or array of tables,
// which is the one case friday does mean wholesale replacement — the user
// turned a leaf friday owns into a table — so that path deletes first.
func setLeaf(doc *edit.Document, leaf ownedLeaf) error {
	err := doc.Set(leaf.path, leaf.val)
	if err == nil {
		return nil
	}
	if !doc.Delete(leaf.path) {
		return fmt.Errorf("set %q: %w", strings.Join(leaf.path, "."), err)
	}
	if err := doc.Set(leaf.path, leaf.val); err != nil {
		return fmt.Errorf("set %q: %w", strings.Join(leaf.path, "."), err)
	}
	return nil
}

// retract removes the leaves friday wrote last time (prev) that the current
// payload no longer defines, leaving the user's own edits alone: a leaf goes
// only when the target still holds exactly prev's value for it. Reports
// whether anything was deleted.
func retract(dst *edit.Document, prev []byte, owned []ownedLeaf) (bool, error) {
	prev = stripBOM(prev)
	prevDoc, err := edit.Parse(prev)
	if err != nil {
		return false, fmt.Errorf("previous owned state: %w", err)
	}
	prevTables, err := Tables(prev)
	if err != nil {
		return false, fmt.Errorf("previous owned state: %w", err)
	}
	prevLeaves := ownedLeaves(prevDoc, prevTables)
	keep := make(map[string]bool, len(owned))
	for _, leaf := range owned {
		keep[pathKey(leaf.path)] = true
	}

	changed := false
	var deleted [][]string
	for _, leaf := range prevLeaves {
		if keep[pathKey(leaf.path)] {
			continue // still rendered; the write loop refreshes it
		}
		cur, had := dst.Get(leaf.path)
		if !had || !reflect.DeepEqual(cur, leaf.val) {
			continue // gone already, or the user has since made it theirs
		}
		if dst.Delete(leaf.path) {
			changed = true
			deleted = append(deleted, leaf.path)
		}
	}
	return pruneEmptied(dst, deleted) || changed, nil
}

// pruneEmptied deletes the tables retraction just emptied, deepest first, so
// a retracted [mcp_servers.stale] leaves no orphan header behind. Only
// ancestors of a leaf friday actually deleted are considered, so a table
// friday never wrote into is never removed.
func pruneEmptied(doc *edit.Document, deleted [][]string) bool {
	seen := make(map[string]bool, len(deleted))
	var parents [][]string
	for _, path := range deleted {
		for n := len(path) - 1; n >= 1; n-- {
			p := path[:n]
			if key := pathKey(p); !seen[key] {
				seen[key] = true
				parents = append(parents, p)
			}
		}
	}
	sort.Slice(parents, func(i, j int) bool { return len(parents[i]) > len(parents[j]) })
	changed := false
	for _, p := range parents {
		tbl, ok := doc.Get(p)
		if !ok {
			continue
		}
		if m, isTable := tbl.(map[string]interface{}); !isTable || len(m) > 0 {
			continue
		}
		if doc.Delete(p) {
			changed = true
		}
	}
	return changed
}

// pathKey renders a key path as a comparable map key. Quoting each segment
// keeps it injective where a plain "." join is not: a TOML key may itself
// contain a dot ("docs.anthropic.com" in a permissions domain table).
func pathKey(path []string) string {
	var b strings.Builder
	for _, seg := range path {
		b.WriteString(strconv.Quote(seg))
		b.WriteByte('.')
	}
	return b.String()
}
