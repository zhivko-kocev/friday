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
