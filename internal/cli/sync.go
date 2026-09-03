package cli

import (
	"context"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/output"
)

// newSyncCommand is pull then push over the same adapters, like `git pull` is
// fetch + merge. Edits are captured first; the push that follows sees the
// freshly pulled files as in-sync (pull updates both baselines) and only
// fans new content out to the other targets. All the conflict smarts live in
// the engine's baseline bookkeeping, so sync adds just one rule of its own:
// a failed capture stops the fan-out.
func newSyncCommand() *ucli.Command {
	return &ucli.Command{
		Name:          "sync",
		Category:      "porcelain",
		Usage:         "capture local edits, then fan them to every agent",
		ArgsUsage:     "[adapter...]",
		ShellComplete: completeAdapters,
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "dry-run", Usage: "show both phases without writing"},
			&ucli.BoolFlag{Name: "force", Usage: "resolve every conflict in favor of the incoming side"},
			&ucli.BoolFlag{Name: "no-interactive", Usage: "skip prompts; conflicts are skipped"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			adapters := cmd.Args().Slice()
			dryRun := cmd.Bool("dry-run")
			force := cmd.Bool("force")
			noInteractive := cmd.Bool("no-interactive")

			cfg, err := loadUserOrDefault()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if len(adapters) > 0 {
				if _, err := cfg.SelectAdapters(adapters); err != nil {
					output.Err("%v", err)
					return ucli.Exit("", 1)
				}
			}

			output.Header("sync: pull")
			var pullCode int
			if noInteractive {
				pullCode = pullBatch(cfg, adapters, dryRun, force)
			} else {
				pullCode = pullPerAdapter(cfg, adapters, dryRun, force)
			}
			// A failed capture leaves the adapters it never reached still
			// holding uncaptured local edits. Pushing the store over them is
			// what would destroy those edits — normally drift detection turns
			// that into a conflict, but under --force the push overwrites them
			// with no prompt. Pull is idempotent, so re-running sync completes
			// the capture; fanning out first is the only unrecoverable move.
			if pullCode != 0 {
				output.Err("capture failed — not pushing; fix the above and re-run `friday sync`")
				return exitFromCode(pullCode)
			}

			output.Header("sync: push")
			if dryRun {
				output.Dim("(dry-run: the push plan below is computed before any pull applies)")
			}
			pushCode := runPush(cfg, adapters, pushOpts{
				dryRun:        dryRun,
				force:         force,
				noInteractive: noInteractive,
			})

			return exitFromCode(pushCode)
		},
	}
}
