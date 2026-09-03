package engine

import (
	"reflect"
	"strings"
	"testing"
)

func TestHookCommandsAcrossDialects(t *testing.T) {
	// Claude/Codex/Antigravity carry the command in "command"; Copilot uses a
	// per-shell "bash" field. HookCommands must surface all of them so the
	// confirm prompt never degrades to a byte count for a real hook.
	cases := map[string]struct {
		src  string
		want string
	}{
		"claude/codex command key": {
			`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"guard.sh"}]}]}}`,
			"guard.sh",
		},
		"copilot bash field": {
			`{"version":1,"hooks":{"preToolUse":[{"type":"command","matcher":"bash","bash":"bash cop.sh --deny-mode copilot-json"}]}}`,
			"bash cop.sh --deny-mode copilot-json",
		},
		"antigravity named group": {
			`{"friday-git-guard":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":"ag.sh"}]}]}}`,
			"ag.sh",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := HookCommands([]byte(tc.src))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("HookCommands = %v, want [%q]", got, tc.want)
			}
		})
	}
}

// canonicalizeJSON decodes in as JSON then re-serializes it through
// marshalCanonical — the same two steps mergeEntries composes internally for
// every top-level key value it (re)writes. It stands in for the old
// whole-file canonicalize helper, which mergeEntries no longer needs now
// that an untouched target's bytes pass through unchanged.
func canonicalizeJSON(t *testing.T, in []byte) []byte {
	t.Helper()
	v, err := decodeJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := marshalCanonical(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMarshalCanonicalSortsKeysRecursively(t *testing.T) {
	got := canonicalizeJSON(t, []byte(`{"b":1,"a":{"z":2,"y":3}}`))
	want := "{\n  \"a\": {\n    \"y\": 3,\n    \"z\": 2\n  },\n  \"b\": 1\n}\n"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMarshalCanonicalStableAcrossInputOrder(t *testing.T) {
	a := canonicalizeJSON(t, []byte(`{"model":"opus","hooks":{"PreToolUse":[]}}`))
	b := canonicalizeJSON(t, []byte(`{"hooks":{"PreToolUse":[]},"model":"opus"}`))
	if string(a) != string(b) {
		t.Errorf("canonical form differs by input order:\n%s\nvs\n%s", a, b)
	}
}

func TestMarshalCanonicalPreservesArrayOrder(t *testing.T) {
	got := canonicalizeJSON(t, []byte(`{"a":[3,1,2]}`))
	if !strings.Contains(string(got), "[\n    3,\n    1,\n    2\n  ]") {
		t.Errorf("array order not preserved: %s", got)
	}
}

func TestMarshalCanonicalNumberFidelity(t *testing.T) {
	got := canonicalizeJSON(t, []byte(`{"timeout":5,"big":1234567890123456789}`))
	s := string(got)
	if !strings.Contains(s, `"timeout": 5`) {
		t.Errorf("integer mangled: %s", s)
	}
	if !strings.Contains(s, `"big": 1234567890123456789`) {
		t.Errorf("large integer mangled: %s", s)
	}
}

func TestMarshalCanonicalNoHTMLEscape(t *testing.T) {
	got := canonicalizeJSON(t, []byte(`{"command":"echo a && b < c > d"}`))
	if !strings.Contains(string(got), "a && b < c > d") {
		t.Errorf("HTML metacharacters escaped: %s", got)
	}
}

func TestDecodeJSONEmptyIsEmptyObject(t *testing.T) {
	for _, in := range []string{"", "   ", "\r\n\t "} {
		got := canonicalizeJSON(t, []byte(in))
		if string(got) != "{}\n" {
			t.Errorf("%q -> %q, want {}", in, got)
		}
	}
}

func TestDecodeJSONRejectsMalformed(t *testing.T) {
	for _, in := range []string{`{`, `{"a":}`, `{"a":1} trailing`, `not json`} {
		if _, err := decodeJSON([]byte(in)); err == nil {
			t.Errorf("%q: expected error, got nil", in)
		}
	}
}

func TestMergeEntriesPreservesUnmanagedKeysAndUserHooks(t *testing.T) {
	// The user owns `model`, an unmanaged event (PostToolUse), and one of the
	// PreToolUse entries; friday adds a Bash entry.
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"}],"PostToolUse":[{"matcher":"X"}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	got, changed, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true (source adds a new PreToolUse entry)")
	}
	s := string(got)
	// model is untouched by the merge (source never mentions it), so it keeps
	// its exact original spelling — no space after the colon, unlike the
	// freshly regenerated hooks value below.
	if !strings.Contains(s, `"model":"opus"`) {
		t.Errorf("untouched key lost its original formatting:\n%s", s)
	}
	for _, want := range []string{`"Read"`, `"PostToolUse"`, `"X"`, `"Bash"`} {
		if !strings.Contains(s, want) {
			t.Errorf("entry-level merge dropped %s:\n%s", want, s)
		}
	}
}

func TestMergeEntriesEmptyTargetSeedsFromSource(t *testing.T) {
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	got, changed, err := mergeEntries(nil, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true (seeding a brand-new file)")
	}
	gotVal, err := decodeJSON(got)
	if err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, got)
	}
	wantVal, err := decodeJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("got %v want %v", gotVal, wantVal)
	}
}

func TestMergeEntriesMalformedTargetErrors(t *testing.T) {
	if _, _, err := mergeEntries([]byte(`{bad`), []byte(`{"hooks":{}}`), nil); err == nil {
		t.Error("expected error for malformed target")
	}
}

func TestMergeEntriesNonObjectErrors(t *testing.T) {
	if _, _, err := mergeEntries([]byte(`[1,2,3]`), []byte(`{"hooks":{}}`), nil); err == nil {
		t.Error("expected error for non-object target")
	}
	if _, _, err := mergeEntries([]byte(`{}`), []byte(`"scalar"`), nil); err == nil {
		t.Error("expected error for non-object source")
	}
}

func TestMergeEntriesIdempotent(t *testing.T) {
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	first, changed1, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed1 {
		t.Error("changed = false on first merge, want true")
	}
	second, changed2, err := mergeEntries(first, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("changed = true on second merge, want false (already wired)")
	}
	if string(first) != string(second) {
		t.Errorf("merge not idempotent (would duplicate friday's entry):\n%s\nvs\n%s", first, second)
	}
}

func TestMergeEntriesInSyncReportsUnchanged(t *testing.T) {
	// An already-wired target (user + friday entry) must report changed=false
	// so planMergeJSON marks it in-sync, and must come back byte-identical
	// since nothing needed patching.
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash"}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	got, changed, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("changed = true, want false (target already carries friday's entry)")
	}
	if string(got) != string(target) {
		t.Errorf("unchanged merge rewrote bytes:\n%s\nvs original\n%s", got, target)
	}
}

func TestMergeEntriesPreservesCommentsAndFormatting(t *testing.T) {
	// The whole point of the hujson rewrite: a hand-commented, oddly-indented
	// target (Copilot's JSONC settings.json is exactly this shape) keeps its
	// comment and its untouched key's exact layout; only the touched key's
	// value is regenerated.
	target := []byte("{\n  // keep this note\n  \"model\":   \"opus\"  ,\n  \"hooks\": {\"PreToolUse\":[{\"matcher\":\"Read\"}]}\n}\n")
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	got, changed, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	s := string(got)
	if !strings.Contains(s, "// keep this note") {
		t.Errorf("comment lost:\n%s", s)
	}
	if !strings.Contains(s, `"model":   "opus"  ,`) {
		t.Errorf("untouched key's original spacing lost:\n%s", s)
	}
	if !strings.Contains(s, `"Bash"`) || !strings.Contains(s, `"Read"`) {
		t.Errorf("touched key lost content:\n%s", s)
	}
}

func TestMergeEntriesInsertsNewKeyIntoNonEmptyTarget(t *testing.T) {
	// Adding a top-level key the target never had (as opposed to replacing
	// one it already has) is the one layout rough edge topLevelPatch's doc
	// comment calls out — assert content correctness, not layout.
	target := []byte(`{"model":"opus"}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	got, changed, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	gotVal, err := decodeJSON(got)
	if err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, got)
	}
	want := map[string]any{
		"model": "opus",
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{"matcher": "Bash"}},
		},
	}
	if !reflect.DeepEqual(gotVal, want) {
		t.Errorf("got %v want %v", gotVal, want)
	}
}

func TestMergeEntriesRetractsRemovedEventViaPrev(t *testing.T) {
	// friday previously wrote hooks under two events; the store now only defines
	// PreToolUse. prev lets the whole PostToolUse friday entry be retracted, while
	// the user's own PostToolUse entry and PreToolUse entry survive.
	prev := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}],"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"p.sh"}]}]}}`)
	target := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}],"PostToolUse":[{"matcher":"Own"},{"matcher":"Bash","hooks":[{"type":"command","command":"p.sh"}]}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}]}}`)
	got, _, err := mergeEntries(target, source, prev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "p.sh") {
		t.Errorf("friday's retracted PostToolUse entry lingered:\n%s", s)
	}
	if !strings.Contains(s, `"Own"`) || !strings.Contains(s, `"Read"`) || !strings.Contains(s, "g.sh") {
		t.Errorf("retraction dropped a user or current entry:\n%s", s)
	}
}

func TestMergeEntriesEmptySourceRetractsAll(t *testing.T) {
	// The store's hooks.json was emptied; friday's entries go, the user's stay.
	prev := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}]}}`)
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}]}}`)
	got, _, err := mergeEntries(target, []byte(`{}`), prev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "g.sh") {
		t.Errorf("friday's entry not retracted when the store emptied:\n%s", s)
	}
	// model is untouched throughout (neither source nor prev mentions it), so
	// it keeps its original no-space-after-colon spelling.
	if !strings.Contains(s, `"Read"`) || !strings.Contains(s, `"model":"opus"`) {
		t.Errorf("retraction dropped the user's hook or key:\n%s", s)
	}
}

func TestMergeEntriesRemovesStaleEntryViaPrev(t *testing.T) {
	// friday previously wrote a Bash hook running old.sh; the store changed it to
	// new.sh. Without prev the old entry would linger; prev lets it be removed.
	prev := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"old.sh"}]}]}}`)
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash","hooks":[{"type":"command","command":"old.sh"}]}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"new.sh"}]}]}}`)
	got, _, err := mergeEntries(target, source, prev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "old.sh") {
		t.Errorf("stale friday entry not removed:\n%s", s)
	}
	if !strings.Contains(s, "new.sh") || !strings.Contains(s, `"Read"`) {
		t.Errorf("merge lost the new entry or the user entry:\n%s", s)
	}
}

// An explicit JSON null is how a hand-authored source says "unset this key".
// Writing the result through instead leaves a literal `"key": null` — or, once
// friday's own entries under it are stripped, an empty `{}` shell — inside a
// file friday only co-owns.
func TestMergeEntriesExplicitNullDropsTheKey(t *testing.T) {
	cases := map[string]struct {
		target, source, prev []byte
		absent               []string
		present              []string
	}{
		"null retracts a key friday owns": {
			target: []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`),
			source: []byte(`{"hooks":null}`),
			prev:   []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`),
			absent: []string{"hooks", "null"},
			// The user's unmanaged key is untouched either way.
			present: []string{`"model"`},
		},
		"null for a brand-new key writes nothing": {
			target:  []byte(`{"model":"opus"}`),
			source:  []byte(`{"newkey":null}`),
			absent:  []string{"newkey", "null"},
			present: []string{`"model"`},
		},
		"null keeps the user's own entries under the key": {
			target:  []byte(`{"hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash"}]}}`),
			source:  []byte(`{"hooks":null}`),
			prev:    []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`),
			absent:  []string{`"Bash"`, "null"},
			present: []string{`"Read"`},
		},
		"nested null retracts only that position": {
			target: []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}],"PostToolUse":[{"matcher":"Edit"}]}}`),
			source: []byte(`{"hooks":{"PreToolUse":null}}`),
			prev:   []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`),
			absent: []string{"PreToolUse", "null"},
			// The sibling hook group survives.
			present: []string{"PostToolUse", `"Edit"`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, _, err := mergeEntries(c.target, c.source, c.prev)
			if err != nil {
				t.Fatal(err)
			}
			s := string(got)
			for _, want := range c.absent {
				if strings.Contains(s, want) {
					t.Errorf("result still contains %q:\n%s", want, s)
				}
			}
			for _, want := range c.present {
				if !strings.Contains(s, want) {
					t.Errorf("result lost %q:\n%s", want, s)
				}
			}
		})
	}
}

// The splice preserves untouched bytes verbatim but serializes a changed key
// through an LF-only encoder, so a CRLF-authored target must be restored to its
// own convention or one file ends up carrying both.
func TestMergeEntriesKeepsTargetLineEndings(t *testing.T) {
	target := []byte("{\r\n  // the user's own note\r\n  \"model\": \"opus\",\r\n  \"hooks\": {\r\n    \"PreToolUse\": [{\"matcher\": \"Read\"}]\r\n  }\r\n}\r\n")
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"guard.sh"}]}]}}`)
	got, changed, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("merge reported no change; the CRLF assertion below would be vacuous")
	}
	if bare := countBareLF(got); bare != 0 {
		t.Errorf("%d bare LF breaks in a CRLF target — mixed line endings:\n%q", bare, got)
	}
	if !strings.Contains(string(got), "guard.sh") || !strings.Contains(string(got), "the user's own note") {
		t.Errorf("merge lost the new hook or the user's comment:\n%s", got)
	}
}

// countBareLF counts LF bytes that are not part of a CRLF pair.
func countBareLF(b []byte) int {
	n := 0
	for i := range b {
		if b[i] == '\n' && (i == 0 || b[i-1] != '\r') {
			n++
		}
	}
	return n
}

func TestMergeReplacePreservesSourceKeyOrder(t *testing.T) {
	// The whole point of mergeReplace: a hand-ordered pattern object (the
	// shape OpenCode's "permission" key needs — precedence is last-object-
	// key-wins, not most-specific-wins) must survive byte for byte, which
	// mergeEntries' decode-merge-remarshal path cannot guarantee since
	// encoding/json always sorts map keys on marshal.
	source := []byte(`{"permission":{"bash":{"*":"ask","git *":"allow","rm *":"deny"}}}`)
	got, changed, err := mergeReplace(nil, source)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true (seeding a brand-new file)")
	}
	// Key order must match source exactly — "*" before "git *" before "rm *".
	s := string(got)
	iStar := strings.Index(s, `"*"`)
	iGit := strings.Index(s, `"git *"`)
	iRm := strings.Index(s, `"rm *"`)
	if !(iStar < iGit && iGit < iRm) {
		t.Errorf("pattern order not preserved: %s", s)
	}
}

func TestMergeReplaceAtomicallyReplacesWholeKeyNotUnion(t *testing.T) {
	// Unlike mergeEntries, mergeReplace does not union nested content: a
	// user's own hand-added pattern under "permission.bash" is not
	// preserved, because render-sourced content has exactly one owner
	// (permissions.yaml) that is fully regenerated on every push.
	target := []byte(`{"permission":{"bash":{"npm test":"allow"}}}`)
	source := []byte(`{"permission":{"bash":{"git *":"allow"}}}`)
	got, changed, err := mergeReplace(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	s := string(got)
	if strings.Contains(s, "npm test") {
		t.Errorf("mergeReplace must fully replace the owned key, not union it: %s", s)
	}
	if !strings.Contains(s, "git *") {
		t.Errorf("new content missing: %s", s)
	}
}

func TestMergeReplacePreservesSiblingKeysAndComments(t *testing.T) {
	target := []byte("{\n  // keep\n  \"model\": \"opus\",\n  \"permission\": {\"bash\": {\"npm test\": \"allow\"}}\n}\n")
	source := []byte(`{"permission":{"bash":{"git *":"allow"}}}`)
	got, _, err := mergeReplace(target, source)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "// keep") || !strings.Contains(s, `"model": "opus"`) {
		t.Errorf("untouched sibling key/comment lost: %s", s)
	}
}

func TestMergeReplaceInSyncWhenIdentical(t *testing.T) {
	source := []byte(`{"permission":{"bash":{"git *":"allow"}}}`)
	first, changed1, err := mergeReplace(nil, source)
	if err != nil {
		t.Fatal(err)
	}
	if !changed1 {
		t.Error("changed = false on first write, want true")
	}
	second, changed2, err := mergeReplace(first, source)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("changed = true on re-render of identical content, want false")
	}
	if string(first) != string(second) {
		t.Errorf("re-render of identical source rewrote bytes:\n%s\nvs\n%s", first, second)
	}
}

// TestMergeReplaceInSyncWhenIdenticalWithSiblingKey is the regression case
// for a bug where changed was computed by comparing the whole target object
// to the whole source object: a target that legitimately carries a sibling
// key source never mentions (opencode.json's "mcp" key alongside
// "permission", written by a separate render rule) could then never be
// DeepEqual to source, so re-rendering identical content always reported
// changed=true and drove a spurious ActionUpdate on every push.
func TestMergeReplaceInSyncWhenIdenticalWithSiblingKey(t *testing.T) {
	target := []byte(`{"mcp":{"github":{"type":"local"}},"permission":{"bash":{"git *":"allow"}}}`)
	source := []byte(`{"permission":{"bash":{"git *":"allow"}}}`)
	merged, changed, err := mergeReplace(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("changed = true, want false — source's owned key already matches, sibling \"mcp\" is irrelevant to this comparison:\n%s", merged)
	}
	if string(merged) != string(target) {
		t.Errorf("in-sync merge rewrote bytes:\n%s\nvs original\n%s", merged, target)
	}
}

func TestMergeReplaceRejectsNonObjectSource(t *testing.T) {
	if _, _, err := mergeReplace([]byte(`{}`), []byte(`[1,2,3]`)); err == nil {
		t.Error("expected error for non-object source")
	}
}
