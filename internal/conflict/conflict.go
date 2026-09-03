// Package conflict implements the interactive resolution UI used by push
// and pull when a file has drifted from what friday last wrote.
package conflict

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/textdiff"
	"github.com/zhivko-kocev/friday/internal/ui"
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

// Prompt asks the user how to resolve a conflict in destRel between canonical
// and dest. labelCanonical and labelDest describe the two sides for the diff
// view. base is the last-synced content both sides diverged from; when
// non-nil the prompt offers [m]erge and returns the merged bytes with
// ChoiceMerge.
//
// destRel is named in the prompt itself, not left to a preceding progress
// line: keeping canonical discards the target's hand edits, so the question
// must never be asked about an unnamed file (a caller's own "CONFLICT: x"
// line is suppressible by --quiet, this is not).
//
// in is the caller's shared stdin reader. It is passed rather than wrapped
// here because bufio reads ahead: a per-prompt reader swallows the rest of a
// piped answer stream into a buffer it then discards, so every later prompt
// in the same run would see a premature EOF.
//
// Returns ChoiceSkip on EOF or unrecognised input after retries.
func Prompt(in *bufio.Reader, destRel, labelCanonical, labelDest string, canonical, dest, base []byte) (Choice, []byte) {
	if ui.Interactive() {
		return promptTUI(destRel, labelCanonical, labelDest, canonical, dest, base)
	}
	return promptIO(in, os.Stdout, destRel, labelCanonical, labelDest, canonical, dest, base)
}

// promptTUI is the rich-terminal counterpart of promptIO: an arrow-key menu
// with the same choices and the same per-direction wording. "show diff" prints
// a colored diff and re-prompts; "merge" offers conflict markers on overlap.
// A cancelled prompt (ctrl-c / esc) resolves to skip, leaving both sides
// untouched — the safe default for a destructive decision.
func promptTUI(destRel, labelCanonical, labelDest string, canonical, dest, base []byte) (Choice, []byte) {
	const keep, use, merge, diff, skip = "keep", "use", "merge", "diff", "skip"
	for range 5 {
		choices := []ui.Choice{
			{Value: keep, Label: "keep " + labelCanonical},
			{Value: use, Label: "use " + labelDest},
		}
		if base != nil {
			choices = append(choices, ui.Choice{Value: merge, Label: "merge the two"})
		}
		choices = append(choices,
			ui.Choice{Value: diff, Label: "show diff"},
			ui.Choice{Value: skip, Label: "skip (leave both untouched)"},
		)

		got, err := ui.SelectOne("Resolve conflict in "+destRel, choices)
		if err != nil {
			return ChoiceSkip, nil
		}
		switch got {
		case keep:
			return ChoiceKeep, nil
		case use:
			return ChoiceTake, nil
		case skip, "":
			return ChoiceSkip, nil
		case merge:
			merged, clean := textdiff.Merge(base, canonical, dest, labelCanonical, labelDest)
			if clean {
				output.OK("merged cleanly")
				return ChoiceMerge, merged
			}
			if ui.Confirm("edits overlap — write with conflict markers?") {
				return ChoiceMerge, merged
			}
		case diff:
			renderDiffColored(labelCanonical, labelDest, canonical, dest)
		}
	}
	return ChoiceSkip, nil
}

// renderDiffColored prints the same line diff renderDiff produces, but through
// the output helpers so +/- lines are colored on a terminal.
func renderDiffColored(labelA, labelB string, a, b []byte) {
	output.Dim("--- %s", labelA)
	output.Dim("+++ %s", labelB)
	for _, op := range textdiff.LineDiff(a, b) {
		output.DiffLine(op)
	}
}

func promptIO(r *bufio.Reader, out io.Writer, destRel, labelCanonical, labelDest string, canonical, dest, base []byte) (Choice, []byte) {
	// Worded from the labels so the menu stays truthful in both directions:
	// on pull the incoming (canonical) side is the target and the dest is
	// the store — a hardcoded "keep canonical [k]" would promise the exact
	// opposite of what ChoiceKeep does there.
	merge := ""
	if base != nil {
		merge = "[m] merge   "
	}
	options := fmt.Sprintf("  conflict in %s\n  [k] keep %s   [t] use %s   %s[d] show diff   [s] skip", destRel, labelCanonical, labelDest, merge)
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
			merged, clean := textdiff.Merge(base, canonical, dest, labelCanonical, labelDest)
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
	for _, op := range textdiff.LineDiff(a, b) {
		fmt.Fprintf(out, "  %s\n", op)
	}
	fmt.Fprintln(out)
}
