package cli

import (
	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// cmdPlugin manages out-of-tree presets living in ~/.friday/plugins/*.yaml.
// Plugins join the no-manifest fallback set (a plugin may shadow a built-in);
// an explicit friday.yaml always wins and is never mutated by plugins.
func cmdPlugin(args []string) int {
	if len(args) == 0 {
		output.Err("usage: friday plugin list|validate")
		return 1
	}
	storeDir, err := config.UserStoreDir()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	switch args[0] {
	case "list":
		plugins, errs := presets.LoadPlugins(storeDir)
		for _, e := range errs {
			output.Warn("%v", e)
		}
		if len(plugins) == 0 && len(errs) == 0 {
			output.Dim("no plugins — drop a .yaml preset into %s/plugins/ to add one", storeDir)
			return 0
		}
		builtin := map[string]bool{}
		for _, n := range presets.Names() {
			builtin[n] = true
		}
		for name, p := range plugins {
			note := ""
			if builtin[name] {
				note = "  (shadows the built-in preset)"
			}
			output.OK("%-12s → %s  (%d rule(s))%s", name, p.Target, len(p.Rules), note)
		}
		if len(errs) > 0 {
			return 1
		}
		return 0

	case "validate":
		_, errs := presets.LoadPlugins(storeDir)
		for _, e := range errs {
			output.Err("%v", e)
		}
		if len(errs) > 0 {
			return 1
		}
		output.OK("all plugins valid")
		return 0

	default:
		output.Err("unknown plugin subcommand %q (want: list, validate)", args[0])
		return 1
	}
}
