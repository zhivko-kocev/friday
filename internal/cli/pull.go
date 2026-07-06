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

type pullOpts struct {
	dryRun, force, noInteractive bool
}

func pullFlags(o *pullOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would change without writing")
	fs.BoolVar(&o.force, "force", false, "auto-apply every adapter (skip the prompt)")
	fs.BoolVar(&o.noInteractive, "no-interactive", false, "skip prompts; legacy batch flow")
	return fs
}

// cmdPull captures edits from agent dirs back into ~/.friday.
//
// `friday pull`           → walk every installed agent; show diff; ask apply / skip / quit
// `friday pull claude`    → same per-adapter flow, restricted to the named adapters
// `friday pull --no-interactive` → batch flow, no prompts at all
func cmdPull(args []string) int {
	var o pullOpts
	fs := pullFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	if o.noInteractive {
		return pullBatch(cfg, fs.Args(), o.dryRun, o.force, false)
	}
	return pullPerAdapter(cfg, fs.Args(), o.dryRun, o.force)
}

// pullBatch is the legacy single-pass flow, kept for `--no-interactive`
// (CI / scripts) where per-adapter prompting makes no sense.
func pullBatch(cfg *config.Config, adapters []string, dryRun, force, interactive bool) int {
	opts := engine.Options{
		Adapters: adapters,
		DryRun:   dryRun,
		Force:    force,
	}
	if interactive {
		opts.OnConflict = interactiveResolver()
		opts.BaseLookup = baseLookup()
	}
	changes, err := engine.Pull(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
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
			applied, err := engine.Pull(cfg, engine.Options{
				Adapters:   []string{name},
				OnConflict: interactiveResolver(),
				BaseLookup: baseLookup(),
			})
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
	for range 5 {
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
