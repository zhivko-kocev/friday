package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/zhivko-kocev/friday/internal/textnorm"
)

// canonicalize parses JSON and re-serializes it deterministically so two
// semantically equal documents compare byte-equal: object keys are sorted
// (encoding/json orders map keys, recursively), array order is preserved (hook
// order is significant), numbers are kept verbatim (UseNumber — no float64
// rounding of timeouts or large integers), HTML metacharacters are left
// unescaped (&, <, > appear literally in commands and URLs), with two-space
// indent and a single trailing newline.
//
// Empty or whitespace-only input canonicalizes to "{}". Non-empty but invalid
// JSON returns an error: callers must never fall back to {} on a parse failure,
// which would silently wipe a co-owned file's other keys.
func canonicalize(data []byte) ([]byte, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return nil, err
	}
	return marshalCanonical(v)
}

// mergeEntries deep-merges source into target at the entry level and returns
// the canonical serialization. Objects union their keys (target-only keys —
// e.g. the user's `model` or an unmanaged hook event — are preserved, shared
// keys recurse). Arrays keep every target element that is NOT deep-equal to a
// source element, then append the source elements: friday's own entries are
// refreshed without duplicating, and the user's own entries survive. With
// prev=nil this is the stateless core — idempotent and user-preserving, but it
// can only add/refresh, never retract a friday entry the source dropped.
// `prev`, when non-nil, is friday's previously-written source (from the owned
// cache): any target element deep-equal to a prev element is also dropped, and a
// key friday wrote before but the source no longer has is stripped and removed
// if empty — so a since-changed OR since-removed hook is cleaned up. target may
// be empty ("{}"); both inputs, when non-empty, must be JSON objects.
func mergeEntries(target, source, prev []byte) ([]byte, error) {
	tv, err := decodeObject(target)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	sv, err := decodeObject(source)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	var pv map[string]any
	if len(bytes.TrimSpace(prev)) > 0 {
		if pv, err = decodeObject(prev); err != nil {
			return nil, fmt.Errorf("previous owned state: %w", err)
		}
	}
	return marshalCanonical(mergeValue(tv, sv, pv))
}

// mergeValue merges source into target recursively. prev (may be nil) carries
// friday's previously-written value at the same position, so array merges can
// drop friday's stale entries — including a whole key friday used to write but
// the source no longer does.
func mergeValue(target, source, prev any) any {
	tm, tok := target.(map[string]any)
	sm, sok := source.(map[string]any)
	pm, pok := prev.(map[string]any)
	if tok && (sok || pok) {
		// Source keys are added/refreshed; user keys the source never mentions
		// stay untouched.
		for k := range sm {
			tm[k] = mergeValue(tm[k], sm[k], pm[k])
		}
		// A key friday wrote before (prev) but the source dropped: strip friday's
		// entries under it, and remove the key entirely if nothing else remains.
		for k := range pm {
			if _, inSource := sm[k]; inSource {
				continue
			}
			merged := mergeValue(tm[k], nil, pm[k])
			if isEmptyContainer(merged) {
				delete(tm, k)
			} else {
				tm[k] = merged
			}
		}
		return tm
	}
	if ta, ok := target.([]any); ok {
		sa, _ := source.([]any)
		pa, _ := prev.([]any)
		out := make([]any, 0, len(ta)+len(sa))
		for _, el := range ta {
			// Drop friday's entries — the ones it is re-adding now (source) and
			// the ones it wrote before (prev) — so nothing duplicates and a
			// since-changed or since-removed entry is dropped. User entries match
			// neither and survive.
			if containsEqual(sa, el) || containsEqual(pa, el) {
				continue
			}
			out = append(out, el)
		}
		return append(out, sa...)
	}
	// A prev-only position that isn't a container is not something friday owns
	// element-wise — leave the user's value in place rather than nil it.
	if source == nil {
		return target
	}
	return source
}

func isEmptyContainer(v any) bool {
	switch t := v.(type) {
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

func containsEqual(list []any, v any) bool {
	for _, el := range list {
		if reflect.DeepEqual(el, v) {
			return true
		}
	}
	return false
}

// decodeJSON parses a single JSON value, tolerating CRLF and leading/trailing
// whitespace. Empty input decodes to an empty object so an absent settings.json
// merges cleanly. Trailing data after the value is rejected.
func decodeJSON(data []byte) (any, error) {
	trimmed := bytes.TrimSpace(textnorm.Newlines(data))
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("unexpected trailing data after JSON value")
	}
	return v, nil
}

// decodeObject parses JSON that must be an object at the top level.
func decodeObject(data []byte) (map[string]any, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return obj, nil
}

// HookCommands extracts every "command" string from a hooks JSON blob so a
// confirmer can show exactly what a merge-json write would install. Best-effort:
// unparseable input yields nil and the caller falls back to a byte count.
func HookCommands(src []byte) []string {
	var root any
	if err := json.Unmarshal(src, &root); err != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if c, ok := t["command"].(string); ok {
				out = append(out, c)
			}
			for _, val := range t {
				walk(val)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(root)
	return out
}

func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
