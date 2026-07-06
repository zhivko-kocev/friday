package cli

import (
	"github.com/zhivko-kocev/friday/internal/lint"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdLint — static store checks: malformed frontmatter, oversized files,
// broken relative refs, destination collisions. Exit 1 on findings.
func cmdLint(args []string) int {
	if len(args) > 0 {
		output.Err("friday lint takes no arguments")
		return 1
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	findings, err := lint.Run(cfg)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if len(findings) == 0 {
		output.OK("store is clean")
		return 0
	}
	for _, f := range findings {
		output.Warn("%-15s %s — %s", f.Rule, f.Path, f.Msg)
	}
	output.Err("%d finding(s)", len(findings))
	return 1
}
