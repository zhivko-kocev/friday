package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdPull captures edits from agent dirs back into ~/.friday.
//
// `friday pull`           → walk every installed agent; show diff; ask apply / skip / quit
// `friday pull claude`    → legacy all-at-once flow with the per-file conflict resolver
// `friday pull --no-interactive` → batch flow, no prompts at all
func cmdPull(args []string) int {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	force := fs.Bool("force", false, "auto-apply every adapter (skip the prompt)")
	noInteractive := fs.Bool("no-interactive", false, "skip prompts; legacy batch flow")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	if len(fs.Args()) > 0 || *noInteractive {
		return pullBatch(cfg, fs.Args(), *dryRun, *force, !*noInteractive)
	}
	return pullPerAdapter(cfg, *dryRun, *force)
}

// pullBatch is the legacy single-pass flow. Used when the caller names
// adapters explicitly or opts out of prompts (`--no-interactive`).
func pullBatch(cfg *config.Config, adapters []string, dryRun, force, interactive bool) int {
	opts := engine.Options{
		Adapters: adapters,
		DryRun:   dryRun,
		Force:    force,
	}
	if interactive {
		opts.OnConflict = interactiveResolver()
	}
	changes, err := engine.Pull(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	report(changes, false, dryRun)
	return exitCode(changes)
}

// pullPerAdapter walks every installed adapter, prints what would change,
// and asks whether to apply. Each adapter is independent — quit stops the
// loop, skip moves on, apply runs engine.Pull for just that one.
func pullPerAdapter(cfg *config.Config, dryRun, force bool) int {
	installed := installedAdapters(cfg)
	if len(installed) == 0 {
		output.Warn("no installed agents detected — nothing to pull")
		return 0
	}

	reader := bufio.NewReader(os.Stdin)
	var seen []engine.Change

	for _, name := range installed {
		planned, err := engine.Pull(cfg, engine.Options{
			Adapters: []string{name},
			DryRun:   true,
		})
		if err != nil {
			output.Err("plan %s: %v", name, err)
			return 1
		}
		if !hasPullWork(planned) {
			output.Dim("adapter %s — no changes", name)
			continue
		}

		// Always render with diffs; the whole point of this flow is the user
		// sees what they're approving before saying yes.
		report(planned, true, true)

		if dryRun {
			seen = append(seen, planned...)
			continue
		}

		choice := "a"
		if !force {
			choice = promptApplyChoice(reader)
		}
		switch choice {
		case "a":
			applied, err := engine.Pull(cfg, engine.Options{Adapters: []string{name}})
			if err != nil {
				output.Err("apply %s: %v", name, err)
				return 1
			}
			output.OK("applied %s", name)
			seen = append(seen, applied...)
		case "s":
			output.Dim("skipped %s", name)
		case "q":
			output.Dim("quit")
			return exitCode(seen)
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

// promptApplyChoice reads one of a/s/q from the user. Anything else (or EOF)
// is treated as quit so a piped stdin doesn't accidentally apply edits.
func promptApplyChoice(r *bufio.Reader) string {
	for attempts := 0; attempts < 5; attempts++ {
		fmt.Print("  [a]pply  [s]kip  [q]uit > ")
		line, err := r.ReadString('\n')
		if err != nil {
			return "q"
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "a", "apply", "y", "yes":
			return "a"
		case "s", "skip", "n", "no":
			return "s"
		case "q", "quit", "":
			return "q"
		default:
			fmt.Println("  unrecognised — type a / s / q")
		}
	}
	return "q"
}
