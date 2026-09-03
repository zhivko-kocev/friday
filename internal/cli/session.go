package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/session"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

// newSessionCommand is the advanced, single-item counterpart to `sessions`
// — the same split as `status` (porcelain, every adapter) versus `doctor
// <file>` (advanced, one mapping).
func newSessionCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "session",
		Category: "advanced",
		Usage:    "inspect or export one session (see also: sessions)",
		Commands: []*ucli.Command{
			newSessionShowCommand(),
			newSessionExportCommand(),
		},
		Action: func(_ context.Context, _ *ucli.Command) error {
			output.Err("usage: friday session <show|export> <id>")
			return ucli.Exit("", 1)
		},
	}
}

// agentScopeFlag narrows the index an <id> is resolved against. Without it
// every show/export rebuilds the full cross-agent index, which asks every
// reader for its whole session list — including the one that shells out to
// the external `opencode` binary — to resolve a single ID.
func agentScopeFlag() ucli.Flag {
	return &ucli.StringFlag{
		Name:  "agent",
		Usage: "resolve the id against only this agent (" + strings.Join(session.Names(), ", ") + ")",
	}
}

func newSessionShowCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "show",
		Usage:     "normalized summary and turn list for one session",
		ArgsUsage: "<id>",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "json", Usage: "machine-readable output"},
			agentScopeFlag(),
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			tr, err := loadSessionArg(cmd)
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if cmd.Bool("json") {
				return printJSON(tr)
			}
			printTranscript(tr)
			return nil
		},
	}
}

func newSessionExportCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "export",
		Usage:     "write a deterministic markdown handoff brief for one session",
		ArgsUsage: "<id>",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "out", Usage: "output file path (default ./friday-handoff-<agent>-<id8>.md)"},
			&ucli.BoolFlag{Name: "force", Usage: "overwrite an existing output file without prompting"},
			&ucli.BoolFlag{Name: "no-interactive", Usage: "don't prompt; refuse rather than overwrite an existing file"},
			agentScopeFlag(),
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			tr, err := loadSessionArg(cmd)
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			out := cmd.String("out")
			if out == "" {
				out = defaultExportPath(tr)
			}
			if err := writeExport(out, []byte(renderHandoff(tr)), cmd.Bool("force"), cmd.Bool("no-interactive")); err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			output.OK("wrote %s", out)
			return nil
		},
	}
}

// writeExport installs a rendered brief at path.
//
// A brief is written 0o600, not world-readable: it embeds raw transcript
// text — user prompts, file contents, anything the user pasted, including
// credentials — and by default it lands in the current directory, which is
// normally a git work tree.
//
// --out names an arbitrary path, so `session export <id> --out README.md`
// must never be allowed to silently truncate that file: an existing target
// is overwritten only with consent (--force, or an interactive yes), and
// --no-interactive refuses rather than prompting. The write itself goes
// through atomicio so a crash or ENOSPC cannot leave a half-written file
// where a whole one used to be, and the previous content is journaled as a
// snapshot so `friday rollback` can bring it back — the same contract every
// other write-capable command honors.
func writeExport(path string, content []byte, force, noInteractive bool) error {
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !force {
			if noInteractive {
				return fmt.Errorf("%s already exists — pass --force to overwrite it", path)
			}
			if !confirmYesNo(fmt.Sprintf("%s already exists — overwrite it?", path)) {
				return fmt.Errorf("aborted — %s left unchanged", path)
			}
		}
	case os.IsNotExist(err):
		old = nil // a create, not an overwrite: rollback deletes it
	default:
		return err
	}
	if err := atomicio.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	recordExportSnapshot(path, old, content)
	return nil
}

// recordExportSnapshot journals the export so `friday rollback` can undo
// it. Best-effort, like every other command's snapshot: a failed journal
// warns but never fails a write that already succeeded.
func recordExportSnapshot(path string, old, content []byte) {
	dir, err := snapshot.Dir()
	if err != nil {
		output.Warn("snapshot skipped: %v", err)
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		output.Warn("snapshot skipped: %v", err)
		return
	}
	snap, err := snapshot.Record(dir, []snapshot.FileWrite{{Adapter: "session", Path: abs, Old: old, New: content}})
	if err != nil {
		output.Warn("snapshot failed: %v", err)
		return
	}
	output.Dim("snapshot %s recorded (`friday rollback` restores it)", snap.ID)
}

// loadSessionArg resolves both show's and export's single positional <id>
// against the cross-agent index (a session is addressable by ID regardless
// of the sessions command's own --project/--since filters, which only
// narrow the list view) and loads its transcript. --agent narrows which
// readers are asked at all.
func loadSessionArg(cmd *ucli.Command) (*session.Transcript, error) {
	if cmd.Args().Len() != 1 {
		return nil, fmt.Errorf("usage: friday session %s <id>", cmd.Name)
	}
	agent := cmd.String("agent")
	if err := validateAgentName(agent); err != nil {
		return nil, err
	}
	id := cmd.Args().Get(0)
	entries, unavailable := buildIndex(agent)
	entry, err := resolveID(entries, id)
	if err != nil {
		// A miss is far more often "that agent isn't readable here" than a
		// wrong id, so name the readers that contributed nothing.
		printUnavailable(unavailable)
		return nil, err
	}
	return entry.reader.Load(entry.ID)
}

func printTranscript(tr *session.Transcript) {
	output.Header(fmt.Sprintf("%s session %s", tr.Agent, tr.ID))
	output.Line(output.LevelInfo, "model:   %s", valueOr(tr.Model, "(unknown)"))
	output.Line(output.LevelInfo, "project: %s", valueOr(tr.Project, "(unknown)"))
	output.Line(output.LevelInfo, "started: %s", tr.Started.Format(time.RFC3339))
	output.Line(output.LevelInfo, "updated: %s", tr.Updated.Format(time.RFC3339))
	output.Line(output.LevelInfo, "turns:   %d", len(tr.Turns))
	if tr.TotalTokens > 0 {
		output.Line(output.LevelInfo, "tokens:  in=%d out=%d cache_creation=%d cache_read=%d total=%d",
			tr.InputTokens, tr.OutputTokens, tr.CacheCreationTokens, tr.CacheReadTokens, tr.TotalTokens)
	}
	if tr.CostKnown {
		output.Line(output.LevelInfo, "cost:    $%.4f", tr.CostUSD)
	}
	if tr.UnreadableLines > 0 {
		output.Warn("%d unreadable line(s) in this transcript", tr.UnreadableLines)
	}
	output.Header("turns:")
	for i, t := range tr.Turns {
		label := t.Role
		if t.Tool != "" {
			label = fmt.Sprintf("%s[%s]", t.Role, t.Tool)
		}
		output.Line(output.LevelInfo, "%3d. %-16s %s", i+1, label, firstLine(t.Text, 200))
	}
}

func defaultExportPath(tr *session.Transcript) string {
	return fmt.Sprintf("./friday-handoff-%s-%s.md", tr.Agent, shortID(tr.ID, 8))
}

// renderHandoff builds the fixed-heading markdown brief. No model call:
// "What was done" is literal per-turn text (first line, truncated), never
// an LLM-generated summary, so the same transcript always exports
// byte-identically — which is also why every timestamp here is RFC3339
// (offset included) rather than normalized to the machine's local zone.
func renderHandoff(tr *session.Transcript) string {
	var b strings.Builder
	b.WriteString("# Session handoff\n\n")

	b.WriteString("## Environment\n\n")
	fmt.Fprintf(&b, "- Agent: %s\n", tr.Agent)
	fmt.Fprintf(&b, "- Model: %s\n", valueOr(tr.Model, "unknown"))
	fmt.Fprintf(&b, "- Project: %s\n", valueOr(tr.Project, "unknown"))
	fmt.Fprintf(&b, "- Started: %s\n", tr.Started.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Ended: %s\n\n", tr.Updated.Format(time.RFC3339))

	b.WriteString("## What was done\n\n")
	wrote := false
	for _, t := range tr.Turns {
		if t.Role != "assistant" || strings.TrimSpace(t.Text) == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", firstLine(t.Text, 200))
		wrote = true
	}
	if !wrote {
		b.WriteString("- (no assistant turns recorded)\n")
	}
	b.WriteString("\n")

	b.WriteString("## Files changed\n\n")
	if len(tr.Files) == 0 {
		b.WriteString("- (none recorded)\n")
	} else {
		for _, f := range tr.Files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	b.WriteString("\n")

	b.WriteString("## Commands run\n\n")
	if len(tr.Tools) == 0 {
		b.WriteString("- (none recorded)\n")
	} else {
		for _, t := range tr.Tools {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	b.WriteString("\n")

	b.WriteString("## Open threads\n\n")
	b.WriteString(openThreads(tr.Turns))
	b.WriteString("\n")

	b.WriteString("## Transcript tail\n\n")
	tail := tr.Turns
	if len(tail) > 20 {
		tail = tail[len(tail)-20:]
	}
	for _, t := range tail {
		label := t.Role
		if t.Tool != "" {
			label += "[" + t.Tool + "]"
		}
		fmt.Fprintf(&b, "**%s** (%s):\n%s\n\n", label, t.At.Format(time.RFC3339), t.Text)
	}

	return b.String()
}

// openThreads reports the trailing user turn (the assistant never replied)
// or a trailing tool call (the tool never got a chance to report back) —
// the two shapes a session can end on that leave real work outstanding. A
// session that ended on a normal assistant turn says so explicitly rather
// than leaving the heading looking broken with nothing under it.
func openThreads(turns []session.Turn) string {
	if len(turns) == 0 {
		return "- (no turns recorded)\n"
	}
	last := turns[len(turns)-1]
	switch {
	case last.Role == "user":
		return fmt.Sprintf("- Pending user request: %s\n", firstLine(last.Text, 200))
	case last.Tool != "":
		return fmt.Sprintf("- Unfinished tool call: %s\n", last.Tool)
	default:
		return "- None -- session ended on a completed assistant turn.\n"
	}
}
