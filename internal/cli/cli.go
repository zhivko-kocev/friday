// Package cli is the friday command dispatcher, built on urfave/cli/v3.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/upgrade"
)

func init() {
	// friday's own wording ("friday <version>"), not urfave's default
	// "<name> version <version>".
	ucli.VersionPrinter = func(cmd *ucli.Command) {
		fmt.Fprintln(cmd.Root().Writer, "friday "+cmd.Root().Version)
	}
	// Root help has two distinct entry points in urfave: the "Incorrect
	// Usage" / missing-required-flag branches call ShowRootCommandHelp
	// directly, but the ordinary `-h`/`--help` flag path (checkHelp) always
	// calls ShowSubcommandHelp — even on the root command. Overriding only
	// ShowRootCommandHelp would leave `friday -h`/`friday --help` (the
	// common case) on urfave's generic template, so both are overridden:
	// the root gets friday's own two-tier banner, any real subcommand keeps
	// urfave's generated per-command help (accurate to its actual Flags).
	//
	// Both read the already-parsed root `--all` so `friday --help --all`
	// selects the full manual, exactly as `friday help --all` does.
	ucli.ShowRootCommandHelp = func(cmd *ucli.Command) error {
		renderUsage(cmd, cmd.Bool("all"))
		return nil
	}
	ucli.ShowSubcommandHelp = func(cmd *ucli.Command) error {
		if cmd.Root() == cmd {
			renderUsage(cmd, cmd.Bool("all"))
			return nil
		}
		return ucli.DefaultShowSubcommandHelp(cmd)
	}
}

// Run is the CLI entry point. main passes os.Args[1:] and the build-time
// version stamp.
//
// It never calls os.Exit itself: the root command's ExitErrHandler is a
// no-op, which stops urfave's default HandleExitCoder from exiting the
// process out from under Run's caller. That default behavior is what a
// typical urfave app wants (Command.Run exits directly), but it would kill
// the test binary on the first exit-coded command — Run must always return
// an int so tests can call it in-process, exactly as the pre-migration
// dispatcher did.
func Run(args []string, ver string) int {
	// The root output flags live in process globals, and urfave/cli/v3 applies
	// a flag only when it is present in argv — there is no "back to default"
	// pass the way the pre-migration flag.BoolVar registration implicitly
	// gave. So Run restores them on the way out: one invocation passing
	// --quiet or --verbose must not change how the next one behaves, in a test
	// binary, a `go run` loop, or any embedder.
	defer func(wasQuiet, wasVerbose bool) {
		output.SetQuiet(wasQuiet)
		verbose = wasVerbose
	}(output.Quiet(), verbose)
	// Deliberately an entry reset, not a restore: the stale-manifest warning
	// is once *per invocation*, so every Run has to be able to print it again.
	manifestWarningsShown = false
	if exePath, err := os.Executable(); err == nil {
		upgrade.CleanupStale(exePath)
	}
	// The nudge printed while rewriting a retired flag spelling has to wait
	// for urfave to parse --quiet/--no-color, which happens inside root.Run —
	// printing during the rewrite put it on the terminal in full colour on
	// exactly the runs that asked for silence.
	args, deprecated := rewriteDeprecatedFlags(args)
	root := newRootCommand(ver)
	root.Before = announceDeprecations(root.Before, deprecated)
	err := root.Run(context.Background(), append([]string{"friday"}, args...))
	return exitCodeFromErr(err)
}

// announceDeprecations wraps a root Before hook so the deprecated-spelling
// notes print after the output flags are applied — the whole reason the note
// is carried here instead of printed during the rewrite, which happens before
// urfave has parsed anything at all.
func announceDeprecations(before ucli.BeforeFunc, notes []string) ucli.BeforeFunc {
	return func(ctx context.Context, cmd *ucli.Command) (context.Context, error) {
		if before != nil {
			var err error
			if ctx, err = before(ctx, cmd); err != nil {
				return ctx, err
			}
		}
		for _, note := range notes {
			output.Warn("%s", note)
		}
		return ctx, nil
	}
}

// applyOutputFlags maps the parsed root flags onto internal/output's state.
// It runs before any command's own output, and unconditionally — so a flag
// left off argv resets to its default rather than inheriting whatever the
// previous in-process invocation set.
func applyOutputFlags(ctx context.Context, cmd *ucli.Command) (context.Context, error) {
	if cmd.Bool("no-color") {
		output.SetColor(false)
	}
	output.SetQuiet(cmd.Bool("quiet"))
	verbose = cmd.Bool("verbose")
	return ctx, nil
}

// rewriteDeprecatedFlags keeps two retired flag spellings working with a
// one-time nudge, applied before urfave ever sees the tokens: --remote
// (init) was renamed --from-git, and --force (pull) was renamed --all so
// its meaning doesn't collide with --force's "overwrite on drift" sense
// used by every other command.
//
// The rewrite is anchored on the resolved command token, not args[0]:
// --no-color/--quiet/--verbose are documented as working with any command,
// so `friday --no-color pull --force` must still reach pull's shim. `--`
// ends the scan — past it nothing is a command name.
func rewriteDeprecatedFlags(args []string) ([]string, []string) {
	cmd := 0
	for cmd < len(args) && strings.HasPrefix(args[cmd], "-") {
		if args[cmd] == "--" {
			return args, nil
		}
		cmd++
	}
	if cmd == len(args) {
		return args, nil
	}
	var old, canonical, note string
	switch args[cmd] {
	case "init":
		old, canonical, note = "remote", "from-git", "note: --remote is deprecated; use --from-git"
	case "pull":
		old, canonical, note = "force", "all", "note: --force is deprecated on pull; use --all"
	default:
		return args, nil
	}
	rest, hit := renameFlag(args[cmd+1:], old, canonical)
	if !hit {
		note = ""
	}
	out := append(args[:cmd+1:cmd+1], rest...)
	if note == "" {
		return out, nil
	}
	return out, []string{note}
}

// exitCodeFromErr converts urfave's returned error into a process exit code.
// ExitCoder errors (cli.Exit(...)) carry an explicit code and an optional
// message printed here — empty for the "framework does the exiting, report
// already printed its own output" case. Any other error is a usage/parse
// failure urfave has already printed to stderr itself, so it maps to 1
// without a second message.
func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	if ec, ok := err.(ucli.ExitCoder); ok {
		if msg := ec.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
		return ec.ExitCode()
	}
	return 1
}

// newRootCommand builds the full command graph. Every subcommand is defined
// in its own file (init.go, push.go, ...) via a newXCommand() constructor;
// this is the single place they're wired together, so dispatch, help, and
// completion all read from one list — nothing can drift the way a
// hand-maintained table could.
func newRootCommand(ver string) *ucli.Command {
	root := &ucli.Command{
		Name:                  "friday",
		Version:               ver,
		EnableShellCompletion: true,
		ShellComplete:         completeRoot,
		// urfave's injected `completion` command has no Action of its own,
		// so a missing or misspelled shell name falls through to its help
		// page — exit 0 with a help page on stdout, which the documented
		// `friday completion bash > /etc/bash_completion.d/friday` install
		// pattern would happily write to disk and source at every login.
		ConfigureShellCompletionCommand: configureCompletionCommand,
		HideHelpCommand:                 true, // we register our own "help" (supports --all)
		ExitErrHandler:                  func(context.Context, *ucli.Command, error) {},
		// The three output flags are applied in Before, not through per-flag
		// Actions: urfave runs Before ahead of runFlagActions, so an Action
		// lands too late to mute anything Before or an early command line
		// prints, and it fires only when the flag is actually present — which
		// is what left --quiet/--verbose stuck across in-process invocations.
		// Reading the parsed values in one place has neither problem.
		Before: applyOutputFlags,
		Flags: []ucli.Flag{
			&ucli.BoolFlag{
				Name:  "no-color",
				Local: false, // inherited by every subcommand
				Usage: "Disable colored output (also: NO_COLOR or FRIDAY_NO_COLOR env)",
			},
			&ucli.BoolFlag{
				Name:  "quiet",
				Local: false,
				Usage: "Suppress all non-error output (exit codes unchanged)",
			},
			&ucli.BoolFlag{
				Name:  "verbose",
				Local: false,
				Usage: "Show per-file decisions and the resolved source for each rendered path",
			},
			&ucli.BoolFlag{
				// `friday --help --all` selected the full manual before the
				// urfave migration remapped --help away from the help
				// command. Local so it never shadows `pull --all`, and
				// hidden because `friday help --all` is the spelling the
				// footer advertises.
				Name:    "all",
				Aliases: []string{"a"}, // `friday -h -a` worked before the migration
				Local:   true,
				Hidden:  true,
				Usage:   "with --help: show advanced commands, flag details, and examples too",
				Action: func(_ context.Context, _ *ucli.Command, v bool) error {
					if v {
						return ucli.Exit("--all only applies to help; did you mean `friday help --all`?", 1)
					}
					return nil
				},
			},
		},
		Action: rootAction,
	}
	root.Commands = []*ucli.Command{
		newInitCommand(),
		newClaimCommand(),
		newSetupCommand(),
		newSyncCommand(),
		newStatusCommand(),
		newSessionsCommand(),
		newShareCommand(),

		newPushCommand(),
		newPullCommand(),
		newPromoteCommand(),
		newTransferCommand(),
		newSourceCommand(),
		newSessionCommand(),
		newRollbackCommand(),
		newEjectCommand(),
		newRemoteCommand(),
		newDoctorCommand(),
		newUpgradeCommand(),
		newMigrateCommand(),
		newVersionCommand(),
		newHelpCommand(),
		newCompleteCommand(),
	}
	return root
}

// rootAction fires when no subcommand name was resolved: both a truly bare
// `friday` and an unrecognized command name land here (urfave routes an
// unmatched leading token to the parent's own Action rather than erroring),
// matching the pre-v1 dispatcher exactly — usage plus exit 1 either way,
// with one extra identifying line for the unmatched-name case. The last
// line is state-aware (D29): firstRunHint names the one concrete next step
// for wherever this install currently stands.
func rootAction(_ context.Context, cmd *ucli.Command) error {
	if rest := cmd.Args().Slice(); len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "friday: unknown command %q\n\n", rest[0])
	}
	// --quiet promises "all non-error output" is suppressed, exit code
	// unchanged, and this is the most reachable place that promise mattered:
	// a bare or misspelled invocation in a script wants the code, not a
	// multi-line banner on stdout. The unknown-command line above is the
	// error half and stays.
	if !output.Quiet() {
		renderUsage(cmd, false)
		if hint := firstRunHint(); hint != "" {
			fmt.Println(hint)
		}
	}
	return ucli.Exit("", 1)
}

// firstRunHint computes the one-line, state-aware suggestion appended after
// bare usage: no store yet points at init/claim, an existing store with
// anything a `friday sync` would actually change points there, and an
// already-synced store returns "" — there's no concrete next action to name.
// Every failure along the way (can't resolve the store dir, a broken
// manifest, a dry-run push error) also degrades to "": this is a one-line
// nudge computed from state friday already loads elsewhere, never a reason
// to fail or complicate the bare-invocation path.
func firstRunHint() string {
	// initcmd.NeedsInit is the single definition of "empty enough to scaffold
	// or clone into", and it is what `init` and `claim` themselves use. A bare
	// directory-exists check answered "you have a store" for a present-but-
	// empty ~/.friday — a CI volume mount, or any mkdir before the first
	// init — and then had nothing to suggest.
	storeDir, err := config.UserStoreDir()
	if err != nil {
		return ""
	}
	needsInit, err := initcmd.NeedsInit(storeDir)
	if err != nil {
		return ""
	}
	if needsInit {
		return "No store yet: run `friday init` to create one, or `friday claim` to adopt what's already installed."
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		return ""
	}
	// Plan exactly what `friday sync` would push: runPush with no adapter
	// args targets installedAdapters(cfg) only. Planning every *configured*
	// adapter instead would count the presets this machine has no agent for
	// as permanently pending, so the hint would nag on every invocation and
	// no amount of syncing could ever clear it.
	adapters := installedAdapters(cfg)
	if len(adapters) == 0 {
		return ""
	}
	changes, err := engine.Push(cfg, engine.Options{Adapters: adapters, DryRun: true})
	if err != nil {
		return ""
	}
	// A conflict is NOT clearable by `friday sync`: sync exits 2 and leaves
	// the drifted file alone, so pointing at it would nag on every
	// invocation with no way out. Name the resolution instead.
	pending := false
	for _, ch := range changes {
		switch ch.Action {
		case engine.ActionConflict:
			return "Drift detected: run `friday status` to see it, then `friday sync` and choose per file (or `friday push --force` to overwrite)."
		case engine.ActionCreate, engine.ActionUpdate:
			pending = true
		}
	}
	if pending {
		return "Pending changes: run `friday sync` to apply them."
	}
	return ""
}

func newVersionCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "version",
		Category: "advanced",
		Usage:    "print version",
		Action: func(_ context.Context, cmd *ucli.Command) error {
			fmt.Println("friday " + cmd.Root().Version)
			return nil
		},
	}
}
