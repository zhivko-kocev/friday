package cli

import (
	"context"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// newTransferCommand moves one agent's config to another, always through
// the store (D19): capture <from>'s current config into the store, then
// push exactly what was just captured to <to>. Rendering from→to directly
// would create target config friday has no baseline for, which is drift by
// construction — going through the store gives the transfer a real
// canonical source and lets a later `friday push <to>` or `friday pull
// <to>` behave exactly as if the file had always lived there.
//
//	friday transfer claude codex            → apply both halves
//	friday transfer claude codex --dry-run  → preview both halves, write nothing
func newTransferCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "transfer",
		Category:  "advanced",
		Usage:     "move one agent's config to another, through the store",
		ArgsUsage: "<from> <to>",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "dry-run", Usage: "show what would be captured and pushed without writing"},
			&ucli.BoolFlag{Name: "no-interactive", Usage: "don't prompt on conflicts; treat them as skip"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			args := cmd.Args().Slice()
			if len(args) != 2 {
				output.Err("friday transfer requires exactly two arguments: <from> <to>")
				return ucli.Exit("", 1)
			}
			dryRun := cmd.Bool("dry-run")
			// Both halves prompt on a store/target collision, so a scripted
			// transfer with no opt-out blocks on stdin forever.
			var resolver engine.ConflictResolver
			var base func(string) ([]byte, bool)
			if !cmd.Bool("no-interactive") {
				resolver = interactiveResolver()
				base = baseLookup()
			}

			cfg, err := loadUserOrDefault()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			fromName, err := resolveAdapterArg(cfg, args[0])
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			toName, err := resolveAdapterArg(cfg, args[1])
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}

			// Half one: capture fromName's current config into the store —
			// reuses the same conflict resolver/confirmer pull already uses,
			// so a collision with existing store content prompts exactly as
			// `friday pull` does today.
			importChanges, err := engine.Import(cfg, engine.Options{
				Adapters:   []string{fromName},
				DryRun:     dryRun,
				OnConflict: resolver,
				BaseLookup: base,
			})
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if !dryRun {
				recordSnapshot(importChanges)
			}
			output.Header("capture: " + fromName + " -> store")
			report(importChanges, false, dryRun)

			// Only the files that are (or already were) genuinely in the
			// store after that capture travel onward — a conflict the user
			// skipped, or a rule Import cannot represent in reverse
			// (concatenate, frontmatter_strip), contributes nothing toName
			// didn't already have.
			var only []string
			for _, ch := range importChanges {
				switch ch.Action {
				case engine.ActionCreate, engine.ActionUpdate, engine.ActionInSync:
					only = append(only, ch.DestRel)
				}
			}
			if len(only) == 0 {
				output.Warn("nothing captured from %s — nothing to transfer to %s", fromName, toName)
				return exitErr(importChanges)
			}

			// Half two: push exactly those store-relative paths to toName.
			pushChanges, err := engine.Push(cfg, engine.Options{
				Adapters:   []string{toName},
				Only:       only,
				DryRun:     dryRun,
				OnConflict: resolver,
				BaseLookup: base,
			})
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if !dryRun {
				recordSnapshot(pushChanges)
			}
			output.Header("push: store -> " + toName)
			report(pushChanges, false, dryRun)

			return exitErr(append(importChanges, pushChanges...))
		},
	}
}
