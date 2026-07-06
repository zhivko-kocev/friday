package cli

import (
	"bufio"
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

type ejectOpts struct {
	yes bool
}

func ejectFlags(o *ejectOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("eject", flag.ContinueOnError)
	fs.BoolVar(&o.yes, "yes", false, "skip the confirmation prompt")
	return fs
}

// cmdEject is the clean exit door: capture the current target state back
// into the store (full fidelity, via import), then remove friday's tracking
// — the manifest, the drift cache, and the snapshots. The store content
// itself stays; it's the user's data. Targets are untouched.
func cmdEject(args []string) int {
	var o ejectOpts
	fs := ejectFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	// 1. Capture: reverse-expand every installed adapter into the store so
	// nothing authored directly in a target dir is lost on the way out.
	installed := installedAdapters(cfg)
	if len(installed) > 0 {
		changes, err := engine.Import(cfg, engine.Options{Adapters: installed, Force: true})
		if err != nil {
			output.Err("capture failed, nothing removed: %v", err)
			return 1
		}
		captured := 0
		for _, ch := range changes {
			if ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate {
				captured++
				output.OK("captured %s", ch.DestRel)
			}
		}
		if captured == 0 {
			output.Dim("targets already reflected in the store — nothing to capture")
		}
	}

	// 2. Confirm.
	if !o.yes && !confirmEject() {
		output.Dim("aborted — nothing removed")
		return 1
	}

	// 3. Remove friday's bookkeeping.
	removed := func(path string, err error) {
		if err != nil && !os.IsNotExist(err) {
			output.Warn("remove %s: %v", path, err)
			return
		}
		output.OK("removed %s", path)
	}
	manifest := filepath.Join(cfg.StoreDir, config.ManifestName)
	removed(manifest, os.Remove(manifest))
	if driftPath, err := drift.DefaultPath(); err == nil {
		removed(driftPath, os.Remove(driftPath))
	}
	if snapDir, err := snapshot.Dir(); err == nil {
		removed(snapDir, os.RemoveAll(snapDir))
	}
	output.OK("ejected — %s keeps your content; agents keep their configs", cfg.StoreDir)
	return 0
}

func confirmEject() bool {
	output.Warn("this removes friday.yaml, the drift cache, and all push snapshots")
	output.Info("type \"eject\" to confirm:")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return strings.TrimSpace(line) == "eject"
}
