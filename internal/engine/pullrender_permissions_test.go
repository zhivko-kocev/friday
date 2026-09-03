package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
	"github.com/zhivko-kocev/friday/internal/spec"
)

func sliceContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func permissionsPullAdapter(targetAbs string) *config.Adapter {
	return renderAdapter(targetAbs, renderRule("permissions.yaml", "settings.json", rules.SurfacePermissions, rules.StrategyMergeJSON))
}

func TestPullRenderPermissionsCreatesNewPermissionsYAML(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "settings.json", `{"permissions":{"allow":["Bash(git status)"],"deny":["Read(**/.env)"]}}`)
	changes, err := planPull(nil, "claude", permissionsPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Fatalf("action = %s, want create: %+v", ch.Action, ch)
	}
	if ch.DestPath != filepath.Join(storeAbs, "permissions.yaml") {
		t.Errorf("DestPath = %q, want permissions.yaml in the store", ch.DestPath)
	}
	got, err := spec.ParsePermissions(ch.NewContent)
	if err != nil {
		t.Fatalf("invalid permissions.yaml: %v\n%s", err, ch.NewContent)
	}
	if !sliceContains(got.Exec.Allow, "git status") {
		t.Errorf("exec.allow = %v, want git status recovered", got.Exec.Allow)
	}
	if !sliceContains(got.Read.Deny, "**/.env") {
		t.Errorf("read.deny = %v, want **/.env recovered", got.Read.Deny)
	}
}

func TestPullRenderPermissionsMissingTargetSkipped(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"permissions.yaml": "version: 1\n"})
	changes, err := planPull(nil, "claude", permissionsPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("got %+v, want no changes (no settings.json to pull from)", changes)
	}
}

func TestPullRenderPermissionsNeverDeletesAbsentPattern(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"permissions.yaml": "version: 1\nexec:\n  deny: [\"rm -rf /\"]\n",
	})
	// The native form no longer mentions rm -rf / at all (e.g. the user
	// removed it by hand) but does carry a newly approved command.
	writeTarget(t, targetAbs, "settings.json", `{"permissions":{"allow":["Bash(npm test)"]}}`)
	changes, err := planPull(nil, "claude", permissionsPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := spec.ParsePermissions(changes[0].NewContent)
	if err != nil {
		t.Fatalf("invalid permissions.yaml: %v\n%s", err, changes[0].NewContent)
	}
	if !sliceContains(got.Exec.Deny, "rm -rf /") {
		t.Error("a pattern absent from this adapter's native form must not be deleted from permissions.yaml")
	}
	if !sliceContains(got.Exec.Allow, "npm test") {
		t.Error("the newly captured pattern must be added")
	}
}

func TestPullRenderPermissionsInSyncWhenUnchanged(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"permissions.yaml": testPermissionsYAML})
	ad := permissionsPullAdapter(targetAbs)
	pushed, err := planPush(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	writeTarget(t, targetAbs, "settings.json", string(pushed[0].NewContent))
	changes, err := planPull(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Errorf("action = %s, want in-sync after a clean push/pull round trip: %+v", changes[0].Action, changes[0])
	}
}

// TestPullRenderPermissionsRefusesWriteBackIntoSource mirrors the mcp.yaml
// guard: permissions.yaml lives only in the "team" source, a read-only
// checkout friday never writes a capture into.
func TestPullRenderPermissionsRefusesWriteBackIntoSource(t *testing.T) {
	roots, targetAbs := scaffold2(t,
		map[string]string{"permissions.yaml": "version: 1\n"},
		nil,
	)
	writeTarget(t, targetAbs, "settings.json", `{"permissions":{"allow":["Bash(git status)"]}}`)
	ad := permissionsPullAdapter(targetAbs)
	changes, err := planPull(nil, "claude", ad, roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("got %+v, want one unsupported change (permissions.yaml is owned by a read-only source)", changes)
	}
	if changes[0].DestPath != "" {
		t.Errorf("DestPath = %q, must be empty so nothing can be written into the source", changes[0].DestPath)
	}
	if !strings.Contains(changes[0].Reason, `is owned by source "team"`) ||
		!strings.Contains(changes[0].Reason, "copy it into the primary store") {
		t.Errorf("reason must name the source and both escape routes: %q", changes[0].Reason)
	}
}

func TestPullRenderPermissionsUnparseableTargetIsUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "settings.json", `not valid json`)
	changes, err := planPull(nil, "claude", permissionsPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("got %+v, want one unsupported change for unparseable settings.json", changes)
	}
}

// TestPullRenderPermissionsClaudeResidueGoesToNativePassthrough is the
// headline case Phase 5's passthrough fragment exists to serve: a claude
// permission entry friday's five axes cannot express (here, "Task") must
// not be silently dropped on pull — it lands verbatim in the store's own
// settings/claude.json, alongside the recovered axis patterns landing in
// permissions.yaml.
func TestPullRenderPermissionsClaudeResidueGoesToNativePassthrough(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "settings.json", `{"permissions":{"allow":["Bash(git status)","Task"]}}`)
	changes, err := planPull(nil, "claude", permissionsPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2 (permissions.yaml + settings/claude.json residue): %+v", len(changes), changes)
	}
	var permCh, residueCh Change
	for _, ch := range changes {
		switch ch.DestRel {
		case "permissions.yaml":
			permCh = ch
		case "settings/claude.json":
			residueCh = ch
		default:
			t.Fatalf("unexpected DestRel %q", ch.DestRel)
		}
	}
	if permCh.DestRel == "" || residueCh.DestRel == "" {
		t.Fatalf("expected both permissions.yaml and settings/claude.json changes, got %+v", changes)
	}
	got, err := spec.ParsePermissions(permCh.NewContent)
	if err != nil {
		t.Fatalf("invalid permissions.yaml: %v\n%s", err, permCh.NewContent)
	}
	if !sliceContains(got.Exec.Allow, "git status") {
		t.Errorf("exec.allow = %v, want git status recovered", got.Exec.Allow)
	}
	if residueCh.Action != ActionCreate {
		t.Errorf("residue action = %s, want create", residueCh.Action)
	}
	if !strings.Contains(string(residueCh.NewContent), `"Task"`) {
		t.Errorf("residue must preserve Task verbatim: %s", residueCh.NewContent)
	}
	if residueCh.Warning == "" {
		t.Error("residue change must carry a warning explaining what it is")
	}
}

// TestPullRenderPermissionsClaudeResidueMergesIntoExistingFragment confirms
// a second pull composes with — rather than clobbers — whatever the
// passthrough fragment already holds, including a user's own hand-written
// content unrelated to permissions.
func TestPullRenderPermissionsClaudeResidueMergesIntoExistingFragment(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"settings/claude.json": `{"statusLine":{"type":"command"},"permissions":{"allow":["OldResidue"]}}`,
	})
	writeTarget(t, targetAbs, "settings.json", `{"permissions":{"allow":["NewResidue"]}}`)
	changes, err := planPull(nil, "claude", permissionsPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	var residueCh Change
	for _, ch := range changes {
		if ch.DestRel == "settings/claude.json" {
			residueCh = ch
		}
	}
	if residueCh.DestRel == "" {
		t.Fatalf("no settings/claude.json change produced: %+v", changes)
	}
	s := string(residueCh.NewContent)
	if !strings.Contains(s, `"statusLine"`) {
		t.Errorf("unrelated existing key lost: %s", s)
	}
	if !strings.Contains(s, "OldResidue") || !strings.Contains(s, "NewResidue") {
		t.Errorf("expected both old and new residue entries preserved: %s", s)
	}
}

func TestPullRenderPermissionsOpenCodeReportsUnrecognisedAsWarning(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "opencode.json", `{"permission":{"bash":{"git status":"allow"},"theme":{"x":"y"}}}`)
	ad := renderAdapter(targetAbs, renderRule("permissions.yaml", "opencode.json", rules.SurfacePermissions, rules.StrategyMergeJSON))
	changes, err := planPull(nil, "opencode", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("opencode has no residue destination — want exactly one change, got %+v", changes)
	}
	got, err := spec.ParsePermissions(changes[0].NewContent)
	if err != nil {
		t.Fatalf("invalid permissions.yaml: %v\n%s", err, changes[0].NewContent)
	}
	if !sliceContains(got.Exec.Allow, "git status") {
		t.Errorf("exec.allow = %v, want git status recovered", got.Exec.Allow)
	}
	if !strings.Contains(changes[0].Warning, "theme") {
		t.Errorf("warning must name the unrecognised key: %q", changes[0].Warning)
	}
}

// TestPullRenderPermissionsGeminiNonCopyMergeReportsNoInverse covers a
// hand-edited friday.yaml that gives gemini's permissions rule a real merge
// strategy: PermissionsInverse has no gemini branch, and the refusal must not
// claim the file is unparseable — friday parses it fine, it just cannot map
// it back.
func TestPullRenderPermissionsGeminiNonCopyMergeReportsNoInverse(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "policies/friday.toml", "[[rule]]\ntoolName = \"read_file\"\ndecision = \"deny\"\n")
	ad := renderAdapter(targetAbs, renderRule("permissions.yaml", "policies/friday.toml", rules.SurfacePermissions, rules.StrategyMergeTOML))
	changes, err := planPull(nil, "gemini", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("got %+v, want one unsupported change", changes)
	}
	if !strings.Contains(changes[0].Reason, "no permissions pull-inverse") {
		t.Errorf("reason must name the real blocker: %q", changes[0].Reason)
	}
	if strings.Contains(changes[0].Reason, "cannot parse") {
		t.Errorf("friday parses the file fine — the reason must not claim otherwise: %q", changes[0].Reason)
	}
}

// TestPlanPullDefaultsSurfaceUnsupported is the one refusal justified by a
// distinct reason: a narrowed native enum cannot recover the canonical union
// value it was clamped from, so a defaults pull would silently narrow the
// store.
func TestPlanPullDefaultsSurfaceUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "settings.json", `{"model":"opus"}`)
	ad := renderAdapter(targetAbs, renderRule("defaults.yaml", "settings.json", rules.SurfaceDefaults, rules.StrategyMergeJSON))
	changes, err := planPull(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("got %+v, want one unsupported change", changes)
	}
	if !strings.Contains(changes[0].Reason, "clamped") {
		t.Errorf("reason should explain why a clamped enum is unrecoverable: %q", changes[0].Reason)
	}
}
