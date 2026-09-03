package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/lint"
	"github.com/zhivko-kocev/friday/internal/output"
)

// TestDoctorReportsMissingStore checks that doctor refuses to claim health
// when there's no store at all. Other paths exercise integration territory
// the engine tests already cover end-to-end.
func TestDoctorReportsMissingStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if code := Run([]string{"doctor"}, "test"); code != 1 {
		t.Errorf("friday doctor with no store returned %d, want 1", code)
	}
}

func TestDoctorOKWhenStoreExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}

	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"doctor"}, "test"); code != 0 {
		t.Errorf("friday doctor on empty store returned %d, want 0", code)
	}
}

// TestUnquotedExpansion pins both the original substring-era cases and the
// exact false negative that motivated the mvdan.cc/sh/v3/syntax rewrite: the
// old `strings.Contains(cmd, "\"")` guard cleared the whole check the moment
// ANY quote appeared anywhere in the command, so a genuinely unquoted $HOME
// sitting beside an unrelated quoted string used to slip through as "safe".
func TestUnquotedExpansion(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`bash $HOME/.friday/hooks/scripts/git-guard.sh`, true},
		{`bash ${CLAUDE_PLUGIN_ROOT}/hooks/scripts/git-guard.sh`, true},
		{`bash ${CLAUDE_PROJECT_DIR}/.claude/hooks/x.sh`, true},
		{`bash ~/.friday/hooks/x.sh`, true},
		{`bash "$HOME/.friday/hooks/scripts/git-guard.sh"`, false}, // quoted → safe
		{`bash "${CLAUDE_PLUGIN_ROOT}/hooks/x.sh"`, false},
		{`echo no path variables here`, false},
		// The bug: $HOME itself is unquoted even though the command also
		// carries an unrelated quoted argument. The old guard saw the `"`
		// and cleared the whole check; the new parse still flags $HOME
		// because it never sits inside a *DblQuoted.
		{`bash $HOME/hooks/x.sh "some other arg"`, true},
		{`echo "unrelated" && bash $HOME/hooks/x.sh`, true},
		// Concatenation across a quote boundary — "$HOME"-part-quoted,
		// path-suffix-not — the whole word still parses as one *Word whose
		// Parts mix a *DblQuoted and a bare *Lit; the ParamExp itself is
		// nested inside the DblQuoted, so this reads as quoted.
		{`bash "$HOME"/hooks/x.sh`, false},
		// A command mvdan can't parse (unbalanced quote) must not panic and
		// must not be flagged — friday doctor is an advisor, not a shell.
		{`bash $HOME/hooks/x.sh "unterminated`, false},
	}
	for _, c := range cases {
		if got := unquotedExpansion(c.cmd); got != c.want {
			t.Errorf("unquotedExpansion(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// Entry-file variants are informational — legacy identity.md and even
// multiple variants must not fail the health check.
func TestDoctorEntryFileVariants(t *testing.T) {
	for name, files := range map[string][]string{
		"legacy identity": {"identity.md"},
		"core":            {"core.md"},
		"nested core":     {filepath.Join("core", "core.md")},
		"multiple":        {"core.md", "identity.md"},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			if runtime.GOOS == "windows" {
				t.Setenv("LocalAppData", t.TempDir())
			}
			storeDir := filepath.Join(home, ".friday")
			for _, rel := range files {
				full := filepath.Join(storeDir, rel)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if code := Run([]string{"doctor"}, "test"); code != 0 {
				t.Errorf("friday doctor returned %d, want 0", code)
			}
		})
	}
}

// captureStdouterr swaps both os.Stdout and os.Stderr for pipes draining
// into one buffer while fn runs, for assertions spanning output.Err's
// stderr-only Error-severity lines and output.Warn/OK's stdout lines
// together. Drained in goroutines while fn runs for the same reason
// captureStdout is: small OS pipe buffers deadlock a post-hoc read.
func captureStdouterr(t *testing.T, fn func()) string {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	re, we, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = wo, we
	// Separate buffers per stream: io.Copy'ing both pipes into one shared
	// bytes.Buffer from two goroutines would be a data race (Buffer isn't
	// safe for concurrent writers) — concatenate only after both drain.
	var outBuf, errBuf bytes.Buffer
	doneOut := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outBuf, ro)
		close(doneOut)
	}()
	doneErr := make(chan struct{})
	go func() {
		_, _ = io.Copy(&errBuf, re)
		close(doneErr)
	}()
	fn()
	_ = wo.Close()
	_ = we.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	<-doneOut
	<-doneErr
	return outBuf.String() + errBuf.String()
}

// TestPrintFindings pins the shared rendering contract behind both the
// store-checks and agnix sections: empty input prints "no issues" and
// leaves *bad untouched, every Error bumps *bad and renders via output.Err,
// every Warn renders via output.Warn without touching *bad, and a run with
// at least one Warn closes with the "advisory warning(s)" count line naming
// .friday-doctor.yaml.
func TestPrintFindings(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	t.Run("empty", func(t *testing.T) {
		bad := 0
		got := captureStdout(t, func() { printFindings(nil, &bad) })
		if bad != 0 {
			t.Errorf("bad = %d, want 0", bad)
		}
		if !strings.Contains(got, "no issues") {
			t.Errorf("got %q, want it to contain \"no issues\"", got)
		}
	})

	t.Run("mixed severities", func(t *testing.T) {
		bad := 0
		findings := []lint.Finding{
			{Rule: "agnix/AS-001", Path: "SKILL.md", Severity: lint.Error, Msg: "boom"},
			{Rule: "oversized", Path: "core.md", Severity: lint.Warn, Msg: "64KiB"},
		}
		got := captureStdouterr(t, func() { printFindings(findings, &bad) })
		if bad != 1 {
			t.Errorf("bad = %d, want 1 (only the Error finding counts)", bad)
		}
		for _, want := range []string{"agnix/AS-001", "SKILL.md", "boom", "oversized", "core.md", "64KiB", "1 advisory warning"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
	})
}

// doctorFixtureStore creates a minimal store containing one oversized file
// (>64KiB), a deterministic trigger for a native Warn-severity finding, and
// points HOME/USERPROFILE/cache env vars at scratch dirs the same way every
// other doctor test does.
func doctorFixtureStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 70*1024)
	if err := os.WriteFile(filepath.Join(storeDir, "big.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	return storeDir
}

// TestDoctorReportsAgnixHintWhenNotInstalled guards D26's absent-agnix path:
// friday doctor never fails because agnix is missing, and names where to
// get it rather than silently skipping the section. Skipped when a real
// agnix binary happens to be on the machine running this test, since then
// the section's content is genuinely out of this test's control.
func TestDoctorReportsAgnixHintWhenNotInstalled(t *testing.T) {
	if _, err := exec.LookPath("agnix"); err == nil {
		t.Skip("a real agnix binary is on PATH; this test needs it absent to be deterministic")
	}
	doctorFixtureStore(t)
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	var code int
	got := captureStdout(t, func() { code = Run([]string{"doctor"}, "test") })
	if code != 0 {
		t.Errorf("friday doctor returned %d, want 0", code)
	}
	if !strings.Contains(got, "agnix") || !strings.Contains(got, "not installed") {
		t.Errorf("doctor output missing the agnix-not-installed hint:\n%s", got)
	}
}

// TestDoctorJSONFindingShape pins doctorJSON's per-finding field mapping —
// the keyed findingJSON{...} literal replacing a positional one that would
// silently scramble fields on reorder — and that Origin is omitted (not
// printed as an empty string) for friday's own, non-delegated findings.
func TestDoctorJSONFindingShape(t *testing.T) {
	if _, err := exec.LookPath("agnix"); err == nil {
		t.Skip("a real agnix binary is on PATH; this test needs it absent to be deterministic")
	}
	doctorFixtureStore(t)
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	var code int
	got := captureStdout(t, func() { code = Run([]string{"doctor", "--json"}, "test") })
	if code != 0 {
		t.Fatalf("friday doctor --json returned %d, want 0 (warnings alone don't fail it)", code)
	}
	var parsed struct {
		Findings []map[string]any `json:"findings"`
		Summary  map[string]int   `json:"summary"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("doctor --json did not print valid JSON: %v\n%s", err, got)
	}
	var oversized map[string]any
	for _, f := range parsed.Findings {
		if f["rule"] == "oversized" {
			oversized = f
		}
	}
	if oversized == nil {
		t.Fatalf("no \"oversized\" finding in %+v", parsed.Findings)
	}
	if oversized["severity"] != "warn" || oversized["path"] != "big.md" {
		t.Errorf("oversized finding = %+v, unexpected rule/severity/path mapping", oversized)
	}
	if _, hasOrigin := oversized["origin"]; hasOrigin {
		t.Errorf("native finding must omit \"origin\", got %+v", oversized)
	}
	if parsed.Summary["warn"] < 1 {
		t.Errorf("summary = %+v, want at least one warn counted", parsed.Summary)
	}
}

// Every doctor test before this one ran against a store with zero installed
// adapters, so the adapters/drift sections and the whole reportHooks switch
// never executed — despite all six presets tripping it the moment their agent
// is installed. This drives both shapes it discriminates: a merge-json hook
// wiring and a confirm_hook guard plugin.
func TestDoctorReportsHookWiringForAnInstalledAdapter(t *testing.T) {
	setDriftEnv(t)
	store := t.TempDir()
	t.Setenv("FRIDAY_HOME", store)
	target := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(store, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hooks/hooks.json", `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"bash ${HOME}/guard.sh"}]}]}}`)
	write("hooks/opencode/git-guard.ts", "export const Guard = async () => ({});\n")
	write(config.ManifestName, "version: 1\nadapters:\n"+
		"  claude:\n    target: "+filepath.ToSlash(target)+"\n    rules:\n"+
		"      - from: hooks/hooks.json\n        to: settings.json\n        strategy: merge-json\n"+
		"  opencode:\n    target: "+filepath.ToSlash(target)+"\n    rules:\n"+
		"      - from: hooks/opencode/git-guard.ts\n        to: plugins/git-guard.ts\n        confirm_hook: true\n")

	out := captureStdout(t, func() { Run([]string{"doctor"}, "test") })

	if !strings.Contains(out, "hooks") {
		t.Fatalf("no hooks section for an installed adapter:\n%s", out)
	}
	// The merge-json wiring and the guard plugin get different words, because
	// only one of them runs the moment it lands.
	if !strings.Contains(out, "wire the store's hooks") {
		t.Errorf("merge-json hook wiring not reported:\n%s", out)
	}
	if !strings.Contains(out, "guard plugin") {
		t.Errorf("confirm_hook guard plugin not reported:\n%s", out)
	}
	if !strings.Contains(out, "asks before installing it") {
		t.Errorf("plugin report does not say the write is confirmation-gated:\n%s", out)
	}
	// And the unquoted-expansion advisory reaches the integration point, not
	// just the parser it delegates to.
	if !strings.Contains(out, "unquoted") {
		t.Errorf("unquoted ${HOME} in a hook command not flagged:\n%s", out)
	}
}
