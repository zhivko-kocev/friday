package output

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// captureStream swaps *stream (os.Stdout or os.Stderr) for a pipe while fn
// runs and returns everything written to it.
func captureStream(t *testing.T, stream **os.File, fn func()) string {
	t.Helper()
	old := *stream
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	*stream = w
	fn()
	_ = w.Close()
	*stream = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	return buf.String()
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TestPlainModeIsUnstyled is the guarantee behind the non-TTY / NO_COLOR /
// --no-color contract: with color off, every helper emits exactly its text
// with no escape sequences, byte-for-byte what scripts and the other tests
// expect.
func TestPlainModeIsUnstyled(t *testing.T) {
	SetColor(false)
	t.Cleanup(func() { SetColor(false) })

	cases := []struct {
		name string
		want string
		fn   func()
	}{
		{"OK", "  ✓ hello world\n", func() { OK("hello %s", "world") }},
		{"Skip", "  – skipped\n", func() { Skip("skipped") }},
		{"Warn", "  ! careful\n", func() { Warn("careful") }},
		{"Info", "  → note\n", func() { Info("note") }},
		{"Header", "\nTitle\n", func() { Header("Title") }},
		{"Dim", "  faint gray\n", func() { Dim("faint gray") }},
		{"Line", "  row\n", func() { Line(LevelWarn, "row") }},
		{"DiffAdd", "  +added\n", func() { DiffLine("+added") }},
		{"DiffDel", "  -removed\n", func() { DiffLine("-removed") }},
		{"DiffCtx", "   context\n", func() { DiffLine(" context") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := captureStream(t, &os.Stdout, c.fn)
			if got != c.want {
				t.Errorf("plain %s = %q, want %q", c.name, got, c.want)
			}
			if strings.Contains(got, "\x1b") {
				t.Errorf("plain %s leaked an escape sequence: %q", c.name, got)
			}
		})
	}
}

// TestErrGoesToStderrPlain — Err writes to stderr and is also unstyled in
// plain mode.
func TestErrGoesToStderrPlain(t *testing.T) {
	SetColor(false)
	t.Cleanup(func() { SetColor(false) })

	got := captureStream(t, &os.Stderr, func() { Err("boom %d", 7) })
	if want := "  ✗ boom 7\n"; got != want {
		t.Errorf("Err = %q, want %q", got, want)
	}
}

// TestColoredModeStylesText — with color forced on, helpers wrap their glyph
// in ANSI and the visible text (escapes stripped) still equals the plain form.
func TestColoredModeStylesText(t *testing.T) {
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	SetColor(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(saved)
		SetColor(false)
	})

	got := captureStream(t, &os.Stdout, func() { OK("hi") })
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("colored OK emitted no escape: %q", got)
	}
	if stripped := ansiRE.ReplaceAllString(got, ""); stripped != "  ✓ hi\n" {
		t.Errorf("colored OK stripped = %q, want %q", stripped, "  ✓ hi\n")
	}
}

// TestColorEnabledPrecedence guards D29's FORCE_COLOR addition: NO_COLOR /
// FRIDAY_NO_COLOR always win (forcing color off even over FORCE_COLOR),
// FORCE_COLOR forces color on independent of whether stdout is a TTY, and
// with neither set the result falls back to auto-detection.
func TestColorEnabledPrecedence(t *testing.T) {
	cases := []struct {
		name                          string
		noColor, fridayNoColor, force string
		want                          bool
	}{
		{name: "force color on", force: "1", want: true},
		{name: "force color on with any value", force: "true", want: true},
		{name: "NO_COLOR wins over FORCE_COLOR", noColor: "1", force: "1", want: false},
		{name: "FRIDAY_NO_COLOR wins over FORCE_COLOR", fridayNoColor: "1", force: "1", want: false},
		{name: "NO_COLOR alone forces off", noColor: "1", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", c.noColor)
			t.Setenv("FRIDAY_NO_COLOR", c.fridayNoColor)
			t.Setenv("FORCE_COLOR", c.force)
			if got := colorEnabled(); got != c.want {
				t.Errorf("colorEnabled() = %v, want %v", got, c.want)
			}
		})
	}
	// The documented third branch: with all three unset the answer is
	// whatever stdout is, which is the one case whose value differs between
	// `go test` and a terminal — so assert the delegation, not a constant.
	t.Run("falls back to TTY detection", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("FRIDAY_NO_COLOR", "")
		t.Setenv("FORCE_COLOR", "")
		if got := colorEnabled(); got != isTTY() {
			t.Errorf("colorEnabled() = %v, want isTTY() = %v", got, isTTY())
		}
	})
}

// TestColorEnabledIsPure guards the split of pinColorProfile out of the
// predicate: querying the color preference must not repin the process-global
// lipgloss renderer, which would silently change how every later caller in
// the process renders.
func TestColorEnabledIsPure(t *testing.T) {
	saved := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(saved) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "")
	t.Setenv("FRIDAY_NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	if !colorEnabled() {
		t.Fatal("colorEnabled() = false with FORCE_COLOR set")
	}
	if got := lipgloss.ColorProfile(); got != termenv.TrueColor {
		t.Errorf("colorEnabled() repinned the global profile to %v", got)
	}
}

// TestQuietSuppressesChatter pins the --quiet contract: every helper but Err
// goes silent.
func TestQuietSuppressesChatter(t *testing.T) {
	SetColor(false)
	SetQuiet(true)
	t.Cleanup(func() {
		SetColor(false)
		SetQuiet(false)
	})
	got := captureStream(t, &os.Stdout, func() {
		OK("ok")
		Skip("skip")
		Warn("warn")
		Info("info")
		Header("header")
		Dim("dim")
		Line(LevelWarn, "line")
		DiffLine("+added")
	})
	if got != "" {
		t.Errorf("quiet mode printed to stdout: %q", got)
	}
	if errOut := captureStream(t, &os.Stderr, func() { Err("boom") }); !strings.Contains(errOut, "boom") {
		t.Errorf("quiet mode swallowed an error: %q", errOut)
	}
}

// TestPromptContextSurvivesQuiet is the informed-consent guard: a prompt
// blocks on stdin whether or not --quiet is set, so the evidence it is asking
// about must print anyway. Suppressing the adapter, the file name, or the
// shell commands while still asking "install this hook?" / "keep canonical or
// use target?" turns consent into a blind guess over destructive writes.
func TestPromptContextSurvivesQuiet(t *testing.T) {
	SetColor(false)
	SetQuiet(true)
	t.Cleanup(func() {
		SetColor(false)
		SetQuiet(false)
	})
	got := captureStream(t, &os.Stdout, func() {
		WithPromptContext(func() {
			Warn("CONFLICT: OUT.md")
			Dim("  dest:   /home/u/OUT.md")
			DiffLine("-old")
		})
	})
	for _, want := range []string{"CONFLICT: OUT.md", "/home/u/OUT.md", "-old"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt context lost %q under --quiet:\n%s", want, got)
		}
	}
	// Chatter after the block is quiet again — the exemption is scoped, not
	// a global un-quieting.
	if after := captureStream(t, &os.Stdout, func() { OK("done") }); after != "" {
		t.Errorf("prompt context leaked past its block: %q", after)
	}
}

// TestPromptContextNests guards the scoping when a prompt's evidence renders
// through another prompt-context helper (the pull plan inside the apply
// prompt): the inner block must not un-quiet the outer one on the way out.
func TestPromptContextNests(t *testing.T) {
	SetColor(false)
	SetQuiet(true)
	t.Cleanup(func() {
		SetColor(false)
		SetQuiet(false)
	})
	got := captureStream(t, &os.Stdout, func() {
		WithPromptContext(func() {
			WithPromptContext(func() { Dim("inner") })
			Dim("outer")
		})
		Dim("after")
	})
	if !strings.Contains(got, "inner") || !strings.Contains(got, "outer") {
		t.Errorf("nested prompt context lost output:\n%s", got)
	}
	if strings.Contains(got, "after") {
		t.Errorf("nested prompt context leaked past its block:\n%s", got)
	}
}
