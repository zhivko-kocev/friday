package cli

import (
	"encoding/json"
	"fmt"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
)

// statusJSON is the machine-readable shape of `friday status --json`,
// intended for CI and tooling. summary.conflict > 0 pairs with exit code 2.
type statusJSON struct {
	Store    string          `json:"store"`
	Adapters []adapterStatus `json:"adapters"`
	Summary  map[string]int  `json:"summary"`
}

type adapterStatus struct {
	Name      string       `json:"name"`
	Target    string       `json:"target"`
	Installed bool         `json:"installed"`
	Changes   []changeJSON `json:"changes"`
}

type changeJSON struct {
	Action  string   `json:"action"`
	Sources []string `json:"sources,omitempty"`
	Dest    string   `json:"dest,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Warning string   `json:"warning,omitempty"`
}

// buildStatusJSON is a pure function so tests assert on the struct instead
// of capturing stdout.
func buildStatusJSON(cfg *config.Config, changes []engine.Change) statusJSON {
	out := statusJSON{
		Store: cfg.StoreDir,
		Summary: map[string]int{
			"created": 0, "updated": 0, "in_sync": 0, "conflict": 0, "skipped": 0,
		},
	}
	byAdapter := map[string][]changeJSON{}
	for _, ch := range changes {
		byAdapter[ch.Adapter] = append(byAdapter[ch.Adapter], changeJSON{
			Action:  ch.Action.String(),
			Sources: ch.Sources,
			Dest:    ch.DestRel,
			Reason:  ch.Reason,
			Warning: ch.Warning,
		})
		switch ch.Action {
		case engine.ActionCreate:
			out.Summary["created"]++
		case engine.ActionUpdate:
			out.Summary["updated"]++
		case engine.ActionInSync:
			out.Summary["in_sync"]++
		case engine.ActionConflict:
			out.Summary["conflict"]++
		case engine.ActionMissingSource, engine.ActionUnsupported:
			out.Summary["skipped"]++
		}
	}
	for _, name := range cfg.AdapterNames() {
		abs, _ := cfg.AdapterTargetAbs(name)
		out.Adapters = append(out.Adapters, adapterStatus{
			Name:      name,
			Target:    abs,
			Installed: dirExists(abs),
			Changes:   byAdapter[name],
		})
	}
	return out
}

func printStatusJSON(cfg *config.Config, changes []engine.Change) error {
	blob, err := json.MarshalIndent(buildStatusJSON(cfg, changes), "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(blob))
	return nil
}
