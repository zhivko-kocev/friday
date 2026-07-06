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
	// ChoiceMerge writes 3-way-merged content; Prompt returns it alongside.
	ChoiceMerge
)

// Prompt asks the user how to resolve a conflict between canonical and dest.
// labelCanonical and labelDest describe the two sides for the diff view.
// base is the last-synced content both sides diverged from; when non-nil the
// prompt offers [m]erge and returns the merged bytes with ChoiceMerge.
//
// Returns ChoiceSkip on EOF or unrecognised input after retries.
func Prompt(labelCanonical, labelDest string, canonical, dest, base []byte) (Choice, []byte) {
	return promptIO(os.Stdin, os.Stdout, labelCanonical, labelDest, canonical, dest, base)
}

func promptIO(in io.Reader, out io.Writer, labelCanonical, labelDest string, canonical, dest, base []byte) (Choice, []byte) {
	r := bufio.NewReader(in)
	// Worded from the labels so the menu stays truthful in both directions:
	// on pull the incoming (canonical) side is the target and the dest is
	// the store — a hardcoded "keep canonical [k]" would promise the exact
	// opposite of what ChoiceKeep does there.
	merge := ""
	if base != nil {
		merge = "[m] merge   "
	}
	options := fmt.Sprintf("  [k] keep %s   [t] use %s   %s[d] show diff   [s] skip", labelCanonical, labelDest, merge)
	for range 5 {
		fmt.Fprintf(out, "%s\n  > ", options)
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return ChoiceSkip, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "k", "keep":
			return ChoiceKeep, nil
		case "t", "take", "target":
			return ChoiceTake, nil
		case "s", "skip", "":
			return ChoiceSkip, nil
		case "m", "merge":
			if base == nil {
				fmt.Fprintf(out, "  no merge base available for this file\n")
				continue
			}
			merged, clean := Merge(base, canonical, dest, labelCanonical, labelDest)
			if clean {
				fmt.Fprintf(out, "  merged cleanly\n")
				return ChoiceMerge, merged
			}
			if promptYes(r, out, "  edits overlap — write with conflict markers? [y/N] > ") {
				return ChoiceMerge, merged
			}
		case "d", "diff":
			renderDiff(out, labelCanonical, labelDest, canonical, dest)
		default:
			fmt.Fprintf(out, "  unrecognised choice; expected k/t/m/d/s\n")
		}
	}
	return ChoiceSkip, nil
}

func promptYes(r *bufio.Reader, out io.Writer, msg string) bool {
	fmt.Fprint(out, msg)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
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

// lcsTable builds the suffix LCS-length table shared by lcsDiff and
// lcsPairs (merge.go).
func lcsTable(a, b []string) [][]int {
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
	return dp
}

// lcsDiff produces a simple line-by-line diff using LCS. Output lines are
// prefixed with " ", "-", or "+".
func lcsDiff(a, b []string) []string {
	la, lb := len(a), len(b)
	dp := lcsTable(a, b)
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
