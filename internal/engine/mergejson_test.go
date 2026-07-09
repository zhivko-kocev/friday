package engine

import (
	"strings"
	"testing"
)

func TestCanonicalizeSortsKeysRecursively(t *testing.T) {
	in := []byte(`{"b":1,"a":{"z":2,"y":3}}`)
	got, err := canonicalize(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": {\n    \"y\": 3,\n    \"z\": 2\n  },\n  \"b\": 1\n}\n"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestCanonicalizeStableAcrossInputOrder(t *testing.T) {
	a, err := canonicalize([]byte(`{"model":"opus","hooks":{"PreToolUse":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalize([]byte(`{"hooks":{"PreToolUse":[]},"model":"opus"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("canonical form differs by input order:\n%s\nvs\n%s", a, b)
	}
}

func TestCanonicalizePreservesArrayOrder(t *testing.T) {
	got, err := canonicalize([]byte(`{"a":[3,1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "[\n    3,\n    1,\n    2\n  ]") {
		t.Errorf("array order not preserved: %s", got)
	}
}

func TestCanonicalizeNumberFidelity(t *testing.T) {
	got, err := canonicalize([]byte(`{"timeout":5,"big":1234567890123456789}`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `"timeout": 5`) {
		t.Errorf("integer mangled: %s", s)
	}
	if !strings.Contains(s, `"big": 1234567890123456789`) {
		t.Errorf("large integer mangled: %s", s)
	}
}

func TestCanonicalizeNoHTMLEscape(t *testing.T) {
	got, err := canonicalize([]byte(`{"command":"echo a && b < c > d"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a && b < c > d") {
		t.Errorf("HTML metacharacters escaped: %s", got)
	}
}

func TestCanonicalizeEmptyIsEmptyObject(t *testing.T) {
	for _, in := range []string{"", "   ", "\r\n\t "} {
		got, err := canonicalize([]byte(in))
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(got) != "{}\n" {
			t.Errorf("%q -> %q, want {}", in, got)
		}
	}
}

func TestCanonicalizeMalformedErrors(t *testing.T) {
	for _, in := range []string{`{`, `{"a":}`, `{"a":1} trailing`, `not json`} {
		if _, err := canonicalize([]byte(in)); err == nil {
			t.Errorf("%q: expected error, got nil", in)
		}
	}
}

func TestMergeEntriesPreservesUnmanagedKeysAndUserHooks(t *testing.T) {
	// The user owns `model`, an unmanaged event (PostToolUse), and one of the
	// PreToolUse entries; friday adds a Bash entry.
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"}],"PostToolUse":[{"matcher":"X"}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	got, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{`"model": "opus"`, `"Read"`, `"PostToolUse"`, `"X"`, `"Bash"`} {
		if !strings.Contains(s, want) {
			t.Errorf("entry-level merge dropped %s:\n%s", want, s)
		}
	}
}

func TestMergeEntriesEmptyTarget(t *testing.T) {
	source := []byte(`{"hooks":{"PreToolUse":[]}}`)
	got, err := mergeEntries(nil, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalize(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestMergeEntriesMalformedTargetErrors(t *testing.T) {
	if _, err := mergeEntries([]byte(`{bad`), []byte(`{"hooks":{}}`), nil); err == nil {
		t.Error("expected error for malformed target")
	}
}

func TestMergeEntriesNonObjectErrors(t *testing.T) {
	if _, err := mergeEntries([]byte(`[1,2,3]`), []byte(`{"hooks":{}}`), nil); err == nil {
		t.Error("expected error for non-object target")
	}
	if _, err := mergeEntries([]byte(`{}`), []byte(`"scalar"`), nil); err == nil {
		t.Error("expected error for non-object source")
	}
}

func TestMergeEntriesIdempotent(t *testing.T) {
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	first, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mergeEntries(first, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("merge not idempotent (would duplicate friday's entry):\n%s\nvs\n%s", first, second)
	}
}

func TestMergeEntriesInSyncEqualsCanonical(t *testing.T) {
	// An already-wired target (user + friday entry) must merge to its own
	// canonical form, so planMergeJSON reports it in-sync.
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash"}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`)
	got, err := mergeEntries(target, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := canonicalize(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(canon) {
		t.Errorf("wired target not in-sync:\n%s\nvs\n%s", got, canon)
	}
}

func TestMergeEntriesRetractsRemovedEventViaPrev(t *testing.T) {
	// friday previously wrote hooks under two events; the store now only defines
	// PreToolUse. prev lets the whole PostToolUse friday entry be retracted, while
	// the user's own PostToolUse entry and PreToolUse entry survive.
	prev := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}],"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"p.sh"}]}]}}`)
	target := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}],"PostToolUse":[{"matcher":"Own"},{"matcher":"Bash","hooks":[{"type":"command","command":"p.sh"}]}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"g.sh"}]}]}}`)
	got, err := mergeEntries(target, source, prev)
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
	got, err := mergeEntries(target, []byte(`{}`), prev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "g.sh") {
		t.Errorf("friday's entry not retracted when the store emptied:\n%s", s)
	}
	if !strings.Contains(s, `"Read"`) || !strings.Contains(s, `"model": "opus"`) {
		t.Errorf("retraction dropped the user's hook or key:\n%s", s)
	}
}

func TestMergeEntriesRemovesStaleEntryViaPrev(t *testing.T) {
	// friday previously wrote a Bash hook running old.sh; the store changed it to
	// new.sh. Without prev the old entry would linger; prev lets it be removed.
	prev := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"old.sh"}]}]}}`)
	target := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"},{"matcher":"Bash","hooks":[{"type":"command","command":"old.sh"}]}]}}`)
	source := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"new.sh"}]}]}}`)
	got, err := mergeEntries(target, source, prev)
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
