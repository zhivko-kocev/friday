package cli

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdExplain answers "which adapter + rule produced this file?" by planning
// a dry-run push over every adapter — the same code path that writes, so
// there is exactly one source of truth for who writes what. Multiple matches
// all print: two rules writing one destination is a misconfiguration explain
// exists to expose.
func cmdExplain(args []string) int {
	if len(args) != 1 {
		output.Err("usage: friday explain <target-file>")
		return 1
	}
	abs, err := filepath.Abs(args[0])
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	changes, err := engine.Push(cfg, engine.Options{DryRun: true})
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	found := 0
	for _, ch := range changes {
		if !samePath(ch.DestPath, abs) {
			continue
		}
		found++
		r := cfg.Adapters[ch.Adapter].Rules[ch.RuleIndex]
		output.Header(ch.DestRel)
		output.Info("adapter:   %s", ch.Adapter)
		output.Info("rule:      from %v → %s  (strategy: %s)", []string(r.From), r.To, r.Strategy)
		if len(r.FrontmatterStrip) > 0 {
			output.Info("           frontmatter_strip: %v", r.FrontmatterStrip)
		}
		if len(r.Replace) > 0 {
			for k, v := range r.Replace {
				output.Info("           replace: %q → %q", k, v)
			}
		}
		output.Info("sources:   %s", strings.Join(ch.Sources, ", "))
		output.Info("state:     %s", ch.Action)
		if ch.Reason != "" {
			output.Dim("           %s", ch.Reason)
		}
	}
	if found == 0 {
		output.Warn("no rule produces %s", abs)
		suggestNearest(changes, args[0])
		return 1
	}
	if found > 1 {
		output.Warn("%d rules write this file — later rules win; consider removing the overlap", found)
	}
	return 0
}

// suggestNearest points at planned destinations whose relative path ends in
// the same basename, for the common "right file, wrong directory" miss.
func suggestNearest(changes []engine.Change, arg string) {
	base := filepath.Base(arg)
	for _, ch := range changes {
		if filepath.Base(ch.DestRel) == base {
			output.Dim("did you mean %s (%s)?", ch.DestPath, ch.Adapter)
		}
	}
}

// samePath compares absolute paths, case-insensitively on Windows.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
