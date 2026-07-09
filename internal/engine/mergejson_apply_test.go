package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// mergeJSONEnv sets up a store shipping hooks/hooks.json, a claude adapter that
// merges it into settings.json, and a temp-dir drift store.
func mergeJSONEnv(t *testing.T) (targetAbs string, cfg *config.Config) {
	t.Helper()
	storeAbs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(storeAbs, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "hooks", "hooks.json"), []byte(storeHooks), 0o644); err != nil {
		t.Fatal(err)
	}
	targetAbs = t.TempDir()
	cfg = &config.Config{
		Version:  1,
		StoreDir: storeAbs,
		Adapters: map[string]*config.Adapter{
			"claude": {Target: targetAbs, Rules: []*rules.Rule{{
				From:     rules.FromSpec{"hooks/hooks.json"},
				To:       "settings.json",
				Strategy: rules.StrategyMergeJSON,
			}}},
		},
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	return targetAbs, cfg
}

func settingsPath(targetAbs string) string { return filepath.Join(targetAbs, "settings.json") }

// approve confirms every drift-exempt write, so apply/drift tests exercise the
// write path without depending on the confirmation UI.
func approve() Options { return Options{ConfirmWrite: func(WriteConfirmInfo) bool { return true }} }

func TestMergeJSONPushCreatesThenPreservesNeighborNoConflict(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)

	// First push: create settings.json. No conflict resolver → any drift would
	// surface as a conflict; a merge-json create must not.
	changes, err := Push(cfg, approve())
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionCreate {
		t.Fatalf("first push action = %s, want create", changes[0].Action)
	}
	wired, err := os.ReadFile(settingsPath(targetAbs))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if !strings.Contains(string(wired), `"Bash"`) {
		t.Errorf("hooks not wired: %s", wired)
	}

	// The user edits an unmanaged key directly in settings.json.
	edited := strings.Replace(string(wired), "{\n", "{\n  \"model\": \"opus\",\n", 1)
	if err := os.WriteFile(settingsPath(targetAbs), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second push: the owned key already matches, so this is in-sync despite the
	// user's edit to model — and never a conflict.
	changes, err = Push(cfg, approve())
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Errorf("second push action = %s, want in-sync (model edit must not conflict)", changes[0].Action)
	}
	after, _ := os.ReadFile(settingsPath(targetAbs))
	if !strings.Contains(string(after), `"model": "opus"`) {
		t.Errorf("user's model key lost: %s", after)
	}
}

func TestMergeJSONPushUpdatePreservesNeighborNoConflictNoWriteBackWarning(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	// Pre-wire with the user's own model key and their own Read hook.
	writeTarget(t, targetAbs, "settings.json", `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"}]}}`)

	changes, err := Push(cfg, approve())
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionUpdate {
		t.Fatalf("action = %s, want update", ch.Action)
	}
	if ch.Warning != "" {
		t.Errorf("merge-json must not carry a write-back warning, got %q", ch.Warning)
	}
	after, _ := os.ReadFile(settingsPath(targetAbs))
	s := string(after)
	if !strings.Contains(s, `"model": "opus"`) || !strings.Contains(s, `"Bash"`) || !strings.Contains(s, `"Read"`) {
		t.Errorf("update must add friday's Bash hook while keeping model + the user's Read hook: %s", s)
	}
}

func TestMergeJSONPushRecordsNoDriftBaseline(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	if _, err := Push(cfg, approve()); err != nil {
		t.Fatal(err)
	}
	dp, err := drift.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := drift.Load(dp)
	if err != nil {
		t.Fatal(err)
	}
	if h := store.BaselineHash("claude", settingsPath(targetAbs)); h != "" {
		t.Errorf("merge-json must not record a target baseline, got %q", h)
	}
}

func TestMergeJSONPushMalformedTargetSkipsAndPreservesFile(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	writeTarget(t, targetAbs, "settings.json", `{ not valid json`)

	// A malformed settings.json is skipped (unsupported), the push does not
	// error out, and the file is left byte-for-byte intact.
	changes, err := Push(cfg, approve())
	if err != nil {
		t.Fatalf("malformed target must not fail the push: %v", err)
	}
	if changes[0].Action != ActionUnsupported {
		t.Errorf("action = %s, want unsupported", changes[0].Action)
	}
	got, _ := os.ReadFile(settingsPath(targetAbs))
	if string(got) != `{ not valid json` {
		t.Errorf("malformed settings.json was modified: %s", got)
	}
}

func TestMergeJSONPushNilConfirmerSkips(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	// No confirmer and no --force: the safe default must NOT install hooks.
	changes, err := Push(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionUnsupported {
		t.Errorf("action = %s, want unsupported (skipped without confirmation)", changes[0].Action)
	}
	if _, err := os.Stat(settingsPath(targetAbs)); !os.IsNotExist(err) {
		t.Error("settings.json was written without confirmation")
	}
}

func TestMergeJSONPushDeclinedSkips(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	changes, err := Push(cfg, Options{ConfirmWrite: func(WriteConfirmInfo) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionUnsupported {
		t.Errorf("action = %s, want unsupported (declined)", changes[0].Action)
	}
	if _, err := os.Stat(settingsPath(targetAbs)); !os.IsNotExist(err) {
		t.Error("settings.json was written after the write was declined")
	}
}

func TestMergeJSONPushForceBypassesConfirmer(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	called := false
	changes, err := Push(cfg, Options{
		Force:        true,
		ConfirmWrite: func(WriteConfirmInfo) bool { called = true; return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("--force must not invoke the confirmer")
	}
	if changes[0].Action != ActionCreate {
		t.Errorf("action = %s, want create under --force", changes[0].Action)
	}
	if _, err := os.Stat(settingsPath(targetAbs)); err != nil {
		t.Errorf("settings.json not written under --force: %v", err)
	}
}

func TestMergeJSONConfirmerReceivesCommands(t *testing.T) {
	_, cfg := mergeJSONEnv(t)
	var got WriteConfirmInfo
	if _, err := Push(cfg, Options{ConfirmWrite: func(i WriteConfirmInfo) bool { got = i; return true }}); err != nil {
		t.Fatal(err)
	}
	if got.Adapter != "claude" || got.DestRel != "settings.json" || !got.Creating {
		t.Errorf("confirm info = %+v, want claude/settings.json/creating", got)
	}
	if !strings.Contains(string(got.Source), `"Bash"`) {
		t.Errorf("confirm source missing hook content: %s", got.Source)
	}
}

func TestMergeJSONDryRunNeverPromptsOrWrites(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	called := false
	changes, err := Push(cfg, Options{DryRun: true, ConfirmWrite: func(WriteConfirmInfo) bool { called = true; return true }})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("dry run must not prompt for confirmation")
	}
	if changes[0].Action != ActionCreate {
		t.Errorf("dry-run action = %s, want create (the real pending action)", changes[0].Action)
	}
	if _, err := os.Stat(settingsPath(targetAbs)); !os.IsNotExist(err) {
		t.Error("dry run wrote settings.json")
	}
}

func TestMergeJSONStaleEntryRemovedAfterStoreChange(t *testing.T) {
	targetAbs, cfg := mergeJSONEnv(t)
	storeHook := filepath.Join(cfg.StoreDir, "hooks", "hooks.json")
	// The user already has their own hook.
	writeTarget(t, targetAbs, "settings.json", `{"hooks":{"PreToolUse":[{"matcher":"Read"}]}}`)

	// First push wires friday's Bash hook running old.sh.
	if err := os.WriteFile(storeHook, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"old.sh"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(cfg, approve()); err != nil {
		t.Fatal(err)
	}

	// The store's hook command changes; the second push must replace friday's own
	// entry in place (via the recorded owned-state), not leave a stale duplicate.
	if err := os.WriteFile(storeHook, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"new.sh"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(cfg, approve()); err != nil {
		t.Fatal(err)
	}

	s := readFileString(t, settingsPath(targetAbs))
	if strings.Contains(s, "old.sh") {
		t.Errorf("stale friday entry not removed after store change:\n%s", s)
	}
	if !strings.Contains(s, "new.sh") {
		t.Errorf("updated entry missing:\n%s", s)
	}
	if !strings.Contains(s, `"Read"`) {
		t.Errorf("user hook lost across the update:\n%s", s)
	}
	if n := strings.Count(s, `"matcher": "Bash"`); n != 1 {
		t.Errorf("expected exactly one friday Bash entry, got %d (stale duplicate?):\n%s", n, s)
	}

	// The owned-state file records what friday wrote.
	dp, _ := drift.DefaultPath()
	if _, err := os.Stat(drift.OwnedPath(dp)); err != nil {
		t.Errorf("owned-state file not written: %v", err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMergeJSONPushPreservesTargetMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningfully preserved on Windows")
	}
	targetAbs, cfg := mergeJSONEnv(t)
	writeTarget(t, targetAbs, "settings.json", `{"model":"opus"}`)
	if err := os.Chmod(settingsPath(targetAbs), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(cfg, approve()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(settingsPath(targetAbs))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600 (a sensitive settings.json must not be widened)", fi.Mode().Perm())
	}
}
