package cli

import (
	"context"
	"os"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
)

// newClaimCommand captures an agent's existing on-disk config into the
// store — pull --discover plus store scaffolding plus source routing, so a
// machine with agent config friday never wrote (a brand-new machine, or one
// where an agent was configured by hand) can bootstrap or enrich ~/.friday
// from what is already installed, in one step (D17/D19).
//
//	friday claim               → every installed agent, into the primary source
//	friday claim claude codex  → only the named adapters
//	friday claim --to team     → newly captured files land in the "team" source
func newClaimCommand() *ucli.Command {
	return &ucli.Command{
		Name:          "claim",
		Category:      "porcelain",
		Usage:         "capture an agent's existing config into the store",
		ArgsUsage:     "[adapter...]",
		ShellComplete: completeAdapters,
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "to", Usage: "route newly captured files into this configured source (default: primary)"},
			&ucli.BoolFlag{Name: "dry-run", Usage: "show what would be captured without writing"},
			&ucli.BoolFlag{Name: "no-interactive", Usage: "don't prompt on conflicts; treat them as skip"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			dryRun := cmd.Bool("dry-run")
			noInteractive := cmd.Bool("no-interactive")

			storeDir, err := config.UserStoreDir()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			needsInit, err := initcmd.NeedsInit(storeDir)
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if needsInit && !dryRun {
				advisory, err := initcmd.Scaffold(storeDir)
				if err != nil {
					output.Err("%v", err)
					return ucli.Exit("", 1)
				}
				if advisory != "" {
					output.Warn("%s", advisory)
				}
				output.Info("scaffolded a new store at %s", storeDir)
			}

			var cfg *config.Config
			if needsInit {
				// Either freshly scaffolded above, or (--dry-run, which never
				// writes) simulated purely in memory: a fresh scaffold's
				// friday.yaml is always exactly the built-in preset set
				// (initcmd.writeManifest), so this already matches what
				// claim would see either way — no second disk read needed.
				home, _ := os.UserHomeDir()
				cfg = config.NewDefault(config.ScopeUser, storeDir, home, presetAdapters())
			} else {
				cfg, err = loadUserOrDefault()
				if err != nil {
					output.Err("%v", err)
					return ucli.Exit("", 1)
				}
			}

			// engine.destRoot falls back to the primary for a --to it can't
			// resolve, so a typo would silently capture into ~/.friday and
			// only be discoverable by inspecting the store afterwards.
			toSource := cmd.String("to")
			if toSource != "" && toSource != "local" && sourceIndex(cfg, toSource) < 0 {
				output.Err("unknown source %q — `friday source list` shows the configured ones; omit --to for the primary store", toSource)
				return ucli.Exit("", 1)
			}

			names := cmd.Args().Slice()
			if len(names) == 0 {
				names = installedAdapters(cfg)
				if len(names) == 0 {
					output.Warn("no installed agents detected — nothing to claim")
					return nil
				}
			}
			resolved := make([]string, 0, len(names))
			for _, a := range names {
				name, err := resolveAdapterArg(cfg, a)
				if err != nil {
					output.Err("%v", err)
					return ucli.Exit("", 1)
				}
				resolved = append(resolved, name)
			}

			opts := engine.Options{Adapters: resolved, DryRun: dryRun, ToSource: toSource}
			if !noInteractive {
				opts.OnConflict = interactiveResolver()
				opts.BaseLookup = baseLookup()
			}
			changes, err := engine.Import(cfg, opts)
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if !dryRun {
				recordSnapshot(changes)
			}
			report(changes, false, dryRun)
			return exitErr(changes)
		},
	}
}
