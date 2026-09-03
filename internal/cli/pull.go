package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/ui"
)

// newPullCommand captures edits from agent dirs back into ~/.friday.
//
// `friday pull`           → walk every installed agent; show diff; ask apply / skip / quit
// `friday pull claude`    → same per-adapter flow, restricted to the named adapters
// `friday pull --no-interactive` → batch flow, no prompts at all
func newPullCommand() *ucli.Command {
	return &ucli.Command{
		Name:          "pull",
		Category:      "advanced",
		Usage:         "one-way sync: agent edits → store (--discover finds new files)",
		ArgsUsage:     "[adapter...]",
		ShellComplete: completeAdapters,
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "dry-run", Usage: "show what would change without writing"},
			&ucli.BoolFlag{Name: "all", Usage: "auto-apply every adapter (skip the prompt)"},
			&ucli.BoolFlag{Name: "no-interactive", Usage: "skip prompts; legacy batch flow"},
			&ucli.BoolFlag{Name: "discover", Usage: "walk the agent dir and capture files a normal pull can't see (bootstrap/enrich the store)"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			adapters := cmd.Args().Slice()
			dryRun := cmd.Bool("dry-run")
			all := cmd.Bool("all")
			noInteractive := cmd.Bool("no-interactive")

			cfg, err := loadUserOrDefault()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}

			var code int
			switch {
			case cmd.Bool("discover"):
				code = pullDiscover(cfg, adapters, dryRun, all, noInteractive)
			case noInteractive:
				code = pullBatch(cfg, adapters, dryRun, all)
			default:
				code = pullPerAdapter(cfg, adapters, dryRun, all)
			}
			return exitFromCode(code)
		},
	}
}

// pullDiscover is `pull --discover`: reverse-expand each adapter's rules to
// walk its target dir and capture files a store-driven pull can't see (a skill
// authored directly in ~/.claude/skills/, a hand-added agent). This is how you
// bootstrap or enrich the store from an existing install. Args are adapter
// names or target dirs; none = every installed adapter.
func pullDiscover(cfg *config.Config, args []string, dryRun, force, noInteractive bool) int {
	names := args
	if len(names) == 0 {
		names = installedAdapters(cfg)
		if len(names) == 0 {
			output.Warn("no installed agents detected — nothing to discover")
			return 0
		}
	}
	resolved := make([]string, 0, len(names))
	for _, a := range names {
		name, err := resolveAdapterArg(cfg, a)
		if err != nil {
			output.Err("%v", err)
			return 1
		}
		resolved = append(resolved, name)
	}

	opts := engine.Options{Adapters: resolved, DryRun: dryRun, Force: force}
	if !noInteractive {
		opts.OnConflict = interactiveResolver()
		opts.BaseLookup = baseLookup()
	}
	changes, err := engine.Import(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if !dryRun {
		recordSnapshot(changes)
	}
	report(changes, false, dryRun)
	return exitCode(changes)
}

// pullBatch is the legacy single-pass flow, kept for `--no-interactive`
// (CI / scripts) where per-adapter prompting makes no sense.
func pullBatch(cfg *config.Config, adapters []string, dryRun, force bool) int {
	changes, err := engine.Pull(cfg, engine.Options{
		Adapters:         adapters,
		DryRun:           dryRun,
		Force:            force,
		PulledStorePaths: map[string]bool{},
	})
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if !dryRun {
		recordSnapshot(changes)
	}
	report(changes, false, dryRun)
	return exitCode(changes)
}

// pullPerAdapter walks the given adapters (default: every installed one),
// prints what would change, and asks whether to apply. Each adapter is
// independent — quit stops the loop, skip moves on, apply runs engine.Pull
// for just that one.
func pullPerAdapter(cfg *config.Config, adapters []string, dryRun, force bool) int {
	installed := adapters
	if len(installed) == 0 {
		installed = installedAdapters(cfg)
		if len(installed) == 0 {
			output.Warn("no installed agents detected — nothing to pull")
			return 0
		}
	} else if _, err := cfg.SelectAdapters(installed); err != nil {
		output.Err("%v", err)
		return 1
	}

	var seen []engine.Change

	// One shared provenance map across every per-adapter Pull call: an edit
	// captured from an earlier agent must not read as a removal (or, under
	// --force, silently revert) when a later agent maps the same store file.
	captured := map[string]bool{}

	for _, name := range installed {
		planned, err := engine.Pull(cfg, engine.Options{
			Adapters:         []string{name},
			DryRun:           true,
			PulledStorePaths: captured,
		})
		if err != nil {
			output.Err("plan %s: %v", name, err)
			return 1
		}
		if !hasPullWork(planned) {
			output.Dim("adapter %s — no changes", name)
			continue
		}

		// The plan is the prompt's evidence — answering "a" writes the
		// agent's version over the store — so when we are about to ask, it
		// prints even under --quiet. Always with diffs: the whole point of
		// this flow is that the user sees what they're approving.
		choice := "a"
		show := func() { report(planned, true, true) }
		if dryRun || force {
			show()
		} else {
			output.WithPromptContext(func() {
				show()
				if ui.Interactive() {
					choice = promptApplyChoiceTUI(name)
				} else {
					choice = promptApplyChoice(stdin)
				}
			})
		}

		if dryRun {
			seen = append(seen, planned...)
			continue
		}
		switch choice {
		case "a":
			applied, err := engine.Pull(cfg, engine.Options{
				Adapters:         []string{name},
				Force:            force,
				OnConflict:       interactiveResolver(),
				BaseLookup:       baseLookup(),
				PulledStorePaths: captured,
			})
			if err != nil {
				output.Err("apply %s: %v", name, err)
				return 1
			}
			recordSnapshot(applied)
			output.OK("applied %s", name)
			seen = append(seen, applied...)
		case "s":
			output.Dim("skipped %s", name)
		case "q":
			output.Dim("quit")
			return exitCode(seen)
		case "eof":
			// Piped/closed stdin mid-flow: nothing was (or will be) applied.
			// Exit non-zero so scripts don't read the silence as success.
			output.Warn("stdin closed — nothing applied; use --no-interactive or --force for scripts")
			return 2
		}
	}
	return exitCode(seen)
}

// hasPullWork returns true when the planned change set contains anything
// the user would want to be asked about. In-sync, missing-source, and
// unsupported actions are no-ops.
func hasPullWork(changes []engine.Change) bool {
	for _, ch := range changes {
		switch ch.Action {
		case engine.ActionCreate, engine.ActionUpdate, engine.ActionConflict:
			return true
		}
	}
	return false
}

// promptApplyChoiceTUI is the rich-terminal counterpart of promptApplyChoice:
// an arrow-key list returning the same a/s/q codes. A cancelled prompt
// (ctrl-c / esc) maps to "quit", stopping the walk without applying.
func promptApplyChoiceTUI(name string) string {
	choice, err := ui.SelectOne("Apply pulled changes for "+name+"?", []ui.Choice{
		{Value: "a", Label: "apply"},
		{Value: "s", Label: "skip"},
		{Value: "q", Label: "quit"},
	})
	if err != nil || choice == "" {
		return "q"
	}
	return choice
}

// promptApplyChoice reads one of a/s/q from the user. EOF returns the
// distinct "eof" choice so the caller can exit non-zero — a piped stdin must
// neither apply edits nor masquerade as a clean quit.
func promptApplyChoice(r *bufio.Reader) string {
	for range 5 {
		fmt.Print("  [a]pply  [s]kip  [q]uit > ")
		line, err := r.ReadString('\n')
		if err != nil {
			return "eof"
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "a", "apply", "y", "yes":
			return "a"
		case "s", "skip", "n", "no":
			return "s"
		case "q", "quit", "":
			return "q"
		default:
			fmt.Println("  unrecognized — type [a]pply, [s]kip, or [q]uit")
		}
	}
	return "q"
}
