package cli

import (
	"flag"
	"fmt"

	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

type rollbackOpts struct {
	list, dryRun bool
}

func rollbackFlags(o *rollbackOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.BoolVar(&o.list, "list", false, "list recorded snapshots")
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would be restored without writing")
	return fs
}

// cmdRollback restores the target state recorded at a previous push — the
// safety net for "I pushed and it ate something". No id = latest snapshot.
func cmdRollback(args []string) int {
	var o rollbackOpts
	fs := rollbackFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	dir, err := snapshot.Dir()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	if o.list {
		snaps, err := snapshot.List(dir)
		if err != nil {
			output.Err("%v", err)
			return 1
		}
		if len(snaps) == 0 {
			output.Dim("no snapshots recorded yet — snapshots are taken on every push")
			return 0
		}
		for i := len(snaps) - 1; i >= 0; i-- { // newest first
			s := snaps[i]
			output.Info("%s  %s  %d file(s)", s.ID, s.Time.Local().Format("2006-01-02 15:04:05"), len(s.Files))
		}
		return 0
	}

	id := ""
	if fs.NArg() > 1 {
		output.Err("usage: friday rollback [--list] [--dry-run] [<id>]")
		return 1
	}
	if fs.NArg() == 1 {
		id = fs.Arg(0)
	}
	snap, err := snapshot.Get(dir, id)
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	if o.dryRun {
		output.Header(fmt.Sprintf("rollback %s (dry-run)", snap.ID))
		for _, f := range snap.Files {
			if f.OldHash == "" {
				output.Info("would-delete   %s", f.Path)
			} else {
				output.Info("would-restore  %s", f.Path)
			}
		}
		return 0
	}

	driftPath, err := drift.DefaultPath()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	ds, err := drift.Load(driftPath)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	restored, err := snapshot.Restore(dir, snap, ds)
	for _, p := range restored {
		output.OK("restored %s", p)
	}
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if err := ds.Save(); err != nil {
		output.Warn("failed to save drift store: %v", err)
	}
	output.OK("rolled back to the state before snapshot %s", snap.ID)
	return 0
}
