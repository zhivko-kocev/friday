package lint

import (
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"testing"

	"github.com/zhivko-kocev/friday/internal/rules"
)

// withAgnixSeams swaps agnixLookPath and agnixRun for the duration of a test
// and restores the real implementations on cleanup — the same seam pattern
// internal/session/opencode.go uses for opencodeLookPath/opencodeRunner.
func withAgnixSeams(t *testing.T, lookPath func(string) (string, error), run agnixRunner) {
	t.Helper()
	origLookPath, origRun := agnixLookPath, agnixRun
	agnixLookPath, agnixRun = lookPath, run
	t.Cleanup(func() { agnixLookPath, agnixRun = origLookPath, origRun })
}

// fakeExitError produces a genuine *exec.ExitError with the given exit code
// by actually running a trivial shell command that exits with it — the
// portable way to get a real typed exec error rather than one hand-built
// from unexported os.ProcessState fields.
func fakeExitError(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit", strconv.Itoa(code))
	} else {
		cmd = exec.Command("sh", "-c", "exit "+strconv.Itoa(code))
	}
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("fakeExitError: got %v (%T), want *exec.ExitError", err, err)
	}
	return exitErr
}

func TestRunAgnixNotFoundWhenNotOnPath(t *testing.T) {
	withAgnixSeams(t,
		func(string) (string, error) { return "", errors.New("not found") },
		func(string, string) ([]byte, error) { t.Fatal("agnixRun should not be called"); return nil, nil },
	)
	cfg := lintFixture(t, map[string]string{"core.md": "hi"}, nil)

	findings, status, detail := RunAgnix(cfg)
	if status != AgnixNotFound {
		t.Errorf("status = %v, want AgnixNotFound", status)
	}
	if findings != nil {
		t.Errorf("findings = %v, want nil", findings)
	}
	if detail != "" {
		t.Errorf("detail = %q, want empty", detail)
	}
}

// sampleSARIF carries the `version` field real agnix emits — runAgnixRoot
// now rejects a document that is not SARIF 2.x, so a fixture without it is
// indistinguishable from the version-skew output friday must refuse.
const sampleSARIF = `{
  "version": "2.1.0",
  "runs": [
    {
      "results": [
        {"ruleId": "CC-SK-007", "level": "warning", "message": {"text": "Unrestricted Bash access"},
         "locations": [{"physicalLocation": {"artifactLocation": {"uri": "skills/x/SKILL.md"}}}]},
        {"ruleId": "AS-001", "level": "error", "message": {"text": "YAML frontmatter required"},
         "locations": [{"physicalLocation": {"artifactLocation": {"uri": "SKILL.md"}}}]}
      ]
    }
  ]
}`

// TestRunAgnixParsesSARIFFindings guards the D26 translation contract: every
// SARIF result becomes a Finding with Rule prefixed "agnix/", Origin set to
// "agnix", and level "error" mapped to Error while every other level (here
// "warning") maps to Warn.
func TestRunAgnixParsesSARIFFindings(t *testing.T) {
	withAgnixSeams(t,
		func(string) (string, error) { return "/usr/bin/agnix", nil },
		func(agnixPath, dir string) ([]byte, error) { return []byte(sampleSARIF), nil },
	)
	cfg := lintFixture(t, map[string]string{"core.md": "hi"}, nil)

	findings, status, detail := RunAgnix(cfg)
	if status != AgnixOK {
		t.Fatalf("status = %v, detail = %q, want AgnixOK", status, detail)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
	byRule := map[string]Finding{}
	for _, f := range findings {
		byRule[f.Rule] = f
	}
	warn, ok := byRule["agnix/CC-SK-007"]
	if !ok {
		t.Fatalf("missing agnix/CC-SK-007 finding in %+v", findings)
	}
	if warn.Severity != Warn || warn.Origin != "agnix" || warn.Path != "skills/x/SKILL.md" || warn.Msg != "Unrestricted Bash access" {
		t.Errorf("warn finding = %+v, unexpected shape", warn)
	}
	fatal, ok := byRule["agnix/AS-001"]
	if !ok {
		t.Fatalf("missing agnix/AS-001 finding in %+v", findings)
	}
	if fatal.Severity != Error || fatal.Origin != "agnix" || fatal.Path != "SKILL.md" {
		t.Errorf("error finding = %+v, unexpected shape", fatal)
	}
}

// TestRunAgnixNonZeroExitWithOutputStillParses guards the "exit code is not
// itself a failure" contract: agnix exits non-zero whenever it finds any
// issue at all, so a non-zero exit paired with real SARIF stdout must still
// be read as AgnixOK, not degrade to AgnixFailed.
func TestRunAgnixNonZeroExitWithOutputStillParses(t *testing.T) {
	exitErr := fakeExitError(t, 1)
	withAgnixSeams(t,
		func(string) (string, error) { return "/usr/bin/agnix", nil },
		func(agnixPath, dir string) ([]byte, error) { return []byte(sampleSARIF), exitErr },
	)
	cfg := lintFixture(t, map[string]string{"core.md": "hi"}, nil)

	findings, status, detail := RunAgnix(cfg)
	if status != AgnixOK {
		t.Fatalf("status = %v, detail = %q, want AgnixOK despite non-zero exit", status, detail)
	}
	if len(findings) != 2 {
		t.Errorf("got %d findings, want 2", len(findings))
	}
}

// TestRunAgnixFailsOnEmptyOutputWithNonZeroExit guards the genuine failure
// path: a non-zero exit with nothing on stdout to parse must degrade to
// AgnixFailed carrying the raw exit status, never fail RunAgnix's caller.
func TestRunAgnixFailsOnEmptyOutputWithNonZeroExit(t *testing.T) {
	exitErr := fakeExitError(t, 2)
	withAgnixSeams(t,
		func(string) (string, error) { return "/usr/bin/agnix", nil },
		func(agnixPath, dir string) ([]byte, error) { return nil, exitErr },
	)
	cfg := lintFixture(t, map[string]string{"core.md": "hi"}, nil)

	findings, status, detail := RunAgnix(cfg)
	if status != AgnixFailed {
		t.Fatalf("status = %v, want AgnixFailed", status)
	}
	if findings != nil {
		t.Errorf("findings = %v, want nil", findings)
	}
	if detail == "" {
		t.Error("detail is empty, want the raw exit status explained")
	}
}

// TestRunAgnixFailsOnUnparseableOutput guards the "never let a linter
// integration fail friday doctor" contract for the other failure mode:
// stdout that isn't valid SARIF (a future agnix output-format change, or a
// truncated/corrupted response) degrades to AgnixFailed rather than
// propagating a JSON error to the caller.
func TestRunAgnixFailsOnUnparseableOutput(t *testing.T) {
	withAgnixSeams(t,
		func(string) (string, error) { return "/usr/bin/agnix", nil },
		func(agnixPath, dir string) ([]byte, error) { return []byte("not json at all"), nil },
	)
	cfg := lintFixture(t, map[string]string{"core.md": "hi"}, nil)

	findings, status, detail := RunAgnix(cfg)
	if status != AgnixFailed {
		t.Fatalf("status = %v, want AgnixFailed", status)
	}
	if findings != nil {
		t.Errorf("findings = %v, want nil", findings)
	}
	if detail == "" {
		t.Error("detail is empty, want the parse error explained")
	}
}

// TestRunAgnixFailsWhenProcessCannotStart guards the third failure mode: the
// runner itself returning a plain (non-ExitError) error, e.g. the binary
// vanished between LookPath and exec.
func TestRunAgnixFailsWhenProcessCannotStart(t *testing.T) {
	withAgnixSeams(t,
		func(string) (string, error) { return "/usr/bin/agnix", nil },
		func(agnixPath, dir string) ([]byte, error) { return nil, errors.New("exec: no such file or directory") },
	)
	cfg := lintFixture(t, map[string]string{"core.md": "hi"}, nil)

	_, status, detail := RunAgnix(cfg)
	if status != AgnixFailed {
		t.Fatalf("status = %v, want AgnixFailed", status)
	}
	if detail == "" {
		t.Error("detail is empty, want the start failure explained")
	}
}

// TestRunAgnixRespectsDoctorIgnoreConfig guards that delegated findings flow
// through the exact same .friday-doctor.yaml disable list as friday's own
// checks — disable: ["agnix/CC-SK-007"] silences a delegated rule exactly
// like a native one.
func TestRunAgnixRespectsDoctorIgnoreConfig(t *testing.T) {
	withAgnixSeams(t,
		func(string) (string, error) { return "/usr/bin/agnix", nil },
		func(agnixPath, dir string) ([]byte, error) { return []byte(sampleSARIF), nil },
	)
	cfg := lintFixture(t, map[string]string{
		"core.md":  "hi",
		ConfigName: "disable: [\"agnix/CC-SK-007\"]\n",
	}, nil)

	findings, status, _ := RunAgnix(cfg)
	if status != AgnixOK {
		t.Fatalf("status = %v, want AgnixOK", status)
	}
	if len(findings) != 1 || findings[0].Rule != "agnix/AS-001" {
		t.Errorf("got %+v, want only agnix/AS-001 surviving the disable list", findings)
	}
}

// TestRunAgnixRootPrefixesNonPrimaryRoot guards the "<source>:" path prefix
// runAgnixRoot applies for any root that isn't the primary store — the same
// disambiguating convention lintRoot already uses for a configured source.
func TestRunAgnixRootPrefixesNonPrimaryRoot(t *testing.T) {
	origRun := agnixRun
	agnixRun = func(agnixPath, dir string) ([]byte, error) { return []byte(sampleSARIF), nil }
	t.Cleanup(func() { agnixRun = origRun })

	findings, err := runAgnixRoot("/usr/bin/agnix", rules.Root{Name: "upstream", Dir: t.TempDir()}, ".sources")
	if err != nil {
		t.Fatalf("runAgnixRoot: %v", err)
	}
	for _, f := range findings {
		if f.Path != "upstream:skills/x/SKILL.md" && f.Path != "upstream:SKILL.md" {
			t.Errorf("finding path = %q, want an \"upstream:\" prefix", f.Path)
		}
	}
}
