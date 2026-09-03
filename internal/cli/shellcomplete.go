package cli

import (
	"context"
	"fmt"
	"strings"

	ucli "github.com/urfave/cli/v3"
)

// completeCommandName is the hidden callback every friday completion script
// published before v1.0 shells out to. Those scripts are installed to
// persistent locations (/etc/bash_completion.d/friday, ${fpath[1]}/_friday,
// ~/.config/fish/completions/friday.fish), so they outlive the binary that
// wrote them: without this command an upgraded friday answers every
// `friday <TAB>` with its usage banner, and the shell offers "Usage:",
// "Store:" and "~/.friday" as candidates.
const completeCommandName = "__complete"

// completionCommandName is urfave's injected script-emitting command. It is
// Hidden, so urfave's own completer never offers it even though it is a
// real, documented command.
const completionCommandName = "completion"

// newCompleteCommand implements the pre-v1.0 callback protocol: no argument
// lists every command name and alias, one per line; a command name lists
// that command's flags, subcommands, and (where its positionals are adapter
// names) the live adapter list.
func newCompleteCommand() *ucli.Command {
	return &ucli.Command{
		Name:            completeCommandName,
		Hidden:          true,
		HideHelp:        true,
		SkipFlagParsing: true,
		Usage:           "internal completion callback for pre-v1.0 shell completion scripts",
		Action: func(ctx context.Context, cmd *ucli.Command) error {
			root := cmd.Root()
			for _, w := range completionsFor(ctx, root, cmd.Args().First()) {
				fmt.Fprintln(root.Writer, w)
			}
			return nil
		},
	}
}

// completionsFor answers one __complete query. Everything is read from the
// live command graph, so it cannot drift from what actually dispatches.
func completionsFor(ctx context.Context, root *ucli.Command, name string) []string {
	if name == "" {
		var out []string
		for _, c := range completableCommands(root) {
			out = append(out, c.Name)
			out = append(out, c.Aliases...)
		}
		return out
	}
	for _, c := range completableCommands(root) {
		if !c.HasName(name) {
			continue
		}
		var out []string
		for _, f := range c.Flags {
			if vf, ok := f.(ucli.VisibleFlag); ok && !vf.IsVisible() {
				continue
			}
			out = append(out, "--"+f.Names()[0])
		}
		for _, sub := range c.Commands {
			if !sub.Hidden {
				out = append(out, sub.Name)
			}
		}
		if c.ShellComplete != nil {
			out = append(out, captureCompletions(ctx, c)...)
		}
		return out
	}
	return nil
}

// captureCompletions runs a command's own dynamic completer and collects
// what it writes, so __complete offers the same live values (adapter names)
// that the modern --generate-shell-completion path does.
func captureCompletions(ctx context.Context, c *ucli.Command) []string {
	var buf strings.Builder
	root := c.Root()
	saved := root.Writer
	root.Writer = &buf
	c.ShellComplete(ctx, c)
	root.Writer = saved
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// completeRoot is the root's ShellComplete. urfave's default prints
// command.Name only and skips every Hidden command, which drops the `undo`
// alias and the `completion` command — both completable before the
// migration. Emitting them here keeps the modern protocol at parity with
// the one it replaced.
func completeRoot(ctx context.Context, cmd *ucli.Command) {
	args := cmd.Args().Slice()
	if len(args) > 0 && strings.HasPrefix(args[len(args)-1], "-") {
		ucli.DefaultCompleteWithFlags(ctx, cmd)
		return
	}
	root := cmd.Root()
	for _, c := range completableCommands(root) {
		fmt.Fprintf(root.Writer, "%s:%s\n", c.Name, c.Usage)
		for _, alias := range c.Aliases {
			fmt.Fprintf(root.Writer, "%s:%s\n", alias, c.Usage)
		}
	}
}

// completableCommands is every top-level command a user can type. The
// injected `completion` command is Hidden for urfave's own bookkeeping but
// is documented in `friday help --all`, so it is kept; __complete is a
// script-only callback, so it is dropped.
func completableCommands(root *ucli.Command) []*ucli.Command {
	var out []*ucli.Command
	for _, c := range root.Commands {
		if c.Name == completeCommandName {
			continue
		}
		if c.Hidden && c.Name != completionCommandName {
			continue
		}
		out = append(out, c)
	}
	return out
}

// configureCompletionCommand gives urfave's injected `completion` command
// friday's own wording and, more importantly, an Action. Without one a
// missing shell name falls through to urfave's help printer: exit 0 with a
// help page on stdout, which `friday completion bash > /etc/bash_completion.d/friday`
// with a forgotten argument would write to disk and source at every login.
// An unknown shell name likewise exited 3 with "No help topic for ...",
// a code friday does not otherwise use.
func configureCompletionCommand(c *ucli.Command) {
	c.Usage = completionAdvancedEntry.usage
	c.Action = func(_ context.Context, cmd *ucli.Command) error {
		if shell := cmd.Args().First(); shell != "" {
			return ucli.Exit(fmt.Sprintf("unsupported shell %q (want: bash, zsh, fish, pwsh)", shell), 1)
		}
		return ucli.Exit("usage: friday completion bash|zsh|fish|pwsh", 1)
	}
}

// completeAdapters is the ShellComplete func for commands whose positional
// arguments are adapter names (push, pull, sync, status, claim). It replaces
// the old completesAdapters bool: dynamic values now live next to the
// command that needs them instead of in a side table. Mirrors urfave's own
// DefaultCompleteWithFlags convention — one suggestion per line to the
// command's Writer — falling back to flag suggestions when the last token
// on the line is itself a flag in progress.
//
// cmd.Args() is nil when __complete invokes this on a command urfave never
// parsed, which is the "no words typed yet" case: offer every adapter.
func completeAdapters(ctx context.Context, cmd *ucli.Command) {
	last := ""
	if args := cmd.Args(); args != nil && args.Len() > 0 {
		last = args.Get(args.Len() - 1)
	}
	if strings.HasPrefix(last, "-") {
		ucli.DefaultCompleteWithFlags(ctx, cmd)
		return
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		return
	}
	for _, name := range cfg.AdapterNames() {
		fmt.Fprintln(cmd.Root().Writer, name)
	}
}
