package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
	"github.com/zhivko-kocev/friday/internal/textnorm"
)

// mergeEntries deep-merges source into target at the entry level. It returns
// target's bytes patched with next's changed top-level keys — everything
// else, including comments and hand formatting, survives byte-for-byte
// (hujson's Parse+Pack round-trips untouched input exactly), because
// merge-json's own granularity is already "one whole key at a time": a key
// friday touches is fully regenerated either way, so there is nothing of the
// user's left to preserve inside it. changed reports whether the merge
// altered anything, sparing the caller a second parse to detect a no-op push.
//
// mergeValue's semantics are unchanged: objects union keys (a key the source
// never mentions stays untouched), arrays keep every target element not
// deep-equal to a source-or-prev element then append the source's. target
// may be empty; both inputs, when non-empty, must be JSON objects.
func mergeEntries(target, source, prev []byte) (merged []byte, changed bool, err error) {
	orig, err := decodeObject(target)
	if err != nil {
		return nil, false, fmt.Errorf("target: %w", err)
	}
	// mergeValue mutates its first argument in place and returns it, so a
	// second, independent decode is what lets orig above stand for "before"
	// once mergeValue below turns tv into "after".
	tv, err := decodeObject(target)
	if err != nil {
		return nil, false, fmt.Errorf("target: %w", err)
	}
	sv, err := decodeObject(source)
	if err != nil {
		return nil, false, fmt.Errorf("source: %w", err)
	}
	var pv map[string]any
	if len(bytes.TrimSpace(prev)) > 0 {
		if pv, err = decodeObject(prev); err != nil {
			return nil, false, fmt.Errorf("previous owned state: %w", err)
		}
	}
	next := mergeValue(tv, sv, pv).(map[string]any)
	changed = !reflect.DeepEqual(orig, next)

	var changedBytes map[string][]byte
	var removed []string
	if changed {
		changedBytes = make(map[string][]byte)
		for key, val := range next {
			if prior, ok := orig[key]; ok && reflect.DeepEqual(prior, val) {
				continue
			}
			b, err := marshalCanonical(val)
			if err != nil {
				return nil, false, fmt.Errorf("marshal %q: %w", key, err)
			}
			changedBytes[key] = bytes.TrimRight(b, "\n")
		}
		for key := range orig {
			if _, ok := next[key]; !ok {
				removed = append(removed, key)
			}
		}
	}
	merged, err = spliceTopLevel(target, changedBytes, removed)
	if err != nil {
		return nil, false, err
	}
	return merged, changed, nil
}

// mergeReplace atomically replaces target's top-level keys with source's,
// verbatim — no recursive union (mergeValue/mergeEntries), no prev. It exists
// for render-sourced content (internal/render, projecting internal/spec's
// mcp.yaml/permissions.yaml) that has exactly one canonical owner and is
// fully regenerated from that owner on every push: there is nothing of the
// user's *inside* a key it owns to preserve — only outside it, which
// spliceTopLevel's untouched-sibling-key behaviour already covers.
//
// Splicing source's raw bytes — never decoding them into a map[string]any
// and re-marshaling, the way mergeEntries does — is what lets a renderer's
// own key order survive into the file untouched. That is load-bearing for
// OpenCode's "permission" object, whose native format resolves overlapping
// rules by declaration order: encoding/json always sorts map keys on
// marshal, which would silently invert friday's deny-beats-ask-beats-allow
// precedence the first time this key round-tripped through mergeEntries.
func mergeReplace(target, source []byte) (merged []byte, changed bool, err error) {
	orig, err := decodeObject(target)
	if err != nil {
		return nil, false, fmt.Errorf("target: %w", err)
	}
	sv, err := decodeObject(source) // validates source is a JSON object
	if err != nil {
		return nil, false, fmt.Errorf("source: %w", err)
	}
	raw, err := decodeTopLevelRaw(source)
	if err != nil {
		return nil, false, fmt.Errorf("source: %w", err)
	}

	// changed is per-key, not whole-object: target legitimately carries
	// sibling keys source never mentions (opencode.json's "mcp" alongside
	// "permission"), so target can never be wholly DeepEqual to source even
	// when every key source actually owns is already in sync.
	changedBytes := make(map[string][]byte, len(sv))
	for key, val := range sv {
		if prior, ok := orig[key]; ok && reflect.DeepEqual(prior, val) {
			continue
		}
		changedBytes[key] = bytes.TrimSpace(raw[key])
	}
	changed = len(changedBytes) > 0
	merged, err = spliceTopLevel(target, changedBytes, nil)
	if err != nil {
		return nil, false, err
	}
	return merged, changed, nil
}

// spliceTopLevel applies changed's raw JSON bytes onto target's original
// bytes as an RFC 6902 patch and removes every key named in removed —
// mergeEntries and mergeReplace are its only two callers, differing only in
// how they decide what changed and how they serialize a changed value's
// bytes. Untouched keys, comments, and hand formatting survive byte for
// byte (hujson's Parse+Pack round-trips untouched input exactly); a changed
// key is spliced using its caller-supplied bytes verbatim, never
// re-marshaled, so a caller that hands it an already-ordered JSON object
// (mergeReplace) gets that order preserved in the file.
//
// Building the patch by hand rather than via encoding/json.Marshal matters
// for the same reason: encoding/json unconditionally compacts nested
// marshaled content (json.RawMessage included), which would flatten a
// changed value's own pretty-printed or caller-ordered shape before hujson
// ever sees it. This comes at the cost of one known, accepted rough edge: a
// brand-new key appended to an already non-empty object lands right after
// the previous member with no separating newline, because that newline is
// the leading whitespace of the new member itself, a position RFC 6902
// "add" has no vocabulary to set. Every subsequent push replaces that key's
// value in place — the clean, common case — and never re-triggers this edge.
func spliceTopLevel(target []byte, changed map[string][]byte, removed []string) ([]byte, error) {
	bomPrefix, raw := trimBOM(target)
	fresh := len(bytes.TrimSpace(raw)) == 0
	if fresh {
		raw = []byte("{}")
	}
	doc, err := hujson.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse target: %w", err)
	}
	if len(changed) > 0 || len(removed) > 0 {
		patch, err := buildPatch(changed, removed)
		if err != nil {
			return nil, err
		}
		if err := doc.Patch(patch); err != nil {
			return nil, fmt.Errorf("patch: %w", err)
		}
	}
	if fresh {
		// A brand-new file has no hand formatting to preserve — lay it out
		// readably instead of leaving Patch's compact insertion order.
		doc.Format()
	}
	out := doc.Pack()
	// Restore the convention the target arrived with. Pack preserves untouched
	// bytes verbatim, but a spliced fragment comes from an encoder that only
	// emits LF, so a CRLF-authored settings.json would otherwise end up
	// carrying both endings — one file, two conventions, and a diff that looks
	// like friday rewrote lines it never touched.
	if textnorm.CRLFDominant(raw) {
		out = textnorm.CRLF(out)
	}
	if len(bomPrefix) > 0 {
		// Re-emit the BOM the target came with: dropping it silently would
		// change the file's encoding out from under whatever wrote it.
		out = append(append(make([]byte, 0, len(bomPrefix)+len(out)), bomPrefix...), out...)
	}
	return out, nil
}

// buildPatch returns an RFC 6902 JSON patch (as hujson-parseable bytes) that
// adds/replaces every key in changed using its already-serialized bytes
// verbatim, and removes every key in removed. Callers only reach here when
// there is at least one op.
func buildPatch(changed map[string][]byte, removed []string) ([]byte, error) {
	type op struct {
		key   string
		value []byte // nil for "remove"
	}
	ops := make([]op, 0, len(changed)+len(removed))
	for key, value := range changed {
		ops = append(ops, op{key: key, value: value})
	}
	for _, key := range removed {
		ops = append(ops, op{key: key})
	}
	// Deterministic order: map iteration above is random, and a stable
	// left-to-right key order keeps a freshly created file's rendering
	// reproducible across runs.
	sort.Slice(ops, func(i, j int) bool { return ops[i].key < ops[j].key })

	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, o := range ops {
		if i > 0 {
			buf.WriteByte(',')
		}
		path, err := json.Marshal("/" + jsonPointerEscape(o.key))
		if err != nil {
			return nil, err
		}
		if o.value == nil {
			fmt.Fprintf(&buf, `{"op":"remove","path":%s}`, path)
		} else {
			fmt.Fprintf(&buf, `{"op":"add","path":%s,"value":%s}`, path, o.value)
		}
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// jsonPointerEscape escapes a single RFC 6901 reference token. "~" is
// escaped first so it does not double-escape the "~" that "/"'s own escape
// introduces.
func jsonPointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

// mergeValue merges source into target recursively, returning the merged
// value, or nil to tell the caller to drop the key entirely. prev (may be
// nil) carries friday's previously-written value at the same position, so a
// key or element friday used to write but the source no longer does is
// retracted instead of left stale.
func mergeValue(target, source, prev any) any {
	tm, tok := target.(map[string]any)
	sm, sok := source.(map[string]any)
	pm, pok := prev.(map[string]any)
	if tok && (sok || pok) {
		// Source keys are added/refreshed; user keys the source never mentions
		// stay untouched.
		for k, sv := range sm {
			merged := mergeValue(tm[k], sv, pm[k])
			// An explicit JSON null means "unset this key" — the same signal a
			// prev-only position uses. Writing the result through instead would
			// leave a literal `"key": null`, or the empty shell left behind once
			// friday's own entries under it were stripped, in a file friday only
			// co-owns. A non-empty result means the user has entries there too:
			// friday's part goes, theirs stays.
			if sv == nil && (merged == nil || isEmptyContainer(merged)) {
				delete(tm, k)
				continue
			}
			tm[k] = merged
		}
		// A key friday wrote before (prev) but the source dropped: strip friday's
		// entries under it, and remove the key entirely if nothing else remains.
		for k := range pm {
			if _, inSource := sm[k]; inSource {
				continue
			}
			merged := mergeValue(tm[k], nil, pm[k])
			if merged == nil || isEmptyContainer(merged) {
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
	// A prev-only scalar position: retract it only when the target still
	// holds exactly what friday recorded writing there. Anything else is the
	// user's own edit and stays. Without the retraction, dropping an MCP
	// server from mcp.yaml would delete only its array-valued fields and
	// leave a half-entry the agent still loads — a bare `npx` with no args.
	if source == nil {
		if reflect.DeepEqual(target, prev) {
			return nil
		}
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

// utf8BOM is the byte order mark PowerShell 5.1's Set-Content/Out-File
// writes by default. Neither hujson nor go-toml/edit accepts one, so
// without stripping it a Windows user who ever edits settings.json or
// config.toml from the shell gets every merge rule for that target
// permanently skipped, behind a parser error that never mentions a BOM.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// trimBOM splits a leading UTF-8 BOM off data, returning it separately so a
// writer can put it back on the content it produces.
func trimBOM(data []byte) (bom, rest []byte) {
	if bytes.HasPrefix(data, utf8BOM) {
		return data[:len(utf8BOM)], data[len(utf8BOM):]
	}
	return nil, data
}

// stripBOM drops a leading UTF-8 BOM, for input friday only reads.
func stripBOM(data []byte) []byte {
	_, rest := trimBOM(data)
	return rest
}

// standardize normalizes data for decoding: CRLF folded, a leading UTF-8 BOM
// and surrounding whitespace removed, and HuJSON (JWCC — comments and
// trailing commas) reduced to strict JSON, so a hand-commented target
// (Copilot's JSONC settings.json) decodes exactly like plain JSON. friday
// only ever needs the *values* here; the original bytes for output are
// handled separately, by spliceTopLevel patching the target directly. Empty
// input yields nil, which every caller reads as an empty object so an absent
// settings.json merges cleanly.
func standardize(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(stripBOM(textnorm.Newlines(data)))
	if len(trimmed) == 0 {
		return nil, nil
	}
	// hujson.Standardize aliases and mutates its input in place (it blanks
	// comments with spaces to preserve byte offsets) — copy first so it can
	// never reach back into the caller's own buffer. Without this, a second
	// decode of the same target bytes (mergeEntries decodes target twice) or
	// a later hujson.Parse of the original bytes would see comments already
	// blanked out from under it.
	return hujson.Standardize(append([]byte(nil), trimmed...))
}

// decodeJSON parses a single JSON or HuJSON value. Trailing data after the
// value is rejected.
func decodeJSON(data []byte) (any, error) {
	std, err := standardize(data)
	if err != nil {
		return nil, err
	}
	if len(std) == 0 {
		return map[string]any{}, nil
	}
	return decodeStandardized(std)
}

func decodeStandardized(std []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(std))
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

// decodeObject parses JSON that must be an object at the top level, and
// rejects one declaring the same top-level key twice: every JSON reader
// resolves that to the last occurrence while spliceTopLevel's RFC 6902
// patch targets the first, so friday's write would land in the copy nobody
// reads — reported as a change, recorded as owned state, and re-offered on
// every push, a confirmation prompt that can never be satisfied.
func decodeObject(data []byte) (map[string]any, error) {
	std, err := standardize(data)
	if err != nil {
		return nil, err
	}
	if len(std) == 0 {
		return map[string]any{}, nil
	}
	v, err := decodeStandardized(std)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object")
	}
	dup, err := duplicateTopLevelKey(std)
	if err != nil {
		return nil, err
	}
	if dup != "" {
		return nil, fmt.Errorf("duplicate top-level key %q — remove one of them, then push", dup)
	}
	return obj, nil
}

// duplicateTopLevelKey returns the first key std's top-level object declares
// twice, or "" when it declares none twice. Nested duplicates are none of
// friday's business: only the top level is spliced by pointer.
func duplicateTopLevelKey(std []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(std))
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return "", nil
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		key, ok := tok.(string)
		if !ok {
			return "", fmt.Errorf("expected an object key, got %v", tok)
		}
		if seen[key] {
			return key, nil
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return "", err
		}
	}
	return "", nil
}

// decodeTopLevelRaw parses data's top-level object, keeping each member's
// value as its own undecoded JSON bytes rather than a decoded any — see
// mergeReplace, whose whole point is splicing a value in without the
// decode/re-marshal round trip that would erase the caller's own key order.
func decodeTopLevelRaw(data []byte) (map[string]json.RawMessage, error) {
	std, err := standardize(data)
	if err != nil {
		return nil, err
	}
	if len(std) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(std, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// hookOwningKeys are the top-level keys an agent reads hook wiring from. Every
// merge target that can execute a hook — claude's and gemini's settings.json,
// codex's hooks.json, copilot's own guard file — reads it from "hooks"; nothing
// declarative friday renders (mcpServers, permissions, model, statusLine) does.
// A TOML target has no such key: codex's hooks live in hooks.json, not
// config.toml, so a [hooks] table there would never run.
var hookOwningKeys = []string{"hooks"}

// declaresHookWiring reports whether a merge payload sets a key the agent
// executes hooks from. It exists because consent cannot be decided from the
// rule's source path alone: a settings/<adapter>.json passthrough merges into
// the very same settings.json a gated hooks/ rule owns, so a "hooks" key
// smuggled through the passthrough would install shell commands unprompted.
// Best-effort by design — a payload this cannot parse (a TOML fragment, an
// unreadable blob) is not a JSON hook declaration, and the source-path signal
// still covers every hooks/ rule.
func declaresHookWiring(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	top, err := decodeTopLevelRaw(payload)
	if err != nil {
		return false
	}
	for _, key := range hookOwningKeys {
		if _, ok := top[key]; ok {
			return true
		}
	}
	return false
}

// hookCommandKeys are the JSON keys that carry a shell command in the hook
// dialects friday wires: "command" (Claude, Codex, Gemini), and the
// per-shell "bash"/"powershell" fields Copilot uses. HookCommands surfaces all
// of them so the confirm prompt shows the real command for every agent, not a
// byte count.
var hookCommandKeys = []string{"command", "bash", "powershell"}

// HookCommands extracts every shell-command string from a hooks JSON blob so a
// confirmer can show exactly what a merge-json write would install. It decodes
// through decodeJSON, not encoding/json: the merge path accepts JWCC, so a
// store author who comments their hooks.json would otherwise silently
// degrade the consent prompt to a byte count while it still claims the write
// "runs arbitrary shell on tool use". Best-effort: unparseable input yields
// nil and the caller falls back to that byte count.
func HookCommands(src []byte) []string {
	root, err := decodeJSON(src)
	if err != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for _, key := range hookCommandKeys {
				if c, ok := t[key].(string); ok {
					out = append(out, c)
				}
			}
			// Recurse in sorted key order so the confirm/doctor output is stable
			// for a hand-written file carrying several commands.
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
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
