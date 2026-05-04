// Package conflict implements the interactive resolution UI used by push
// and pull when a file has drifted from what friday last wrote.
package conflict

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

type Choice int

const (
	// ChoiceKeep keeps the canonical version (write source → dest).
	ChoiceKeep Choice = iota
	// ChoiceTake keeps the destination version (do not overwrite).
	ChoiceTake
	// ChoiceSkip leaves both sides untouched.
	ChoiceSkip
)

// Prompt asks the user how to resolve a conflict between canonical and dest.
// labelCanonical and labelDest describe the two sides for the diff view.
//
// Returns ChoiceSkip on EOF or unrecognised input after retries.
func Prompt(labelCanonical, labelDest string, canonical, dest []byte) Choice {
	return promptIO(os.Stdin, os.Stdout, labelCanonical, labelDest, canonical, dest)
}

func promptIO(in io.Reader, out io.Writer, labelCanonical, labelDest string, canonical, dest []byte) Choice {
	r := bufio.NewReader(in)
	for attempts := 0; attempts < 5; attempts++ {
		fmt.Fprintf(out, "  [k] keep canonical   [t] use target   [d] show diff   [s] skip\n  > ")
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return ChoiceSkip
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "k", "keep":
			return ChoiceKeep
		case "t", "take", "target":
			return ChoiceTake
		case "s", "skip", "":
			return ChoiceSkip
		case "d", "diff":
			renderDiff(out, labelCanonical, labelDest, canonical, dest)
		default:
			fmt.Fprintf(out, "  unrecognised choice; expected k/t/d/s\n")
		}
	}
	return ChoiceSkip
}

func renderDiff(out io.Writer, labelA, labelB string, a, b []byte) {
	fmt.Fprintf(out, "\n  --- %s\n  +++ %s\n", labelA, labelB)
	for _, op := range LineDiff(a, b) {
		fmt.Fprintf(out, "  %s\n", op)
	}
	fmt.Fprintln(out)
}

// LineDiff returns a unified-style line diff of a and b. Each line is
// prefixed with "  " (context), "- " (only in a), or "+ " (only in b).
// Uses LCS so single insertions don't cascade into reporting every later
// line as changed.
func LineDiff(a, b []byte) []string {
	return lcsDiff(splitLines(string(a)), splitLines(string(b)))
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Trailing newline produces an empty final element — drop it for a cleaner diff.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lcsDiff produces a simple line-by-line diff using LCS. Output lines are
// prefixed with " ", "-", or "+".
func lcsDiff(a, b []string) []string {
	la, lb := len(a), len(b)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
	}
	for i := la - 1; i >= 0; i-- {
		for j := lb - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < la && j < lb {
		switch {
		case a[i] == b[j]:
			out = append(out, "  "+a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	for ; i < la; i++ {
		out = append(out, "- "+a[i])
	}
	for ; j < lb; j++ {
		out = append(out, "+ "+b[j])
	}
	return out
}
