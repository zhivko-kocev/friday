package drift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ownedFile writes a raw owned-state map so a test can plant a record in a key
// shape only an older release ever wrote.
func ownedFile(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hooks-owned.json")
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Several rules legitimately merge into one target: friday wires four
// merge-json rules onto claude's settings.json. A pre-v1.0 record omits the
// rule's own source, so it cannot be attributed to any of them — handing it to
// the wrong one makes that rule retract a sibling's still-live content, and
// merge rules are drift-exempt so nothing would prompt first. Get must not
// read it at any source.
func TestOwnedGetIgnoresSourcelessLegacyRecord(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	path := ownedFile(t, map[string]string{
		ownedKeyV1("claude", target): `{"hooks":{"PreToolUse":[{"command":"guard"}]}}`,
	})

	o, err := LoadOwned(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"hooks/hooks.json", "settings/claude.json", "permissions.yaml"} {
		if got := o.Get("claude", source, target); got != nil {
			t.Errorf("Get(claude, %s) = %q, want nil — a source-less record must never be attributed to a rule", source, got)
		}
	}
}

// A record written under a name friday has since renamed is still this rule's,
// because source remains part of the key — so the rename fallback cannot
// cross rules the way the source-less shape could.
func TestOwnedGetFollowsAdapterRename(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	const payload = `{"mcpServers":{}}`
	path := ownedFile(t, map[string]string{
		ownedKey("antigravity", "mcp.yaml", target): payload,
	})

	o, err := LoadOwned(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(o.Get("gemini", "mcp.yaml", target)); got != payload {
		t.Errorf("Get(gemini, mcp.yaml) = %q, want %q via the antigravity rename", got, payload)
	}
	// A different rule's record is still not this rule's.
	if got := o.Get("gemini", "permissions.yaml", target); got != nil {
		t.Errorf("Get(gemini, permissions.yaml) = %q, want nil", got)
	}
}

// Set retires every shape the same record could have been written under, so no
// stale entry lingers past the first push that touches the target.
func TestOwnedSetRetiresEveryLegacyShape(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := ownedFile(t, map[string]string{
		ownedKeyV1("gemini", target):                   "stale-v1",
		ownedKeyV1("antigravity", target):              "stale-v1-legacy",
		ownedKey("antigravity", "mcp.yaml", target):    "stale-renamed",
		ownedKey("gemini", "permissions.yaml", target): "sibling-rule",
	})

	o, err := LoadOwned(path)
	if err != nil {
		t.Fatal(err)
	}
	o.Set("gemini", "mcp.yaml", target, []byte("current"))
	if err := o.Save(); err != nil {
		t.Fatal(err)
	}

	saved := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{
		ownedKeyV1("gemini", target),
		ownedKeyV1("antigravity", target),
		ownedKey("antigravity", "mcp.yaml", target),
	} {
		if _, ok := saved[retired]; ok {
			t.Errorf("key %q survived Set, want retired", retired)
		}
	}
	if saved[ownedKey("gemini", "mcp.yaml", target)] != "current" {
		t.Errorf("current record = %q, want %q", saved[ownedKey("gemini", "mcp.yaml", target)], "current")
	}
	// A sibling rule's own record is not this rule's to retire.
	if saved[ownedKey("gemini", "permissions.yaml", target)] != "sibling-rule" {
		t.Error("Set removed a sibling rule's record")
	}
}

// A Windows target path carries a drive-letter colon, so the key boundary
// cannot be found by counting colons.
func TestOwnedKeyPathFindsTheTargetPath(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("tmp", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key  string
		want string
		ok   bool
	}{
		{ownedKey("claude", "hooks/hooks.json", abs), abs, true},
		{ownedKeyV1("claude", abs), abs, true},
		{"claude:hooks/hooks.json:not-absolute", "", false},
		{"no-colons-at-all", "", false},
	}
	for _, c := range cases {
		got, ok := ownedKeyPath(c.key)
		if got != c.want || ok != c.ok {
			t.Errorf("ownedKeyPath(%q) = %q, %v; want %q, %v", c.key, got, ok, c.want, c.ok)
		}
	}
}

// The owned store is a machine-local cache: a corrupt file degrades the merge
// to exact-content dedup rather than bricking every write command.
func TestLoadOwnedCorruptFileIsEmptyNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks-owned.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := LoadOwned(path)
	if err != nil {
		t.Fatalf("LoadOwned on a corrupt file: %v", err)
	}
	if got := o.Get("claude", "hooks/hooks.json", "/x"); got != nil {
		t.Errorf("Get = %q, want nil", got)
	}
}

// Renaming an adapter in friday.yaml must not make every file friday itself
// wrote look foreign — Check would report drift and push would exit 2 on a
// purely cosmetic rename. Set retires the old baseline so the fallback cannot
// resurrect it later.
func TestStoreRenameFallbackAndRetirement(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(target, []byte("written"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	s.Set("antigravity", target, []byte("written"))
	if drifted, exists := s.Check("gemini", target); drifted || !exists {
		t.Errorf("Check(gemini) = %v, %v; want not-drifted via the antigravity baseline", drifted, exists)
	}

	s.Set("gemini", target, []byte("written"))
	if _, ok := s.hashes[s.key("antigravity", target)]; ok {
		t.Error("the antigravity baseline survived a gemini write, want retired")
	}
	if _, ok := s.hashes[s.key("gemini", target)]; !ok {
		t.Error("no gemini baseline recorded")
	}

	// A live adapter that shares no rename lineage keeps its own baseline.
	s.Set("claude", target, []byte("written"))
	if _, ok := s.hashes[s.key("gemini", target)]; !ok {
		t.Error("a claude write retired gemini's baseline")
	}

	// A machine with no legacy baseline at all is unaffected.
	fresh, err := Load(filepath.Join(dir, "fresh.json"))
	if err != nil {
		t.Fatal(err)
	}
	if drifted, exists := fresh.Check("gemini", target); !drifted || !exists {
		t.Errorf("Check on an unknown file = %v, %v; want drifted (friday never wrote it)", drifted, exists)
	}
}
