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
		name string
		in   []string
		want []string
	}{
		{"long form", []string{"--force"}, []string{"--all"}},
		{"single dash", []string{"-force"}, []string{"--all"}},
		{"with value", []string{"--force=true", "claude"}, []string{"--all=true", "claude"}},
		{"absent untouched", []string{"--all", "claude"}, []string{"--all", "claude"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renameFlag(c.in, "force", "all", "note")
			if !slices.Equal(out, c.want) {
				t.Errorf("renameFlag(%v) = %v, want %v", c.in, out, c.want)
			}
		})
	}
}

func TestRenameFlagNudges(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	got := captureStdout(t, func() { renameFlag([]string{"--force"}, "force", "all", "use --all") })
	if !strings.Contains(got, "use --all") {
		t.Errorf("deprecation nudge not printed: %q", got)
	}
	quiet := captureStdout(t, func() { renameFlag([]string{"--all"}, "force", "all", "use --all") })
	if strings.Contains(quiet, "use --all") {
		t.Errorf("nudge printed when old flag absent: %q", quiet)
	}
}

func TestDoctorJSONFlagIsDiscoverable(t *testing.T) {
	if !slices.Contains(completionsFor("doctor"), "--json") {
		t.Errorf("doctor --json not surfaced in completion/help (got %v)", completionsFor("doctor"))
	}
}
