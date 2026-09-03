package cli

import (
	"bufio"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// answerStdin points the package's shared prompt reader at a canned answer
// stream for one test.
func answerStdin(t *testing.T, answers string) {
	t.Helper()
	saved := stdin
	stdin = bufio.NewReader(strings.NewReader(answers))
	t.Cleanup(func() { stdin = saved })
}

// TestHookConsentPreviewSurvivesQuiet is the informed-consent regression
// guard: --quiet used to strip the adapter, the target file, and the shell
// commands out of the hook prompt while still asking "install this hook?",
// which is the documented invariant ("show the exact commands and prompt
// before writing") half-honoured.
func TestHookConsentPreviewSurvivesQuiet(t *testing.T) {
	output.SetColor(false)
	output.SetQuiet(true)
	t.Cleanup(func() {
		output.SetColor(false)
		output.SetQuiet(false)
	})
	answerStdin(t, "n\n")

	var ok bool
	got := captureStdout(t, func() {
		ok = hookWriteConfirmer()(engine.WriteConfirmInfo{
			Adapter:  "claude",
			DestRel:  "hooks.json",
			Merge:    true,
			Creating: true,
			Source:   []byte(`{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"friday pull --no-interactive"}]}]}}`),
		})
	})
	if ok {
		t.Error("a declined prompt returned true")
	}
	for _, want := range []string{
		"claude",
		"hooks.json",
		"friday pull --no-interactive",
		"arbitrary shell",
		"install this hook?",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("--quiet hid %q from the consent prompt:\n%s", want, got)
		}
	}
}

// TestHookPluginConsentPreviewSurvivesQuiet covers the other confirm shape:
// a wholesale plugin install, where the body IS the code being wired in.
func TestHookPluginConsentPreviewSurvivesQuiet(t *testing.T) {
	output.SetColor(false)
	output.SetQuiet(true)
	t.Cleanup(func() {
		output.SetColor(false)
		output.SetQuiet(false)
	})
	answerStdin(t, "n\n")

	got := captureStdout(t, func() {
		hookWriteConfirmer()(engine.WriteConfirmInfo{
			Adapter: "opencode",
			DestRel: "plugin/friday.js",
			Source:  []byte("export const friday = () => run('friday pull')\n"),
		})
	})
	for _, want := range []string{"opencode", "plugin/friday.js", "run('friday pull')", "install this hook?"} {
		if !strings.Contains(got, want) {
			t.Errorf("--quiet hid %q from the plugin consent prompt:\n%s", want, got)
		}
	}
}

// TestPipedAnswersSurviveSuccessiveConfirmations guards the shared reader:
// bufio reads ahead, so a per-prompt reader swallowed the whole pipe on the
// first question and every later adapter was silently reported as declined.
func TestPipedAnswersSurviveSuccessiveConfirmations(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	answerStdin(t, "y\ny\ny\n")

	confirm := hookWriteConfirmer()
	info := engine.WriteConfirmInfo{
		Adapter: "claude",
		DestRel: "hooks.json",
		Merge:   true,
		Source:  []byte(`{"hooks":{}}`),
	}
	var got []bool
	captureStdout(t, func() {
		for range 3 {
			got = append(got, confirm(info))
		}
	})
	for i, ok := range got {
		if !ok {
			t.Errorf("confirmation %d read false; the queued answer was lost", i+1)
		}
	}
}
