package engine

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestTablesReturnsTopLevelKeys(t *testing.T) {
	// Ownership is per-leaf inside a top-level key (Splice's job), so Tables
	// only has to name the root entries the payload defines — whatever their
	// shape.
	cases := map[string]struct {
		payload string
		want    []string
	}{
		"named collection": {
			"[mcp_servers.github]\ncommand = \"npx\"\n\n[mcp_servers.linear]\nurl = \"https://mcp.linear.app\"\n",
			[]string{"mcp_servers"},
		},
		"table mixing a scalar with nested tables": {
			"[permissions.friday]\ndescription = \"x\"\n\n[permissions.friday.network]\nenabled = true\n",
			[]string{"permissions"},
		},
		"flat table": {"[tui]\nsome_setting = true\n", []string{"tui"}},
		"bare scalar root key": {
			"model = \"gpt-5-codex\"\n",
			[]string{"model"},
		},
		"codex's own shape — bare scalars alongside a table": {
			"model = \"x\"\nmodel_provider = \"y\"\n\n[tui]\ntheme = \"dark\"\n",
			[]string{"model", "model_provider", "tui"},
		},
		"empty payload": {"", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Tables([]byte(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(got)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestTablesMalformedPayloadErrors(t *testing.T) {
	if _, err := Tables([]byte("not = valid = toml = =")); err == nil {
		t.Error("expected error for malformed TOML")
	}
}

// TestSpliceKeepsUnmanagedKeysInsideOwnedTables is the regression case for a
// whole-table claim that silently destroyed user configuration on the very
// first push: render.defaultsCodex emits only [tui].theme, but [tui] was
// deleted and rewritten wholesale, taking Codex's documented notifications
// and notification_method with it.
func TestSpliceKeepsUnmanagedKeysInsideOwnedTables(t *testing.T) {
	existing := []byte(`# ~/.codex/config.toml -- hand maintained
model = "gpt-5"  # my pinned model
approval_policy = "on-request"

# UI preferences
[tui]
notifications = true
theme = "light"
notification_method = "osc9"

[analytics]
enabled = true
endpoint = "https://my.collector"

# note for other
[other]
key = "val"
`)
	payload := []byte("model = \"gpt-5-codex\"\nmodel_reasoning_effort = \"high\"\n\n[tui]\ntheme = \"dark\"\n\n[analytics]\nenabled = false\n")
	tables, err := Tables(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := Splice(existing, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	s := string(got)
	for _, want := range []string{
		"notifications = true", `notification_method = "osc9"`,
		`endpoint = "https://my.collector"`,
		`approval_policy = "on-request"`, `key = "val"`,
		"# ~/.codex/config.toml -- hand maintained", "# my pinned model",
		"# UI preferences", "# note for other",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("unmanaged content lost — %q is gone:\n%s", want, s)
		}
	}
	if strings.Contains(s, "light") || strings.Contains(s, "gpt-5\"") {
		t.Errorf("stale owned value survived:\n%s", s)
	}
	for _, want := range []string{"gpt-5-codex", "model_reasoning_effort", "dark", "enabled = false"} {
		if !strings.Contains(s, want) {
			t.Errorf("owned value not written — %q missing:\n%s", want, s)
		}
	}
}

// TestSpliceRetractionLeavesHandEditedValueAlone is the regression case for a
// retraction that deleted purely by path membership: the user's own theme and
// notifications were destroyed (the whole file emptied) when the payload
// stopped defining [tui]. Retraction is content-based — friday removes only
// what the target still holds exactly as friday recorded writing it.
func TestSpliceRetractionLeavesHandEditedValueAlone(t *testing.T) {
	existing := []byte("[tui]\ntheme = \"solarized-my-own-choice\"\nnotifications = true\n")
	prev := []byte("[tui]\ntheme = \"dark\"\n") // friday wrote "dark"; the user has since changed it
	got, changed, err := Splice(existing, nil, prev, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("changed = true, want false — nothing friday owns is still there to retract")
	}
	if string(got) != string(existing) {
		t.Errorf("a hand-edited table must survive retraction byte for byte:\n%s\nwant\n%s", got, existing)
	}
}

// TestSpliceRetractionKeepsUserKeysUnderRetractedServer: dropping a server
// from mcp.yaml must not take the user's hand-added env table with it.
func TestSpliceRetractionKeepsUserKeysUnderRetractedServer(t *testing.T) {
	existing := []byte("[mcp_servers.github]\ncommand = \"npx\"\n\n[mcp_servers.github.env]\nGITHUB_TOKEN = \"sekrit\"\n")
	prev := []byte("[mcp_servers.github]\ncommand = \"npx\"\n")
	got, changed, err := Splice(existing, nil, prev, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true (friday's own command must be retracted)")
	}
	s := string(got)
	if !strings.Contains(s, `GITHUB_TOKEN = "sekrit"`) {
		t.Errorf("the user's own env table was destroyed with friday's entry:\n%s", s)
	}
	if strings.Contains(s, "npx") {
		t.Errorf("friday's own leaf was not retracted:\n%s", s)
	}
}

func TestSpliceBareScalarRootKeyCreatesAndUpdates(t *testing.T) {
	payload := []byte("model = \"gpt-5-codex\"\n")
	tables, err := Tables(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := Splice(nil, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true (new key created)")
	}
	if !strings.Contains(string(got), `model = "gpt-5-codex"`) && !strings.Contains(string(got), `model = 'gpt-5-codex'`) {
		t.Errorf("model not written: %s", got)
	}

	// A stale existing scalar is replaced in place; an unrelated sibling
	// key survives untouched.
	existing := []byte("unrelated = 1\nmodel = \"old-model\"\n")
	got2, changed2, err := Splice(existing, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed2 {
		t.Error("changed = false, want true (value updated)")
	}
	if !strings.Contains(string(got2), "unrelated = 1") {
		t.Errorf("unrelated key lost: %s", got2)
	}
	if strings.Contains(string(got2), "old-model") {
		t.Errorf("stale value survived: %s", got2)
	}
}

func TestSpliceBareScalarRootKeyRetractsWhenSourceDrops(t *testing.T) {
	prev := []byte("model = \"gpt-5-codex\"\n")
	existing := []byte("unrelated = 1\nmodel = \"gpt-5-codex\"\n")
	// The new payload no longer defines model at all (e.g. defaults.yaml's
	// model.name was removed).
	tables, err := Tables(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := Splice(existing, nil, prev, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true (stale key retracted)")
	}
	if strings.Contains(string(got), "model") {
		t.Errorf("model must be retracted once the source stops defining it: %s", got)
	}
	if !strings.Contains(string(got), "unrelated = 1") {
		t.Errorf("unrelated key lost: %s", got)
	}
}

func mcpPayload(servers ...string) []byte {
	var buf strings.Builder
	for _, name := range servers {
		buf.WriteString("[mcp_servers." + name + "]\ncommand = \"npx\"\n\n")
	}
	return []byte(buf.String())
}

func TestSpliceCreatesNamedTablesOnEmptyTarget(t *testing.T) {
	payload := mcpPayload("github", "linear")
	tables, err := Tables(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := Splice(nil, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	s := string(got)
	if !strings.Contains(s, "[mcp_servers.github]") || !strings.Contains(s, "[mcp_servers.linear]") {
		t.Errorf("missing expected tables:\n%s", s)
	}
}

func TestSplicePreservesUnrelatedContentAndUserOwnServer(t *testing.T) {
	existing := []byte("# my config\n[model]\nname = \"opus\"\n\n[mcp_servers.my-own]\ncommand = \"my-tool\"\n")
	payload := mcpPayload("github")
	tables, _ := Tables(payload)
	got, changed, err := Splice(existing, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	s := string(got)
	if !strings.Contains(s, "# my config") || !strings.Contains(s, `name = "opus"`) {
		t.Errorf("unrelated content lost:\n%s", s)
	}
	if !strings.Contains(s, "[mcp_servers.my-own]") || !strings.Contains(s, `command = "my-tool"`) {
		t.Errorf("user's own MCP server table dropped — union semantics broken:\n%s", s)
	}
	if !strings.Contains(s, "[mcp_servers.github]") {
		t.Errorf("new server missing:\n%s", s)
	}
}

func TestSpliceRetractsTableRemovedFromPayloadViaPrev(t *testing.T) {
	// The target still holds exactly what prev says friday wrote for "stale",
	// which is what makes it friday's to retract.
	existing := []byte("[mcp_servers.github]\ncommand = \"npx\"\n\n[mcp_servers.stale]\ncommand = \"npx\"\n")
	prev := mcpPayload("github", "stale") // friday previously wrote both
	payload := mcpPayload("github")       // mcp.yaml no longer defines "stale"
	tables, _ := Tables(payload)
	got, changed, err := Splice(existing, payload, prev, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	s := string(got)
	if strings.Contains(s, "stale") {
		t.Errorf("retracted table lingered, header and all:\n%s", s)
	}
	if !strings.Contains(s, "[mcp_servers.github]") {
		t.Errorf("surviving server lost:\n%s", s)
	}
}

func TestSpliceRetractionNeverTouchesUserOwnTable(t *testing.T) {
	// friday previously wrote "old" and the payload dropped it; the user's
	// own "their-own" table was never friday's and must survive untouched
	// regardless of what prev or payload say.
	existing := []byte("[mcp_servers.old]\ncommand = \"npx\"\n\n[mcp_servers.their-own]\ncommand = \"user-tool\"\n")
	prev := mcpPayload("old")
	payload := mcpPayload() // now empty — but Splice is still called with an explicit (empty) table set
	got, changed, err := Splice(existing, payload, prev, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true (old must be retracted)")
	}
	s := string(got)
	if strings.Contains(s, "[mcp_servers.old]") {
		t.Errorf("retracted table lingered:\n%s", s)
	}
	if !strings.Contains(s, "[mcp_servers.their-own]") || !strings.Contains(s, "user-tool") {
		t.Errorf("user's own table must never be retracted:\n%s", s)
	}
}

func TestSpliceReRenderOfIdenticalContentIsNoOp(t *testing.T) {
	existing := []byte("[model]\nname = \"opus\"\n")
	payload := mcpPayload("github")
	tables, _ := Tables(payload)
	first, changed1, err := Splice(existing, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed1 {
		t.Error("changed = false on first splice, want true")
	}
	second, changed2, err := Splice(first, payload, payload, tables)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("changed = true on identical re-splice, want false")
	}
	if string(first) != string(second) {
		t.Errorf("identical re-splice rewrote bytes:\n%s\nvs\n%s", first, second)
	}
}

func TestSpliceNestedTableGetsExpandedSections(t *testing.T) {
	// Verifies the leaf walk emits its own [section] headers rather than one
	// long inline table (which multi-line TOML syntax cannot express at all).
	payload := []byte(`
[permissions.friday]
description = "x"

[permissions.friday.filesystem]
[permissions.friday.filesystem.":workspace_roots"]
"src/**" = "write"

[permissions.friday.network]
enabled = true
[permissions.friday.network.domains]
"docs.anthropic.com" = "allow"
`)
	tables, err := Tables(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tables, []string{"permissions"}) {
		t.Fatalf("tables = %v, want [permissions]", tables)
	}
	got, _, err := Splice(nil, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"[permissions.friday]", `description = 'x'`,
		"workspace_roots", "'src/**' = 'write'",
		"[permissions.friday.network]", "enabled = true",
		"'docs.anthropic.com' = 'allow'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "{") {
		t.Errorf("expected expanded sections, not an inline table:\n%s", s)
	}
}

// TestSpliceMergesBOMdTarget: PowerShell 5.1's Set-Content writes a UTF-8
// BOM by default, and go-toml rejects it — without stripping, every merge
// rule for a shell-edited config.toml is permanently skipped.
func TestSpliceMergesBOMdTarget(t *testing.T) {
	existing := append([]byte("\xEF\xBB\xBF"), []byte("[other]\nkey = \"val\"\n")...)
	payload := []byte("[tui]\ntheme = \"dark\"\n")
	tables, err := Tables(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := Splice(existing, payload, nil, tables)
	if err != nil {
		t.Fatalf("a BOM must not make the target unmergeable: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if !strings.HasPrefix(string(got), "\xEF\xBB\xBF") {
		t.Errorf("the target's BOM must be re-emitted, not dropped: %q", got)
	}
	if !strings.Contains(string(got), "theme") || !strings.Contains(string(got), `key = "val"`) {
		t.Errorf("BOM'd target not merged: %q", got)
	}
}

func TestSpliceReplacesATableSittingWhereFridayOwnsAScalar(t *testing.T) {
	// TOML cannot hold both shapes at one path, so a target that turned
	// friday's owned scalar into a table is replaced rather than skipped —
	// the write is confirm-gated, and leaving it would mean friday could
	// never sync that key again.
	existing := []byte("[model]\nname = \"opus\"\n")
	payload := []byte("model = \"gpt-5-codex\"\n")
	tables, _ := Tables(payload)
	got, changed, err := Splice(existing, payload, nil, tables)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if !strings.Contains(string(got), "gpt-5-codex") {
		t.Errorf("owned scalar not written: %s", got)
	}
}

func TestSpliceMalformedTargetErrors(t *testing.T) {
	if _, _, err := Splice([]byte("not = valid = ="), mcpPayload("a"), nil, []string{"mcp_servers"}); err == nil {
		t.Error("expected error for malformed target")
	}
}
