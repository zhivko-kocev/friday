// Package output centralises the CLI's user-facing print helpers so that
// every command renders status, errors, and diffs the same way.
package output

import (
	"fmt"
	"os"
	"strings"
)

// useColor is set on init from environment, then optionally overridden by
// the CLI via SetColor (e.g. for a --no-color flag).
var useColor = colorEnabled()

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("FRIDAY_NO_COLOR") != "" {
		return false
	}
	return isTTY()
}

// SetColor lets the CLI override the auto-detected color preference.
func SetColor(on bool) { useColor = on }

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cCyan   = "\033[36m"
	cGray   = "\033[90m"
)

func col(c, s string) string {
	if !useColor {
		return s
	}
	return c + s + cReset
}

func OK(format string, args ...any)   { fmt.Printf("  "+col(cGreen, "✓")+" "+format+"\n", args...) }
func Skip(format string, args ...any) { fmt.Printf("  "+col(cGray, "–")+" "+format+"\n", args...) }
func Warn(format string, args ...any) { fmt.Printf("  "+col(cYellow, "!")+" "+format+"\n", args...) }
func Err(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "  "+col(cRed, "✗")+" "+format+"\n", args...)
}
func Info(format string, args ...any) { fmt.Printf("  "+col(cCyan, "→")+" "+format+"\n", args...) }
func Header(s string)                 { fmt.Printf("\n" + col(cBold, s) + "\n") }
func Dim(format string, args ...any) {
	fmt.Printf("  " + col(cGray, fmt.Sprintf(format, args...)) + "\n")
}

func DiffLine(line string) {
	switch {
	case strings.HasPrefix(line, "+"):
		fmt.Println("  " + col(cGreen, line))
	case strings.HasPrefix(line, "-"):
		fmt.Println("  " + col(cRed, line))
	default:
		fmt.Println("  " + col(cGray, line))
	}
}
