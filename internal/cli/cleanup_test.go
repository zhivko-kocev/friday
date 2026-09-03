package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/output"
)

func TestRenameFlag(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	cases := []struct {
		name    string
		in      []string
		want    []string
		wantHit bool
	}{
		{"long form", []string{"--force"}, []string{"--all"}, true},
		{"single dash", []string{"-force"}, []string{"--all"}, true},
		{"with value", []string{"--force=true", "claude"}, []string{"--all=true", "claude"}, true},
		{"absent untouched", []string{"--all", "claude"}, []string{"--all", "claude"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, hit := renameFlag(c.in, "force", "all")
			if !slices.Equal(out, c.want) {
				t.Errorf("renameFlag(%v) = %v, want %v", c.in, out, c.want)
			}
			if hit != c.wantHit {
				t.Errorf("renameFlag(%v) hit = %v, want %v", c.in, hit, c.wantHit)
			}
		})
	}
}

// The nudge must survive the move out of renameFlag — and must now respect
// --quiet, which it could not while it printed before urfave parsed anything.
func TestDeprecatedSpellingNudge(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	got := captureStdout(t, func() { Run([]string{"pull", "--force", "--dry-run", "--no-interactive"}, "test") })
	if !strings.Contains(got, "use --all") {
		t.Errorf("deprecation nudge not printed: %q", got)
	}
	quiet := captureStdout(t, func() { Run([]string{"--quiet", "pull", "--force", "--dry-run", "--no-interactive"}, "test") })
	if strings.Contains(quiet, "use --all") {
		t.Errorf("--quiet did not suppress the deprecation nudge: %q", quiet)
	}
	canonical := captureStdout(t, func() { Run([]string{"pull", "--all", "--dry-run", "--no-interactive"}, "test") })
	if strings.Contains(canonical, "use --all") {
		t.Errorf("nudge printed for the canonical spelling: %q", canonical)
	}
}

func TestDoctorJSONFlagIsDiscoverable(t *testing.T) {
	doctor := findCommand(t, "doctor")
	for _, f := range doctor.Flags {
		if slices.Contains(f.Names(), "json") {
			return
		}
	}
	t.Errorf("doctor --json not registered on the doctor command")
}
