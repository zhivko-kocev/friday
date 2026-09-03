package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	ucli "github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// newMigrateCommand rewrites a friday.yaml seeded by a pre-v1.0 release into
// one the current friday understands: legacy adapter keys renamed to their
// canonical names (with that adapter's target and rules refreshed from the
// built-in preset, since the old ones name surfaces that no longer exist),
// and every retired-strategy rule dropped.
//
// config.load degrades around both rather than refusing to open the manifest,
// so this is not a rescue command — it is the documented way to stop the
// per-run warnings and get a renamed adapter rendering again.
func newMigrateCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "migrate",
		Category: "advanced",
		Usage:    "rewrite a friday.yaml written by an older release",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "dry-run", Usage: "show what would change without writing"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if cmd.Args().Len() > 0 {
				output.Err("friday migrate takes flags only — got positional arg %q", cmd.Args().First())
				return ucli.Exit("", 1)
			}
			storeDir, err := config.UserStoreDir()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			path := filepath.Join(storeDir, config.ManifestName)
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					output.Err("no %s at %s — nothing to migrate", config.ManifestName, storeDir)
				} else {
					output.Err("%v", err)
				}
				return ucli.Exit("", 1)
			}

			dryRun := cmd.Bool("dry-run")
			changes, err := migrateManifest(path, dryRun)
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if len(changes) == 0 {
				output.OK("%s is already current — nothing to migrate", path)
				return nil
			}
			for _, c := range changes {
				if dryRun {
					output.Skip("would %s", c)
				} else {
					output.OK("%s", c)
				}
			}
			if dryRun {
				output.Info("dry run — %s unchanged", path)
			} else {
				output.OK("migrated %s", path)
			}
			return nil
		},
	}
}

// errNoManifestWrite aborts config.EditManifest's write without failing the
// command. It is how --dry-run runs the migration over the real node tree
// instead of a second, drift-prone simulation of it, and how an
// already-current manifest is left byte-identical rather than re-encoded.
var errNoManifestWrite = errors.New("no manifest write needed")

// migrateManifest applies every stale-manifest rewrite to path and returns
// one human-readable line per change. Nothing is written when dryRun is set
// or when there was nothing to change.
func migrateManifest(path string, dryRun bool) ([]string, error) {
	var changes []string
	err := config.EditManifest(path, func(doc *yaml.Node) error {
		var err error
		if changes, err = rewriteStaleManifest(doc); err != nil {
			return err
		}
		if dryRun || len(changes) == 0 {
			return errNoManifestWrite
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoManifestWrite) {
		return nil, err
	}
	return changes, nil
}

// rewriteStaleManifest mutates the manifest's node tree in place. Working on
// nodes rather than the parsed Config is what keeps every comment, key order,
// and untouched adapter byte-identical.
func rewriteStaleManifest(doc *yaml.Node) ([]string, error) {
	adapters := config.MappingValue(doc, "adapters")
	if adapters == nil || adapters.Kind != yaml.MappingNode {
		return nil, nil
	}
	changes, err := renameLegacyAdapters(adapters)
	if err != nil {
		return nil, err
	}
	return append(changes, dropRetiredRules(adapters)...), nil
}

// renameLegacyAdapters moves each config.LegacyAdapterNames key to its
// canonical name and refreshes that adapter's target and rules from the
// current preset. The two halves are inseparable: an adapter was renamed
// because its surface moved, so the rules the old release wrote under the old
// key no longer describe anything real. Every other key the user added under
// that adapter (project_target, project_rules) is left alone, as is the
// adapter's position in the file.
func renameLegacyAdapters(adapters *yaml.Node) ([]string, error) {
	var changes []string
	for _, legacy := range slices.Sorted(maps.Keys(config.LegacyAdapterNames)) {
		if config.MappingValue(adapters, legacy) == nil {
			continue
		}
		canonical := config.LegacyAdapterNames[legacy]
		if config.MappingValue(adapters, canonical) != nil {
			config.DeleteMappingKey(adapters, legacy)
			changes = append(changes, fmt.Sprintf(
				"drop adapter %q (superseded by %q, already present)", legacy, canonical))
			continue
		}
		p, ok := presets.Get(canonical)
		if !ok {
			return nil, fmt.Errorf("adapter %q was renamed to %q, but no built-in preset by that name exists", legacy, canonical)
		}
		config.RenameMappingKey(adapters, legacy, canonical)
		adapter := config.MappingValue(adapters, canonical)
		if adapter.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("adapter %q is not a mapping", legacy)
		}
		target, err := config.EncodeNode(p.Target)
		if err != nil {
			return nil, err
		}
		ruleList, err := config.EncodeNode(p.Rules)
		if err != nil {
			return nil, err
		}
		config.SetMappingValue(adapter, "target", target)
		config.SetMappingValue(adapter, "rules", ruleList)
		changes = append(changes, fmt.Sprintf(
			"rename adapter %q to %q and reset its target/rules to the %s preset", legacy, canonical, canonical))
	}
	return changes, nil
}

// dropRetiredRules removes every rule naming a strategy friday no longer
// implements. config.load already drops these at read time, so leaving them
// in the file only reprints the same warning on every command.
func dropRetiredRules(adapters *yaml.Node) []string {
	var changes []string
	for i := 0; i+1 < len(adapters.Content); i += 2 {
		name, adapter := adapters.Content[i].Value, adapters.Content[i+1]
		if adapter.Kind != yaml.MappingNode {
			continue
		}
		for _, key := range []string{"rules", "project_rules"} {
			list := config.MappingValue(adapter, key)
			if list == nil || list.Kind != yaml.SequenceNode {
				continue
			}
			kept := make([]*yaml.Node, 0, len(list.Content))
			for j, rule := range list.Content {
				strategy := retiredStrategy(rule)
				if strategy == "" {
					kept = append(kept, rule)
					continue
				}
				changes = append(changes, fmt.Sprintf(
					"drop adapter %q %s[%d]: retired strategy %q", name, key, j, strategy))
			}
			switch {
			case len(kept) == len(list.Content):
			case len(kept) == 0:
				config.DeleteMappingKey(adapter, key)
			default:
				list.Content = kept
			}
		}
	}
	return changes
}

// retiredStrategy names the rule's strategy when it is one friday no longer
// implements, else "".
func retiredStrategy(rule *yaml.Node) string {
	if rule.Kind != yaml.MappingNode {
		return ""
	}
	strategy := config.MappingValue(rule, "strategy")
	if strategy == nil {
		return ""
	}
	switch strategy.Value {
	case rules.StrategyMDToTOML, rules.StrategyMDToJSON:
		return strategy.Value
	}
	return ""
}
